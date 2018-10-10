package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

func newTestHandler(serviceName string, service interface{}) *Handler {
	handler := NewHandler()
	if err := handler.RegisterName(serviceName, service); err != nil {
		panic(err)
	}
	return handler
}

func TestClientRequest(t *testing.T) {
	// 构造 RPCServer, 并注册服务
	handler := newTestHandler("service", new(Svc))
	defer handler.Stop()
	// 构建 Client
	client := DialInProc(handler)
	defer client.Close()

	var resp Result
	if err := client.Call(&resp, "service_echo", "hello", 10, &Args{"world"}); err != nil {
		t.Fatal(err)
	}

	respBytes, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(respBytes))
}

func TestClientReconnect(t *testing.T) {
	// 启动一个 RPCServer
	startServer := func(addr string) (*Handler, net.Listener) {
		// 构建 RPCServer，并注册服务
		handler := newTestHandler("service", new(Svc))
		// 监听服务端口
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		// 将服务端口与 ws/wss 协议处理器绑定
		go http.Serve(l, handler.WebsocketHandler([]string{"*"}))
		return handler, l
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s1, l1 := startServer("127.0.0.1:0")
	client, err := DialContext(ctx, "ws://"+l1.Addr().String())
	if err != nil {
		t.Fatal("dial error: ", err)
	}

	var resp Result
	if err := client.CallContext(ctx, &resp, "service_echo", "", 1, nil); err != nil {
		t.Fatal("call error: ", err)
	}

	l1.Close()
	s1.Stop()
	if err := client.CallContext(ctx, &resp, "service_echo", "", 2, nil); err == nil {
		t.Error("successful call while the server is down")
		t.Logf("resp: %#v", resp)
	}

	time.Sleep(2 * time.Second)

	s2, l2 := startServer(l1.Addr().String())
	defer l2.Close()
	defer s2.Stop()
}
