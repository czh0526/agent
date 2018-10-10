package rpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/websocket"
	set "gopkg.in/fatih/set.v0"
)

// 构建 websocket 协议处理器
func NewWSServer(allowedOrigins []string, handler *Handler) *http.Server {
	return &http.Server{Handler: handler.WebsocketHandler(allowedOrigins)}
}

var websocketJSONCodec = websocket.Codec{
	Marshal: func(v interface{}) ([]byte, byte, error) {
		msg, err := json.Marshal(v)
		return msg, websocket.TextFrame, err
	},
	Unmarshal: func(msg []byte, payloadType byte, v interface{}) error {
		dec := json.NewDecoder(bytes.NewReader(msg))
		dec.UseNumber()
		return dec.Decode(v)
	},
}

/*
	构建一个支持 websocket 协议的 http.Handler
*/
func (c *Handler) WebsocketHandler(allowedOrigins []string) http.Handler {
	return websocket.Server{
		Handshake: wsHandshakeValidator(allowedOrigins),
		Handler: func(conn *websocket.Conn) {
			conn.MaxPayloadBytes = maxRequestContentLength

			encoder := func(v interface{}) error {
				return websocketJSONCodec.Send(conn, v)
			}
			decoder := func(v interface{}) error {
				return websocketJSONCodec.Receive(conn, v)
			}

			c.ServeCodec(NewCodec(conn, encoder, decoder), OptionMethodInvocation)
		},
	}
}

// 构建一个具有重连功能的客户端
func DialWebsocket(ctx context.Context, endpoint, origin string) (*Client, error) {
	if origin == "" {
		var err error
		if origin, err = os.Hostname(); err != nil {
			return nil, err
		}
		if strings.HasPrefix(endpoint, "wss") {
			origin = "https://" + strings.ToLower(origin)
		} else {
			origin = "http://" + strings.ToLower(origin)
		}
	}
	config, err := websocket.NewConfig(endpoint, origin)
	if err != nil {
		return nil, err
	}

	return newClient(ctx, func(ctx context.Context) (net.Conn, error) {
		return wsDialContext(ctx, config)
	})
}

// 客户端使用，建立一个 websocket.Conn 连接
func wsDialContext(ctx context.Context, config *websocket.Config) (*websocket.Conn, error) {
	// 构建 tcp 连接
	var conn net.Conn
	var err error
	switch config.Location.Scheme {
	case "ws":
		conn, err = dialContext(ctx, "tcp", wsDialAddress(config.Location))
	case "wss":
		dialer := contextDialer(ctx)
		conn, err = tls.DialWithDialer(dialer, "tcp", wsDialAddress(config.Location), config.TlsConfig)
	default:
		err = websocket.ErrBadScheme
	}
	if err != nil {
		return nil, err
	}

	// 包装成 websocket 连接
	ws, err := websocket.NewClient(config, conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ws, err
}

func dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{KeepAlive: tcpKeepAliveInterval}
	return d.DialContext(ctx, network, addr)
}

func contextDialer(ctx context.Context) *net.Dialer {
	dialer := &net.Dialer{Cancel: ctx.Done(), KeepAlive: tcpKeepAliveInterval}
	if deadline, ok := ctx.Deadline(); ok {
		dialer.Deadline = deadline
	} else {
		dialer.Deadline = time.Now().Add(defaultDialTimeout)
	}
	return dialer
}

func wsHandshakeValidator(allowedOrigins []string) func(*websocket.Config, *http.Request) error {
	// 构建 origins 并填充数据
	origins := set.New()
	allowAllOrigins := false
	for _, origin := range allowedOrigins {
		if origin == "*" {
			allowAllOrigins = true
		}
		if origin != "" {
			origins.Add(strings.ToLower(origin))
		}
	}

	if len(origins.List()) == 0 {
		origins.Add("http://localhost")
		if hostname, err := os.Hostname(); err == nil {
			origins.Add("http://" + strings.ToLower(hostname))
		}
	}

	// 函数逻辑：比较 header 中的 Origin 属性
	f := func(cfg *websocket.Config, req *http.Request) error {
		origin := strings.ToLower(req.Header.Get("Origin"))
		if allowAllOrigins || origins.Has(origin) {
			return nil
		}
		return fmt.Errorf("orgin %s not allowed", origin)
	}

	return f
}

var wsPortMap = map[string]string{"ws": "80", "wss": "443"}

func wsDialAddress(location *url.URL) string {
	if _, ok := wsPortMap[location.Scheme]; ok {
		if _, _, err := net.SplitHostPort(location.Host); err != nil {
			return net.JoinHostPort(location.Host, wsPortMap[location.Scheme])
		}
	}
	return location.Host
}
