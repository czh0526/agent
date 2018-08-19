package agent

import (
	"github.com/czh0526/agent/p2p"
	"github.com/czh0526/agent/rpc"
)

type Agent struct {
	P2PServer p2p.Server
	RPCServer rpc.Server
}

func NewAgent() (*Agent, error) {
	p2pSrvr := p2p.NewServer()
	rpcSrvr := rpc.NewServer()

	agent := &Agent{
		P2PServer: p2pSrvr,
		RPCServer: rpcSrvr,
	}
	return agent, nil
}

func (self *Agent) Start() error {

	self.P2PServer.Start()
	self.RPCServer.Start()

	return nil
}

func (self *Agent) Stop() error {
	if err := self.P2PServer.Stop(); err != nil {
		return err
	}
	if err := self.RPCServer.Stop(); err != nil {
		return err
	}

	return nil
}
