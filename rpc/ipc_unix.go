// +build darwin freebsd linux netbsd openbsd solaris

package rpc

import (
	"context"
	"net"
	"os"
	"path/filepath"
)

// 服务端使用，建立 linux 平台上的 IPC 连接
func ipcListen(endpoint string) (net.Listener, error) {
	// 构建目录
	if err := os.MkdirAll(filepath.Dir(endpoint), 0751); err != nil {
		return nil, err
	}
	// 删除旧文件
	os.Remove(endpoint)

	// 创建 Listener
	l, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, err
	}
	os.Chmod(endpoint, 0600)
	return l, nil
}

// 客户端使用，建立 linux 平台上的 IPC 连接
func newIPCConnection(ctx context.Context, endpoint string) (net.Conn, error) {
	return dialContext(ctx, "unix", endpoint)
}
