package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/czh0526/agent/log"
)

var (
	ErrClientQuit = errors.New("client is closed")
	ErrNoResult   = errors.New("no result in JSON-RPC response")
)

const (
	tcpKeepAliveInterval = 30 * time.Second
	defaultDialTimeout   = 10 * time.Second
	defaultWriteTimeout  = 10 * time.Second
)

type jsonrpcMessage struct {
	Version string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id, omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Error   *jsonError      `json:"error,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

func (msg *jsonrpcMessage) isResponse() bool {
	return msg.hasValidID() && msg.Method == "" && len(msg.Params) == 0
}

func (msg *jsonrpcMessage) hasValidID() bool {
	return len(msg.ID) > 0 && msg.ID[0] != '{' && msg.ID[0] != '['
}

type requestOp struct {
	ids  []json.RawMessage
	err  error
	resp chan *jsonrpcMessage
}

func (op *requestOp) wait(ctx context.Context) (*jsonrpcMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-op.resp:
		return resp, op.err
	}
}

type Client struct {
	idCounter   uint32
	connectFunc func(ctx context.Context) (net.Conn, error)

	writeConn net.Conn

	close       chan struct{}
	didQuit     chan struct{}
	reconnected chan net.Conn
	readErr     chan error
	sendDone    chan error

	readResp  chan []*jsonrpcMessage
	requestOp chan *requestOp
	respWait  map[string]*requestOp
}

func (c *Client) Call(result interface{}, method string, args ...interface{}) error {
	ctx := context.Background()
	return c.CallContext(ctx, result, method, args...)
}

func (c *Client) CallContext(ctx context.Context, result interface{}, method string, args ...interface{}) error {
	// 构建 msg 对象
	msg, err := c.newMessage(method, args...)
	if err != nil {
		return err
	}
	// 构建 Op 对象
	op := &requestOp{ids: []json.RawMessage{msg.ID}, resp: make(chan *jsonrpcMessage, 1)}
	// 发送 Op 对象
	err = c.send(ctx, op, msg)
	if err != nil {
		return err
	}

	// 等待对方的 response 返回
	switch resp, err := op.wait(ctx); {
	case err != nil:
		return err
	case resp.Error != nil:
		return resp.Error
	case len(resp.Result) == 0:
		return ErrNoResult
	default:
		return json.Unmarshal(resp.Result, &result)
	}
}

/*
	返回包装了 conn 的 Client 对象
	read(), write() 使用 conn,
	重连使用 connectFunc
*/
func newClient(initctx context.Context, connectFunc func(context.Context) (net.Conn, error)) (*Client, error) {
	conn, err := connectFunc(initctx)
	if err != nil {
		return nil, err
	}

	c := &Client{
		connectFunc: connectFunc,
		writeConn:   conn,
		close:       make(chan struct{}),
		didQuit:     make(chan struct{}),
		readErr:     make(chan error),
		readResp:    make(chan []*jsonrpcMessage),
		requestOp:   make(chan *requestOp),
		respWait:    make(map[string]*requestOp),
		sendDone:    make(chan error, 1),
	}

	// 启动长连接处理例程
	go c.dispatch(conn)

	return c, nil
}

func (c *Client) dispatch(conn net.Conn) {
	// 启动读例程
	go c.read(conn)

	var (
		lastOp        *requestOp
		requestOpLock = c.requestOp
		reading       = true
	)

	defer close(c.didQuit)
	defer func() {
		c.closeRequestOps(ErrClientQuit)
		conn.Close()
		if reading {
			for {
				select {
				case <-c.readResp:
				case <-c.readErr:
					return
				}
			}
		}
	}()

	// 循环监控事件
	for {
		select {
		case <-c.close:
			return

		case batch := <-c.readResp:
			for _, msg := range batch {
				switch {
				case msg.isResponse():
					c.handleResponse(msg)
				default:
					log.Info("error: message is not a response.")
				}
			}

		case err := <-c.readErr:
			c.closeRequestOps(err)
			conn.Close()
			reading = false

		case newconn := <-c.reconnected:
			if reading {
				conn.Close()
				<-c.readErr
			}
			go c.read(newconn)
			reading = true
			conn = newconn

		case op := <-requestOpLock:
			// 锁住 send()
			requestOpLock = nil
			lastOp = op
			for _, id := range op.ids {
				c.respWait[string(id)] = op
			}

		case err := <-c.sendDone:
			// 发送 request 出错，清除 op 对象
			if err != nil {
				for _, id := range lastOp.ids {
					delete(c.respWait, string(id))
				}
			}
			// 释放 send()
			requestOpLock = c.requestOp
			lastOp = nil
		}
	}
}

// 循环读取 jsonrpcMessage, 并写入 readResp
func (c *Client) read(conn net.Conn) error {
	var (
		buf json.RawMessage
		dec = json.NewDecoder(conn)
	)
	readMessage := func() (rs []*jsonrpcMessage, err error) {
		buf = buf[:0]
		if err = dec.Decode(&buf); err != nil {
			return nil, err
		}
		if isBatch(buf) {
			err = json.Unmarshal(buf, &rs)
		} else {
			rs = make([]*jsonrpcMessage, 1)
			err = json.Unmarshal(buf, &rs[0])
		}
		return rs, err
	}

	for {
		resp, err := readMessage()
		if err != nil {
			c.readErr <- err
			return err
		}
		c.readResp <- resp
	}
}

func (c *Client) handleResponse(msg *jsonrpcMessage) {
	// 查找之前的 requestOp
	op := c.respWait[string(msg.ID)]
	if op == nil {
		log.Debug("unsolicited response", "msg", msg)
		return
	}
	delete(c.respWait, string(msg.ID))

	// 将 msg 写入 op.resp, 触发 op.wait()
	op.resp <- msg
	return
}

func (c *Client) send(ctx context.Context, op *requestOp, msg interface{}) error {
	select {
	case c.requestOp <- op:
		// 发送 msg
		err := c.write(ctx, msg)
		// 触发 sendDone 事件处理
		c.sendDone <- err
		return err

	case <-ctx.Done():
		return ctx.Err()

	case <-c.didQuit:
		return ErrClientQuit
	}
}

func (c *Client) write(ctx context.Context, msg interface{}) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(defaultWriteTimeout)
	}

	// 检查连接
	if c.writeConn == nil {
		if err := c.reconnect(ctx); err != nil {
			return err
		}
	}

	// 写数据
	c.writeConn.SetWriteDeadline(deadline)
	err := json.NewEncoder(c.writeConn).Encode(msg)
	if err != nil {
		c.writeConn = nil
	}
	return err
}

func (c *Client) closeRequestOps(err error) {
	didClose := make(map[*requestOp]bool)

	for id, op := range c.respWait {
		delete(c.respWait, id)

		if !didClose[op] {
			op.err = err
			close(op.resp)
			didClose[op] = true
		}
	}
}

func (c *Client) reconnect(ctx context.Context) error {
	newconn, err := c.connectFunc(ctx)
	if err != nil {
		return err
	}

	select {
	case c.reconnected <- newconn:
		c.writeConn = newconn
		return nil

	case <-c.didQuit:
		newconn.Close()
		return ErrClientQuit
	}
}

func (c *Client) newMessage(method string, paramsIn ...interface{}) (*jsonrpcMessage, error) {
	params, err := json.Marshal(paramsIn)
	if err != nil {
		return nil, err
	}
	return &jsonrpcMessage{Version: "2.0", ID: c.nextID(), Method: method, Params: params}, nil
}

func (c *Client) nextID() json.RawMessage {
	id := atomic.AddUint32(&c.idCounter, 1)
	return []byte(strconv.FormatUint(uint64(id), 10))
}

func (c *Client) Close() {
	select {
	case c.close <- struct{}{}: // 发送关闭信号
		<-c.didQuit // 等待 dispatch() 退出
	case <-c.didQuit:
	}
}

func Dial(rawurl string) (*Client, error) {
	return DialContext(context.Background(), rawurl)
}

func DialContext(ctx context.Context, rawurl string) (*Client, error) {
	u, err := url.Parse(rawurl)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "ws", "wss":
		return DialWebsocket(ctx, rawurl, "")
	case "":
		return DialIPC(ctx, rawurl)
	default:
		return nil, fmt.Errorf("no hnown transport for URL scheme %q", u.Scheme)
	}
}
