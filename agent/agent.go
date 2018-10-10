package agent

import (
	"fmt"
	"reflect"

	"github.com/czh0526/agent/log"

	"github.com/czh0526/agent/p2p"
	"github.com/czh0526/agent/rpc"
)

// Agent 是一个 Service 的容器，用于构建各种 Service
type Agent struct {
	stop      chan struct{}
	P2PServer *p2p.P2PServer
	RPCServer *rpc.RPCServer

	serviceFuncs []ServiceConstructor
	services     map[reflect.Type]Service
}

func NewAgent() (*Agent, error) {
	p2pSrvr := p2p.NewServer()
	rpcSrvr := rpc.NewServer()

	agent := &Agent{
		stop:         make(chan struct{}),
		P2PServer:    p2pSrvr,
		RPCServer:    rpcSrvr,
		serviceFuncs: []ServiceConstructor{},
	}
	return agent, nil
}

func (self *Agent) Start() error {

	// 向 P2PServer 中注入 Service
	services := make(map[reflect.Type]Service)
	for _, constructor := range self.serviceFuncs {
		service, err := constructor()
		if err != nil {
			return err
		}
		kind := reflect.TypeOf(service)
		if _, exists := services[kind]; exists {
			return &DuplicateServiceError{Kind: kind}
		}
		services[kind] = service
	}
	self.services = services

	for _, service := range services {
		self.P2PServer.Protocols = append(self.P2PServer.Protocols, service.Protocols()...)
	}

	log.Info("--> 启动 P2P Server.")
	if err := self.P2PServer.Start(); err != nil {
		log.Error("[P2PServer] -> Start() error: %v", err)
		return err
	}
	log.Info("<-- P2P Server 启动成功.")

	log.Info("--> 启动 Service.")
	started := []reflect.Type{}
	for kind, service := range services {
		if err := service.Start(self.P2PServer); err != nil {
			for _, kind := range started {
				services[kind].Stop()
			}
			self.P2PServer.Stop()

			return err
		}
		started = append(started, kind)
	}
	log.Info("<-- Service 启动成功.")

	log.Info("--> 启动 RPC Server.")
	if err := self.startRPC(); err != nil {
		fmt.Println("[RPCServer] -> Start() error: %v", err)
		return err
	}
	log.Info("<-- RPC Server 启动成功.")

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

	go func() {
		log.Info("--> 关闭 RPC Server.")
		self.RPCServer.Stop()
		log.Info("<-- RPC Server 关闭成功.")
	}()

	// 通知外部程序，agent终止
	close(self.stop)
	return nil
}

func (self *Agent) Wait() {
	stop := self.stop
	<-stop
}

func (self *Agent) Register(constructor ServiceConstructor) error {
	if self.P2PServer.IsRunning() {
		return ErrAgentRunning
	}

	self.serviceFuncs = append(self.serviceFuncs, constructor)
	return nil
}

func (self *Agent) startRPC() error {
	apis := []rpc.API{}
	for _, service := range self.services {
		apis = append(apis, service.APIs()...)
	}
	return self.RPCServer.Start(apis)
}
