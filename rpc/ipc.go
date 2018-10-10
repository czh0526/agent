package rpc

import (
	"context"
	"net"
	"time"
)

const defaultPipeDialTimeout = 2 * time.Second

// 建立一个具有重连功能的客户端
func DialIPC(ctx context.Context, endpoint string) (*Client, error) {
	return newClient(ctx, func(ctx context.Context) (net.Conn, error) {
		return newIPCConnection(ctx, endpoint)
	})
}
