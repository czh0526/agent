package rpc

import (
	"context"
	"net"
)

func startInProc(server *RPCServer) (*Handler, error) {
	return NewHandler(), nil
}

func DialInProc(handler *Handler) *Client {
	initctx := context.Background()
	// 构建 Client, 并启动 dispatch()
	c, _ := newClient(initctx, func(context.Context) (net.Conn, error) {
		// 重连函数，负责重新建立连接，并指挥服务器服务于该连接
		p1, p2 := net.Pipe()
		go handler.ServeCodec(NewJSONCodec(p1), OptionMethodInvocation)
		return p2, nil
	})
	return c
}
