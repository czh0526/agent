package agent

import (
	"github.com/czh0526/agent/log"

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
	log.Info("--> 启动 P2P Server.")
	if err := self.P2PServer.Start(); err != nil {
		log.Error("[P2PServer] -> Start() error: %v", err)
		return err
	}
	log.Info("<-- P2P Server 启动成功.")

	/*
		if err := self.RPCServer.Start(); err != nil {
			fmt.Println("[RPCServer] -> Start() error: %v", err)
			return err
		}
	*/

	return nil
}

func (self *Agent) Stop() error {
	go func() {
		log.Info("--> 关闭 P2P Server.")
		if err := self.P2PServer.Stop(); err != nil {
			log.Error("P2P Server 关闭失败.")
			return
		}
		log.Info("<-- P2P Server 关闭成功.")
	}()
	//go self.RPCServer.Stop()
	// 通知外部程序，agent终止
	close(self.stop)
	return nil
}

func (self *Agent) Wait() {
	stop := self.stop
	<-stop
}
