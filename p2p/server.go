package p2p

import (
	"crypto/ecdsa"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"time"

	"github.com/czh0526/agent/crypto"
	"github.com/czh0526/agent/log"
	"github.com/czh0526/agent/p2p/discover"
	"github.com/czh0526/agent/params"
)

const (
	defaultDialTimeout = 60 * time.Second
	maxActiveDialTasks = 16
)

type P2PServer struct {
	PrivateKey      *ecdsa.PrivateKey
	ListenAddr      string
	MaxPeers        int
	MaxPendingPeers int

	newTransport func(net.Conn) transport

	listener net.Listener
	ntab     discoverTable
	dialer   NodeDialer

	running   bool
	quit      chan struct{}
	addpeer   chan *conn
	Protocols []Protocol

	loopWG sync.WaitGroup
	log    log.Logger
}

func NewServer() *P2PServer {
	return &P2PServer{
		ListenAddr:      "0.0.0.0:65353",
		MaxPeers:        10,
		MaxPendingPeers: 3,
		newTransport:    newRLPX,
		running:         false,
		quit:            make(chan struct{}),
		log:             log.New(),
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
	log.Info("监听 UDP", "udp address", realaddr)

	bootnodes := []*discover.Node{
		discover.MustParseNode("enode://913416ec113671505ba3a532a884a337d7303837c2e6e0aebf7754f0fdf6db976c6b314e769fb01bef49be52bbee03d32a4901609c4d1fe992024ba7ca8edc5d@139.199.100.150:65353"),
	}

	// 加载私钥
	nodeKeyPath, err := filepath.Abs(filepath.Join(params.HomeDir, "nodekey"))
	if err != nil {
		return err
	}
	privateKey, err := crypto.LoadECDSA(nodeKeyPath)
	if err != nil {
		return err
	}
	self.PrivateKey = privateKey
	log.Info("加载 nodekey", "NodeID", discover.PubkeyID(&privateKey.PublicKey).String())

	nodeDBPath, err := filepath.Abs(filepath.Join(params.HomeDir, "nodes"))
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
	log.Info("构建 discoverTable.")

	// 启动 tcp 监听
	if self.ListenAddr != "" {
		if err := self.startListening(); err != nil {
			return err
		}
		log.Info("监听 TCP", "Listen Address", self.ListenAddr)
	}

	// 启动 tcp 拨号例程
	self.loopWG.Add(1)
	dialstate := newDialState(bootnodes, self.ntab, 15) // bootnodes 作为 [tcp]Dial 连接过程的必要节点
	log.Info("构建 dialstate, 管理拨号状态.")
	go self.run(dialstate)
	log.Info("进入循环：构建各种 Task 并执行.")

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
			log.Info("监听方：构建一个 TCP 连接", "dest node", fd.RemoteAddr())
			self.SetupConn(fd, inboundConn, nil)
			// 归还一个令牌
			slots <- struct{}{}
		}()
	}
	fmt.Println("[P2PServer] -> listenLoop(): exited.")
}

func (self *P2PServer) SetupConn(fd net.Conn, flags connFlag, dialDest *discover.Node) error {
	c := &conn{fd: fd, transport: self.newTransport(fd), flags: flags}
	err := self.setupConn(c, flags, dialDest)
	if err != nil {
		return err
	}
	return nil
}

func (self *P2PServer) setupConn(c *conn, flags connFlag, dialDest *discover.Node) error {
	var err error
	if c.id, err = c.doEncHandshake(self.PrivateKey, dialDest); err != nil {
		self.log.Error("Failed RLPx handshake", "addr", c.fd.RemoteAddr(), "err", err)
		return err
	}

	self.addpeer <- c
	return nil
}

type conn struct {
	fd net.Conn
	transport
	flags connFlag
	id    discover.NodeID
}

type transport interface {
	doEncHandshake(prv *ecdsa.PrivateKey, dialDest *discover.Node) (discover.NodeID, error)
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

	// 启动 ts 中的任务，让 runningTasks 中的任务数量尽量饱满
	startTasks := func(ts []task) (rest []task) {
		i := 0
		for ; len(runningTasks) < maxActiveDialTasks && i < len(ts); i++ {
			t := ts[i]
			go func() {
				log.Trace("启动任务", "task", t.Type())
				t.Do(self)
				taskdone <- t
			}()
			runningTasks = append(runningTasks, t)
		}
		return ts[i:]
	}

	// 如果 queuedTasks 不能让 runningTasks 尽量饱满，
	// 构建新的 Task 集合，填充 runningTasks 和 queuedTasks
	scheduleTasks := func() {
		// queuedTasks ==> runningTasks
		queuedTasks = append(queuedTasks[:0], startTasks(queuedTasks)...)
		// 如果 runningTask 没跑满
		if len(runningTasks) < maxActiveDialTasks {
			// 新构建一批任务
			nt := dialstate.newTasks(len(runningTasks)+len(queuedTasks), peers, time.Now())
			log.Debug("newTasks() --> startTasks() ", "task num", len(nt))
			// 分别放入 runningTasks 和 queuedTasks 中
			queuedTasks = append(queuedTasks, startTasks(nt)...)
		}
	}

running:
	for {
		log.Trace("P2PServer.run() ->", "peer number", len(peers))
		//<-time.After(time.Second * 3)
		scheduleTasks() // 如果未能成功启动 Task,

		// 监听外部退出事件
		select {
		case <-self.quit:
			break running
		case t := <-taskdone:
			dialstate.taskDone(t, time.Now())
			delTask(t)
		case c := <-self.addpeer:
			p := newPeer(c)
			go self.runPeer(p)
			peers[c.id] = p
		}
	}
	fmt.Println("[P2PServer] -> loop(): exited.")
}

func (self *P2PServer) runPeer(p *Peer) {
	p.run()
}

func (self *P2PServer) Stop() error {
	fmt.Println("[P2PServer] -> Stop():")
	self.running = false
	self.listener.Close() // 关闭 listenLoop()
	close(self.quit)      // 关闭 run()
	self.ntab.Close()     // 关闭 discoverTable
	return nil
}

func (self *P2PServer) IsRunning() bool {
	return self.running
}

func (self *P2PServer) Dialer() NodeDialer {
	return self.dialer
}

func (self *P2PServer) Lookup(target discover.NodeID) []*discover.Node {
	return self.ntab.Lookup(target)
}
