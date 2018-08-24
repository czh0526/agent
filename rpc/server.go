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

func (self *RPCServer) Start() error {
	go self.loop()
	return nil
}

func (self *RPCServer) loop() {
	self.running = true

	for {
		if !self.running {
			break
		}

		fmt.Println("[RPCServer] -> loop(): is running.")

		select {
		case <-self.quit:
			break
		case <-time.After(time.Second * 10):
		}
	}
	fmt.Println("RPCServer::loop() is exited.")
}

func (self *RPCServer) Stop() error {
	fmt.Println("RPCServer:Stop()")
	self.running = false
	self.quit <- struct{}{}
	return nil
}
