package rpc

import (
	"fmt"
	"net"

	"github.com/czh0526/agent/log"
)

type RPCServer struct {
	inprocHandler *Handler

	wsEndpoint string
	wsListener net.Listener
	wsHandler  *Handler

	ipcEndpoint string
	ipcListener net.Listener
	ipcHandler  *Handler
}

func NewServer() *RPCServer {
	return &RPCServer{}
}

func (self *RPCServer) Start(apis []API) error {
	if err := self.startInProc(apis); err != nil {
		return err
	}
	log.Info("启动 RPC InProc ")

	var ipcEndpoint = EndpointIPC()
	if err := self.startIPC(ipcEndpoint, apis); err != nil {
		self.stopInProc()
		return err
	}
	log.Info(fmt.Sprintf("启动 RPC IPC, endpoint = %v", self.ipcListener.Addr()))

	var wsEndpoint = EndpointWS()
	if err := self.startWS(wsEndpoint, apis, []string{"*"}); err != nil {
		self.stopInProc()
		self.stopIPC()
		return err
	}
	log.Info(fmt.Sprintf("启动 RPC Websocket, endpoint = %v", self.wsListener.Addr()))

	return nil
}

func (self *RPCServer) Stop() {
	self.stopInProc()
	self.stopWS()
}

func (self *RPCServer) startInProc(apis []API) error {
	handler := NewHandler()
	for _, api := range apis {
		if err := handler.RegisterName(api.Namespace, api.Service); err != nil {
			return err
		}
		log.Debug("InProc registered, service = %v, namespace = %v", api.Service, api.Namespace)
	}
	self.inprocHandler = handler

	return nil
}

func (self *RPCServer) stopInProc() {
	if self.inprocHandler != nil {
		self.inprocHandler.Stop()
		self.inprocHandler = nil
	}
}

func (self *RPCServer) InprocHandler() *Handler {
	return self.inprocHandler
}

func (self *RPCServer) startWS(endpoint string, apis []API, wsOrigins []string) error {
	if endpoint == "" {
		return nil
	}

	listener, handler, err := StartWSEndpoint(endpoint, apis, wsOrigins)
	if err != nil {
		return err
	}
	log.Debug(fmt.Sprintf("Websocket endpoint opened, url = ws://%s", listener.Addr()))

	self.wsEndpoint = endpoint
	self.wsListener = listener
	self.wsHandler = handler

	return nil
}

func (self *RPCServer) stopWS() {
	if self.wsListener != nil {
		self.wsListener.Close()
		self.wsListener = nil
	}
	if self.wsHandler != nil {
		self.wsHandler.Stop()
		self.wsHandler = nil
	}
}

func (self *RPCServer) startIPC(endpoint string, apis []API) error {

	listener, handler, err := StartIPCEndpoint(endpoint, apis)
	if err != nil {
		return err
	}

	self.ipcEndpoint = endpoint
	self.ipcListener = listener
	self.ipcHandler = handler
	return nil
}

func (self *RPCServer) stopIPC() {
	if self.ipcListener != nil {
		self.ipcListener.Close()
		self.ipcListener = nil
	}
	if self.ipcHandler != nil {
		self.ipcHandler.Stop()
		self.ipcHandler = nil
	}
}
