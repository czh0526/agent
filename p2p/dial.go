package p2p

import (
	"container/heap"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"time"

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

	lookupRunning bool
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
	Do(Server)
}

type dialTask struct {
	flags        connFlag
	dest         *discover.Node
	lastResolved time.Time
	resolveDelay time.Duration
}

func (t *dialTask) Do(srv Server) {
	<-time.After(time.Millisecond * 500)
	err := t.dial(srv, t.dest)
	if err != nil {
		fmt.Printf("dialTask error: %v \n", err)
	}
}

func (t *dialTask) dial(srv Server, dest *discover.Node) error {
	fd, err := srv.Dialer().Dial(dest)
	if err != nil {
		return &dialError{err}
	}
	return srv.SetupConn(fd, t.flags, dest)
}

func (t *dialTask) String() string {
	return fmt.Sprintf("%v %x %v:%d", t.flags, t.dest.ID[:8], t.dest.IP, t.dest.TCP)
}

type discoverTask struct {
	results []*discover.Node
}

func (t *discoverTask) Do(srv Server) {
	<-time.After(time.Millisecond * 500)
	var target discover.NodeID
	rand.Read(target[:])
	t.results = srv.Lookup(target)
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
	}
	fmt.Printf("[dialstate] -> newTasks(): 没有足够的 dialTask，构建一个 discoverTask. \n")

	// waitExpireTask ==> newtasks
	if nRunning == 0 && len(newtasks) == 0 {
		t := &waitExpireTask{s.hist.min().exp.Sub(now)}
		newtasks = append(newtasks, t)
	}
	fmt.Println()
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
	_, dialing := s.dialing[n.ID]
	switch {
	case dialing:
		return errAlreadyDialing
	case peers[n.ID] != nil:
		return errAlreadyConnected
	case s.ntab != nil && n.ID == s.ntab.Self().ID:
		return errSelf
	case s.hist.contains(n.ID):
		return errRecentlyDialed
	}
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
