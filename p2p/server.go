package p2p

import (
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"time"

	"github.com/czh0526/agent/crypto"
	"github.com/czh0526/agent/p2p/discover"
)

const (
	defaultDialTimeout = 15 * time.Second
	maxActiveDialTasks = 16
)

type P2PServer struct {
	ListenAddr      string
	MaxPeers        int
	MaxPendingPeers int
	listener        net.Listener
	ntab            discoverTable
	dialer          NodeDialer

	running bool
	quit    chan struct{}
	addpeer chan *conn

	loopWG sync.WaitGroup
}

func NewServer() Server {
	return &P2PServer{
		ListenAddr:      "0.0.0.0:65353",
		MaxPeers:        10,
		MaxPendingPeers: 3,
		running:         false,
		quit:            make(chan struct{}),
	}
}

func (self *P2PServer) Start() error {

	self.running = true
	self.addpeer = make(chan *conn)

	// 处理本地地址和种子节点
	var (
		conn     *net.UDPConn
		realaddr *net.UDPAddr
	)
	addr, err := net.ResolveUDPAddr("udp", self.ListenAddr)
	if err != nil {
		return err
	}

	conn, err = net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	realaddr = conn.LocalAddr().(*net.UDPAddr)
	if err != nil {
		return err
	}
	fmt.Printf("[P2PServer] -> Start(): realaddr = %v \n", realaddr)

	bootnodes := []*discover.Node{
		discover.MustParseNode("enode://0f231b57ffe1a1b69dcd5e6fbed3ea4bc2e903eae6e6295aca2abf92e264652945219403ecdb99a8523c485e6dfd05f1124d332feb89397820843ee7ed2b3a1f@139.199.100.150:65353"),
	}

	// 加载私钥
	nodeKeyPath, err := filepath.Abs("./nodekey")
	if err != nil {
		return err
	}
	privateKey, err := crypto.LoadECDSA(nodeKeyPath)
	if err != nil {
		return err
	}
	fmt.Printf("self NodeID = %v \n", discover.PubkeyID(&privateKey.PublicKey).String())

	nodeDBPath, err := filepath.Abs("./nodes")
	if err != nil {
		return err
	}

	// 构建拨号器
	if self.dialer == nil {
		self.dialer = TCPDialer{&net.Dialer{Timeout: defaultDialTimeout}}
	}

	cfg := discover.Config{
		PrivateKey:   privateKey,
		AnnounceAddr: realaddr,
		NodeDBPath:   nodeDBPath,
		Bootnodes:    bootnodes, // 作为 [udp]DiscoverTable 发现过程的 nursery.
	}

	// 构建 discoveryTable(网络节点缓存)
	tab, err := discover.ListenUDP(conn, cfg)
	self.ntab = tab

	// 启动 tcp 监听
	if self.ListenAddr != "" {
		if err := self.startListening(); err != nil {
			return err
		}
	}

	// 启动 tcp 拨号例程
	self.loopWG.Add(1)
	dialstate := newDialState(bootnodes, self.ntab, 15) // bootnodes 作为 [tcp]Dial 连接过程的必要节点
	go self.run(dialstate)

	return nil
}

func (self *P2PServer) startListening() error {
	listener, err := net.Listen("tcp", self.ListenAddr)
	if err != nil {
		return err
	}

	laddr := listener.Addr().(*net.TCPAddr)
	self.ListenAddr = laddr.String()
	self.listener = listener

	self.loopWG.Add(1)
	go self.listenLoop()

	return nil
}

func (self *P2PServer) listenLoop() {

	defer self.loopWG.Done()

	// 创建有限个令牌
	tokens := 50
	if self.MaxPendingPeers > 0 {
		tokens = self.MaxPendingPeers
	}
	slots := make(chan struct{}, tokens)
	for i := 0; i < tokens; i++ {
		slots <- struct{}{}
	}

running:
	for {
		// 获取一个令牌
		<-slots

		var (
			fd  net.Conn
			err error
		)

		fmt.Println("[P2PServer] -> listenLoop(): waiting for accepting a conn ...")
		fd, err = self.listener.Accept()
		if err != nil {
			fmt.Printf("[P2PServer] -> listenLoop(): error occur —— %v \n", err)
			break running
		}
		fmt.Printf("[P2PServer] => listenLoop(): get a conn from %v \n", fd.LocalAddr())

		go func() {
			self.SetupConn(fd, inboundConn, nil)
			// 归还一个令牌
			slots <- struct{}{}
		}()
	}
	fmt.Println("[P2PServer] -> listenLoop(): exited.")
}

func (self *P2PServer) SetupConn(fd net.Conn, flag connFlag, node *discover.Node) error {
	c := &conn{fd: fd, flags: flag}
	err := self.setupConn(c)
	if err != nil {
		return err
	}
	return nil
}

func (self *P2PServer) setupConn(c *conn) error {
	self.addpeer <- c
	return nil
}

type conn struct {
	fd    net.Conn
	flags connFlag
	id    discover.NodeID
}

func (c *conn) is(f connFlag) bool {
	return c.flags&f != 0
}

type dialer interface {
	newTasks(running int, peer map[discover.NodeID]*Peer, now time.Time) []task
	taskDone(task, time.Time)
}

func (self *P2PServer) run(dialstate dialer) {
	defer self.loopWG.Done()
	var (
		peers        = make(map[discover.NodeID]*Peer)
		taskdone     = make(chan task, maxActiveDialTasks)
		runningTasks []task
		queuedTasks  []task
	)

	delTask := func(t task) {
		for i := range runningTasks {
			if runningTasks[i] == t {
				runningTasks = append(runningTasks[:i], runningTasks[i+1:]...)
				break
			}
		}
	}

	// 将 ts 集合拆分成 runningTasks 和 queuedTasks
	startTasks := func(ts []task) (rest []task) {
		i := 0
		for ; len(runningTasks) < maxActiveDialTasks && i < len(ts); i++ {
			t := ts[i]
			go func() {
				t.Do(self)
				taskdone <- t
			}()
			runningTasks = append(runningTasks, t)
		}
		return ts[i:]
	}

	scheduleTasks := func() {
		// queuedTasks 中移除
		queuedTasks = append(queuedTasks[:0], startTasks(queuedTasks)...)
		if len(runningTasks) < maxActiveDialTasks {
			// 新构建一批任务
			nt := dialstate.newTasks(len(runningTasks)+len(queuedTasks), peers, time.Now())
			// 分别放入 runningTasks 和 queuedTasks 中
			queuedTasks = append(queuedTasks, startTasks(nt)...)
		}
	}

running:
	for {
		fmt.Printf("[P2PServer]: peer number = %v \n", len(peers))
		scheduleTasks()

		// 监听外部退出事件
		select {
		case <-self.quit:
			break running
		case t := <-taskdone:
			dialstate.taskDone(t, time.Now())
			delTask(t)
		case c := <-self.addpeer:
			p := newPeer(c)
			peers[c.id] = p
		}
	}
	fmt.Println("[P2PServer] -> loop(): exited.")
}

func (self *P2PServer) Stop() error {
	fmt.Println("[P2PServer] -> Stop():")
	self.running = false
	self.listener.Close() // 关闭 listenLoop()
	close(self.quit)      // 关闭 run()
	self.ntab.Close()     // 关闭 discoverTable
	return nil
}

func (self *P2PServer) Dialer() NodeDialer {
	return self.dialer
}

func (self *P2PServer) Lookup(target discover.NodeID) []*discover.Node {
	return self.ntab.Lookup(target)
}
