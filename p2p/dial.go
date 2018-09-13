package p2p

import (
	"container/heap"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/czh0526/agent/log"
	"github.com/czh0526/agent/p2p/discover"
)

const (
	// 可以重新尝试连接的阈值
	dialHistoryExpiration = 30 * time.Second
	fallbackInterval      = 20 * time.Second
)

type discoverTable interface {
	Self() *discover.Node
	Close()
	Resolve(target discover.NodeID) *discover.Node
	Lookup(target discover.NodeID) []*discover.Node
	ReadRandomNodes([]*discover.Node) int
}

type connFlag int

const (
	dynDialedConn connFlag = 1 << iota
	staticDialedConn
	inboundConn
	trustedConn
)

func (f connFlag) String() string {
	s := ""
	if f&trustedConn != 0 {
		s += "-trusted"
	}
	if f&dynDialedConn != 0 {
		s += "-dyndial"
	}
	if f&staticDialedConn != 0 {
		s += "-staticdial"
	}
	if f&inboundConn != 0 {
		s += "-inbound"
	}

	if s != "" {
		s = s[1:]
	}
	return s
}

type NodeDialer interface {
	Dial(*discover.Node) (net.Conn, error)
}

type TCPDialer struct {
	*net.Dialer
}

func (t TCPDialer) Dial(dest *discover.Node) (net.Conn, error) {
	addr := &net.TCPAddr{IP: dest.IP, Port: int(dest.TCP)}
	return t.Dialer.Dial("tcp", addr.String())
}

type dialHistory []pastDial

type pastDial struct {
	id  discover.NodeID
	exp time.Time
}

func (h dialHistory) min() pastDial {
	return h[0]
}

func (h *dialHistory) expire(now time.Time) {
	for h.Len() > 0 && h.min().exp.Before(now) {
		heap.Pop(h)
	}
}

func (h dialHistory) contains(id discover.NodeID) bool {
	for _, v := range h {
		if v.id == id {
			return true
		}
	}
	return false
}

func (h *dialHistory) add(id discover.NodeID, exp time.Time) {
	heap.Push(h, pastDial{id, exp})
}

func (h dialHistory) Len() int           { return len(h) }
func (h dialHistory) Less(i, j int) bool { return h[i].exp.Before(h[j].exp) }
func (h dialHistory) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *dialHistory) Push(x interface{}) {
	*h = append(*h, x.(pastDial))
}
func (h *dialHistory) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

type dialstate struct {
	maxDynDials int
	ntab        discoverTable

	lookupRunning bool             // discoverTask 是否正在执行
	lookupBuf     []*discover.Node // discoverTask 任务的执行结果
	dialing       map[discover.NodeID]connFlag
	randomNodes   []*discover.Node
	hist          *dialHistory

	start     time.Time
	bootnodes []*discover.Node
}

func newDialState(bootnodes []*discover.Node, ntab discoverTable, maxdyn int) *dialstate {
	s := &dialstate{
		maxDynDials: maxdyn,
		ntab:        ntab,
		dialing:     make(map[discover.NodeID]connFlag),
		bootnodes:   make([]*discover.Node, len(bootnodes)),
		randomNodes: make([]*discover.Node, maxdyn/2),
		hist:        new(dialHistory),
	}
	copy(s.bootnodes, bootnodes)
	return s
}

type dialError struct {
	error
}

type task interface {
	Type() string
	Do(Server)
}

type dialTask struct {
	flags        connFlag
	dest         *discover.Node
	lastResolved time.Time
	resolveDelay time.Duration
}

func (t *dialTask) Type() string {
	return "dialTask"
}

func (t *dialTask) Do(srv Server) {
	log.Debug("dialTask.Do()", "dest node", t.dest)
	err := t.dial(srv, t.dest)
	if err != nil {
		log.Error("dialTask.Do()", "error", err)
	}
}

func (t *dialTask) dial(srv Server, dest *discover.Node) error {
	fd, err := srv.Dialer().Dial(dest)
	if err != nil {
		return &dialError{err}
	}
	log.Info("连接方：构建一个 TCP 连接", "dest node", dest.IP, "port", dest.TCP)
	return srv.SetupConn(fd, t.flags, dest)
}

func (t *dialTask) String() string {
	return fmt.Sprintf("%v %x %v:%d", t.flags, t.dest.ID[:8], t.dest.IP, t.dest.TCP)
}

type discoverTask struct {
	results []*discover.Node
}

func (t *discoverTask) Type() string {
	return "discoverTask"
}

func (t *discoverTask) Do(srv Server) {
	var target discover.NodeID
	rand.Read(target[:])
	t.results = srv.Lookup(target)
	log.Debug("discoverTask.Do()", "find nodes num", len(t.results))
}

func (t *discoverTask) String() string {
	s := "discovery lookup"
	if len(t.results) > 0 {
		s += fmt.Sprintf(" (%d results)", len(t.results))
	}
	return s
}

type waitExpireTask struct {
	time.Duration
}

func (t waitExpireTask) Type() string {
	return "waitExpireTask"
}

func (t waitExpireTask) Do(Server) {
	time.Sleep(t.Duration)
}

func (t waitExpireTask) String() string {
	return fmt.Sprintf("wait for dial hist expire (%v)", t.Duration)
}

func (s *dialstate) newTasks(nRunning int, peers map[discover.NodeID]*Peer, now time.Time) []task {
	//	if s.start.IsZero() {
	//		s.start = now
	//	}

	var newtasks []task
	addDial := func(flag connFlag, n *discover.Node) bool {
		if err := s.checkDial(n, peers); err != nil {
			return false
		}
		s.dialing[n.ID] = flag
		newtasks = append(newtasks, &dialTask{flags: flag, dest: n})
		return true
	}

	/*
		计算需要构建的 Task 数量
	*/
	needDynDials := s.maxDynDials

	// 减去已经建立连接的
	for _, p := range peers {
		if p.rw.is(dynDialedConn) {
			needDynDials--
		} else {
			fmt.Printf("peer is a %v conn.\n", p.rw.flags)
		}
	}

	// 减去正在建立连接的
	for _, flag := range s.dialing {
		if flag&dynDialedConn != 0 {
			needDynDials--
		}
	}

	/*
		构建 dialTask
	*/
	s.hist.expire(now)

	// bootnode: dialTask ==> newtasks
	if len(peers) == 0 && len(s.bootnodes) > 0 && needDynDials > 0 && now.Sub(s.start) > fallbackInterval {
		// shift 第一个 bootnode
		bootnode := s.bootnodes[0]
		s.bootnodes = append(s.bootnodes[:0], s.bootnodes[1:]...)
		s.bootnodes = append(s.bootnodes, bootnode)

		if addDial(dynDialedConn, bootnode) {
			needDynDials--
		}
	}
	fmt.Printf("[dialstate] -> newTasks(): 需要构建 %v 个拨号任务. \n", needDynDials)

	// randomNodes: dialTask ==> newtasks
	randomCandidates := needDynDials / 2
	if randomCandidates > 0 {
		// 填充 s.randomNodes 中的条目
		n := s.ntab.ReadRandomNodes(s.randomNodes)
		// 为选中中随机节点添加 Task
		i := 0
		for ; i < randomCandidates && i < n; i++ {
			if addDial(dynDialedConn, s.randomNodes[i]) {
				needDynDials--
			}
		}
		fmt.Printf("[dialstate] -> newTasks(): 从 discoverTable 中构建了 %v 个任务. \n", i)
	}

	// lookupBuf: dialTask ==> newtasks
	i := 0
	for ; i < len(s.lookupBuf) && needDynDials > 0; i++ {
		if addDial(dynDialedConn, s.lookupBuf[i]) {
			needDynDials--
		}
	}
	fmt.Printf("[dialstate] -> newTasks(): 从 lookupBuf 中构建了 %v 个任务. \n", i)

	// discoverTask ==> newtasks
	s.lookupBuf = s.lookupBuf[:copy(s.lookupBuf, s.lookupBuf[i:])]
	if len(s.lookupBuf) < needDynDials && !s.lookupRunning {
		s.lookupRunning = true
		newtasks = append(newtasks, &discoverTask{})
		log.Trace("[dialstate] -> newTasks(): 没有足够的 dialTask，构建一个 discoverTask.")
	}

	// waitExpireTask ==> newtasks
	if nRunning == 0 && len(newtasks) == 0 {
		t := &waitExpireTask{s.hist.min().exp.Sub(now)}
		newtasks = append(newtasks, t)
		log.Trace("[dialstate] -> newTasks(): 不能创建 dialTask 和 discoverTask, 构建一个 waitExpireTask, 准备重连历史节点.")
	}
	return newtasks
}

var (
	errSelf             = errors.New("is self")
	errAlreadyDialing   = errors.New("already dialing")
	errAlreadyConnected = errors.New("already connected")
	errRecentlyDialed   = errors.New("recently dialed")
	errNotWhitelisted   = errors.New("not contained in netrestrict whitelist")
)

func (s *dialstate) checkDial(n *discover.Node, peers map[discover.NodeID]*Peer) error {
	fmt.Printf("校验拨号任务：==> %v \n", n.IP)
	for id, p := range peers {
		fmt.Printf("    p.id = %x..., p.ip = %v \n", id[:8], p.rw.fd.RemoteAddr())
	}
	_, dialing := s.dialing[n.ID]
	switch {
	case dialing:
		fmt.Printf("正在拨号: ==> %v \n", n.IP)
		return errAlreadyDialing
	case peers[n.ID] != nil:
		fmt.Printf("已经建立连接: ==> %v \n", n.IP)
		return errAlreadyConnected
	case s.ntab != nil && n.ID == s.ntab.Self().ID:
		fmt.Printf("向自身拨号: ==> %v \n", n.IP)
		return errSelf
	case s.hist.contains(n.ID):
		fmt.Printf("间隔太近: ==> %v \n", n.IP)
		return errRecentlyDialed
	}
	fmt.Printf("正常：==> %v \n", n.IP)
	return nil
}

func (self *dialstate) taskDone(t task, now time.Time) {
	switch t := t.(type) {
	case *dialTask:
		// 30 秒钟后，可以重新连接该节点
		self.hist.add(t.dest.ID, now.Add(dialHistoryExpiration))
		delete(self.dialing, t.dest.ID)
	case *discoverTask:
		self.lookupRunning = false
		// 将发现的新节点放入缓存
		self.lookupBuf = append(self.lookupBuf, t.results...)
	}
}
