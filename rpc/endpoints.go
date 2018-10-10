package rpc

import (
	"net"
	"path/filepath"
	"runtime"

	"github.com/czh0526/agent/log"
	"github.com/czh0526/agent/params"
)

func EndpointWS() string {
	return "127.0.0.1:8545"
}

func EndpointIPC() string {
	if runtime.GOOS == "windows" {
		return `\\.\pipe\agent.ipc`
	} else {
		return filepath.Join(params.HomeDir, "/pipe/agent.ipc")
	}
}

// 构建 IPC 监听端口，绑定 IPC 协议处理器
func StartIPCEndpoint(endpoint string, apis []API) (net.Listener, *Handler, error) {
	handler := NewHandler()
	for _, api := range apis {
		if err := handler.RegisterName(api.Namespace, api.Service); err != nil {
			return nil, nil, err
		}
		log.Debug("IPC registered, namespace = %v", api.Namespace)
	}

	listener, err := ipcListen(endpoint)
	if err != nil {
		return nil, nil, err
	}

	go handler.ServeListener(listener)
	return listener, handler, nil
}

// 创建 wss 监听端口， 绑定 websocket 协议处理器
func StartWSEndpoint(endpoint string, apis []API, wsOrigins []string) (net.Listener, *Handler, error) {

	// 启动 RPC 服务容器
	handler := NewHandler()
	// 注册服务
	for _, api := range apis {
		if err := handler.RegisterName(api.Namespace, api.Service); err != nil {
			log.Error("WebSocket registered, service = %v, namespace = %v", api.Service, api.Namespace)
		}
	}

	var (
		listener net.Listener
		err      error
	)
	// 构造 Listener
	if listener, err = net.Listen("tcp", endpoint); err != nil {
		return nil, nil, err
	}

	// 启动 websocket 协议服务器
	go NewWSServer(wsOrigins, handler).Serve(listener)
	return listener, handler, err
}
