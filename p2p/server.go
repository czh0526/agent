package p2p

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/czh0526/agent/crypto"
	"github.com/czh0526/agent/p2p/discover"
)

const (
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
		discover.MustParseNode("enode://ba85011c70bcc5c04d8607d3a0ed29aa6179c092cbdda10d5d32684fb33ed01bd94f588ca8f91ac48318087dcb02eaf36773a7a453f0eedd6742af668097b29c@10.0.1.16:30303?discport=30304"),
		discover.MustParseNode("enode://81fa361d25f157cd421c60dcc28d8dac5ef6a89476633339c5df30287474520caca09627da18543d9079b5b288698b542d56167aa5c09111e55acdbbdf2ef799@10.0.1.16:30303"),
		discover.MustParseNode("enode://9bffefd833d53fac8e652415f4973bee289e8b1a5c6c4cbe70abf817ce8a64cee11b823b66a987f51aaa9fba0d6a91b3e6bf0d5a5d1042de8e9eeea057b217f8@10.0.1.36:30301?discport=17"),
		discover.MustParseNode("enode://1b5b4aa662d7cb44a7221bfba67302590b643028197a7d5214790f3bac7aaa4a3241be9e83c09cf1f6c69d007c634faae3dc1b1221793e8446c0b3a09de65960@10.0.1.16:30303"),
	}

	privateKey, err := crypto.LoadECDSA("E:\\mydata\\agent\\nodekey")
	if err != nil {
		return err
	}

	cfg := discover.Config{
		PrivateKey:   privateKey,
		AnnounceAddr: realaddr,
		Bootnodes:    bootnodes,
	}

	// 启动 udp 监听/通讯例程
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
	dialer := newDialState(bootnodes, self.ntab, 15)
	go self.run(dialer)

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
