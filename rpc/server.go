package rpc

import (
	"fmt"
	"time"
)

type RPCServer struct {
	running bool
	quit    chan struct{}
}

func NewServer() Server {
	return &RPCServer{
		running: false,
		quit:    make(chan struct{}),
	}
}

func (self *RPCServer) Start() {
	go self.loop()
}

func (self *RPCServer) loop() {
	self.running = true
	for {
		if !self.running {
			break
		}

		fmt.Println("RPCServer::Start().")

		select {
		case <-self.quit:
			self.running = false
		case <-time.After(time.Second * 2):
		}
	}
}

func (self *RPCServer) Stop() error {
	fmt.Println("RPCServer:Stop()")
	self.quit <- struct{}{}
	return nil
}
