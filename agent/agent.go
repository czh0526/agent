package agent

import (
	"fmt"

	"github.com/czh0526/agent/p2p"
	"github.com/czh0526/agent/rpc"
)

type Agent struct {
	stop      chan struct{}
	P2PServer p2p.Server
	RPCServer rpc.Server
}

func NewAgent() (*Agent, error) {
	p2pSrvr := p2p.NewServer()
	rpcSrvr := rpc.NewServer()

	agent := &Agent{
		stop:      make(chan struct{}),
		P2PServer: p2pSrvr,
		RPCServer: rpcSrvr,
	}
	return agent, nil
}

func (self *Agent) Start() error {
	if err := self.P2PServer.Start(); err != nil {
		fmt.Println("[P2PServer] -> Start() error: %v", err)
		return err
	}

	if err := self.RPCServer.Start(); err != nil {
		fmt.Println("[RPCServer] -> Start() error: %v", err)
		return err
	}

	return nil
}

func (self *Agent) Stop() error {
	go self.P2PServer.Stop()
	go self.RPCServer.Stop()
	// 通知外部程序，agent终止
	close(self.stop)
	return nil
}

func (self *Agent) Wait() {
	stop := self.stop
	<-stop
}
