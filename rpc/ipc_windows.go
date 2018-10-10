// +build windows

package rpc

import (
	"context"
	"net"
	"time"

	npipe "gopkg.in/natefinch/npipe.v2"
)

// 服务端使用，建立 windows 平台上的 IPC 监听端口
func ipcListen(endpoint string) (net.Listener, error) {
	return npipe.Listen(endpoint)
}

// 客户端使用，建立 windows 平台上的 IPC 连接
func newIPCConnection(ctx context.Context, endpoint string) (net.Conn, error) {
	timeout := defaultPipeDialTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = deadline.Sub(time.Now())
		if timeout < 0 {
			timeout = 0
		}
	}
	return npipe.DialTimeout(endpoint, timeout)
}
