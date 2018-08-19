package p2p

import (
	"fmt"
	"time"
)

type P2PServer struct {
	running bool
	quit    chan struct{}
}

func NewServer() Server {
	return &P2PServer{
		running: false,
		quit:    make(chan struct{}),
	}
}

func (self *P2PServer) Start() {
	go self.loop()
}

func (self *P2PServer) loop() {
	self.running = true
	for {
		// 退出条件
		if !self.running {
			break
		}

		fmt.Println("P2PServer::Start().")

		// 监听外部退出事件
		select {
		case <-self.quit:
			self.running = false
		case <-time.After(time.Second * 2):
		}
	}
	fmt.Println("P2PServer exited.")
}

func (self *P2PServer) Stop() error {
	fmt.Println("P2PServer::Stop().")
	self.quit <- struct{}{}
	return nil
}
