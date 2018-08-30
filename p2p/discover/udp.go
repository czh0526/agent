package discover

import (
	"bytes"
	"container/list"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/czh0526/agent/crypto"

	"github.com/czh0526/agent/rlp"
)

const Version = 4

var (
	errPacketTooSmall = errors.New("too small")
	errBadHash        = errors.New("bad hash")
	errExpired        = errors.New("expired")
	errTimeout        = errors.New("RPC timeout")
	errClockWarp      = errors.New("reply deadline too far in the future")
	errClosed         = errors.New("socket closed")
)

const (
	respTimeout = 500 * time.Millisecond
	expiration  = 20 * time.Second

	ntpFailureThreshold = 32
	ntpWarningCooldown  = 10 * time.Minute
	driftThreshold      = 10 * time.Second
)

type Config struct {
	PrivateKey   *ecdsa.PrivateKey
	AnnounceAddr *net.UDPAddr
	NodeDBPath   string
	Bootnodes    []*Node
}

const (
	macSize  = 256 / 8           // 32 byte
	sigSize  = 520 / 8           // 65 byte
	headSize = macSize + sigSize // 97 byte
)

var (
	headSpace = make([]byte, headSize)
)

type pending struct {
	from     NodeID
	ptype    byte
	deadline time.Time
	callback func(resp interface{}) (done bool)
	errc     chan<- error
}

type reply struct {
	from    NodeID
	ptype   byte
	data    interface{}
	matched chan<- bool
}

type udp struct {
	conn        *net.UDPConn
	priv        *ecdsa.PrivateKey
	ourEndpoint rpcEndpoint

	addpending chan *pending
	gotreply   chan reply

	closing chan struct{}

	*Table
}

func ListenUDP(c *net.UDPConn, cfg Config) (*Table, error) {
	tab, _, err := newUDP(c, cfg)
	if err != nil {
		return nil, err
	}
	return tab, nil
}

func newUDP(c *net.UDPConn, cfg Config) (*Table, *udp, error) {
	// 构建 udp 对象
	udp := &udp{
		conn:       c,
		priv:       cfg.PrivateKey,
		closing:    make(chan struct{}),
		gotreply:   make(chan reply),
		addpending: make(chan *pending),
	}

	realaddr := c.LocalAddr().(*net.UDPAddr)
	if cfg.AnnounceAddr != nil {
		realaddr = cfg.AnnounceAddr
	}

	udp.ourEndpoint = makeEndpoint(realaddr, uint16(realaddr.Port))
	tab, err := newTable(udp, PubkeyID(&cfg.PrivateKey.PublicKey), realaddr, cfg.Bootnodes)
	if err != nil {
		return nil, nil, err
	}
	udp.Table = tab

	// 启动 udp 例程
	go udp.loop()
	go udp.readLoop()
	return tab, udp, nil
}

func (t *udp) loop() {
	var (
		plist        = list.New()
		timeout      = time.NewTimer(0)
		nextTimeout  *pending
		contTimeouts = 0
		ntpWarnTime  = time.Now()
	)
	<-timeout.C
	defer timeout.Stop()

	resetTimeout := func() {
		if plist.Front() == nil || nextTimeout == plist.Front().Value {
			return
		}

		now := time.Now()
		for el := plist.Front(); el != nil; el = el.Next() {
			nextTimeout = el.Value.(*pending)
			if dist := nextTimeout.deadline.Sub(now); dist < 2*respTimeout {
				// 设置下一次的超时时间间隔
				timeout.Reset(dist)
				return
			}
			// 对于超时时间异常的，返回错误并删除
			nextTimeout.errc <- errClockWarp
			plist.Remove(el)
		}
		// plist 列表为空，停止定时器
		nextTimeout = nil
		timeout.Stop()
	}

	for {
		fmt.Println("[udp] -> loop(): ")
		resetTimeout()

		select {
		case <-t.closing:
			for el := plist.Front(); el != nil; el = el.Next() {
				el.Value.(*pending).errc <- errClosed
			}
			return
		case p := <-t.addpending:
			fmt.Printf("插入一个 pending %v 对象 \n", getTypeString(p))
			p.deadline = time.Now().Add(respTimeout)
			plist.PushBack(p)

		case r := <-t.gotreply:
			var matched bool
			for el := plist.Front(); el != nil; el = el.Next() {
				p := el.Value.(*pending)
				if p.from == r.from && p.ptype == r.ptype {
					matched = true
					if p.callback(r.data) {
						p.errc <- nil
						plist.Remove(el)
					}
					contTimeouts = 0
				}
			}
			r.matched <- matched

		case now := <-timeout.C:
			nextTimeout = nil
			// 删除过期的 pending 对象
			for el := plist.Front(); el != nil; el = el.Next() {
				p := el.Value.(*pending)
				if now.After(p.deadline) || now.Equal(p.deadline) {
					p.errc <- errTimeout
					plist.Remove(el)
					contTimeouts++
				}
			}

			if contTimeouts > ntpFailureThreshold {
				if time.Since(ntpWarnTime) >= ntpWarningCooldown {
					ntpWarnTime = time.Now()
					//go checkClockDrift()
				}
				contTimeouts = 0
			}
		}
	}

}

func getTypeString(p *pending) string {
	switch p.ptype {
	case pingPacket:
		return "ping"
	case pongPacket:
		return "pong"
	case findnodePacket:
		return "findnode"
	case neighborsPacket:
		return "neighbors"
	default:
		return "unknown"
	}
}

func (t *udp) readLoop() {
	defer t.conn.Close()

	// udp 包的最大尺寸
	buf := make([]byte, 1280)
	for {
		nbytes, from, err := t.conn.ReadFromUDP(buf)
		if err != nil {
			fmt.Printf("udp -> readLoop: read error: %v \n", err)
			return
		}

		if err := t.handlePacket(from, buf[:nbytes]); err != nil {

		}
	}
}

func (t *udp) handlePacket(from *net.UDPAddr, buf []byte) error {
	packet, fromID, hash, err := decodePacket(buf)
	if err != nil {
		return err
	}

	printPacket("[udp]: <<", fromID, packet)

	err = packet.handle(t, from, fromID, hash)
	return err
}

func printPacket(prefix string, fromID NodeID, packet packet) {
	fmt.Printf("%v %v <= 0x%x... \n", prefix, packet.name(), fromID[:8])
}

func (t *udp) Stop() {
	t.closing <- struct{}{}
}

func (t *udp) close() {
	close(t.closing)
	t.conn.Close()
}

func (t *udp) pending(id NodeID, ptype byte, callback func(interface{}) bool) <-chan error {
	// 构造 pending 对象
	ch := make(chan error, 1)
	p := &pending{from: id, ptype: ptype, callback: callback, errc: ch}
	// 将 pending 对象纳入管理
	select {
	case t.addpending <- p:
	case <-t.closing:
		ch <- errClosed
	}
	// 返回 pending 对象的监控管道
	return ch
}

func (t *udp) handleReply(from NodeID, ptype byte, req packet) bool {
	matched := make(chan bool, 1)
	// 将 reply 对象纳入管理
	select {
	case t.gotreply <- reply{from, ptype, req, matched}:
		return <-matched
	case <-t.closing:
		return false
	}
}

func (t *udp) ping(toid NodeID, toaddr *net.UDPAddr) error {
	// 构建 ping 消息
	req := &ping{
		Version:    Version,
		From:       t.ourEndpoint,
		To:         makeEndpoint(toaddr, 0),
		Expiration: uint64(time.Now().Add(expiration).Unix()),
	}

	packet, hash, err := encodePacket(t.priv, pingPacket, req)
	if err != nil {
		return err
	}
	// 期待有 pong 消息响应
	errc := t.pending(toid, pongPacket, func(p interface{}) bool {
		return bytes.Equal(p.(*pong).ReplyTok, hash)
	})
	t.write(toaddr, req.name(), packet)
	return <-errc
}

func (t *udp) waitping(from NodeID) error {
	fmt.Println("[udp]: waitping")
	<-time.After(time.Second * 3)
	return nil
}

func (t *udp) findnode(toid NodeID, toaddr *net.UDPAddr, target NodeID) ([]*Node, error) {
	fmt.Println("[udp]: findnode")
	nodes := make([]*Node, 0, bucketSize)
	<-time.After(time.Second * 3)
	return nodes, nil
}

func makeEndpoint(addr *net.UDPAddr, tcpPort uint16) rpcEndpoint {
	ip := addr.IP.To4()
	if ip == nil {
		ip = addr.IP.To16()
	}
	return rpcEndpoint{IP: ip, UDP: uint16(addr.Port), TCP: tcpPort}
}

// 编码 + 发送 作为一个整体逻辑，不期待数据响应。
func (t *udp) send(toaddr *net.UDPAddr, ptype byte, req packet) ([]byte, error) {
	packet, hash, err := encodePacket(t.priv, ptype, req)
	if err != nil {
		return hash, err
	}
	return hash, t.write(toaddr, req.name(), packet)
}

// 单纯的发送数据包逻辑
func (t *udp) write(toaddr *net.UDPAddr, what string, packet []byte) error {
	_, err := t.conn.WriteToUDP(packet, toaddr)
	fmt.Printf("[udp]: >> %v , addr = %v, err = %v \n", what, toaddr, err)
	return err
}

func encodePacket(priv *ecdsa.PrivateKey, ptype byte, req interface{}) (packet, hash []byte, err error) {
	// 构建字节缓存
	b := new(bytes.Buffer)
	b.Write(headSpace)
	b.WriteByte(ptype)
	if err := rlp.Encode(b, req); err != nil {
		return nil, nil, err
	}
	packet = b.Bytes()

	// hash => 签名 => 填充
	sig, err := crypto.Sign(crypto.Keccak256(packet[headSize:]), priv)
	if err != nil {
		return nil, nil, err
	}
	copy(packet[macSize:], sig)

	// hash => 填充
	hash = crypto.Keccak256(packet[macSize:])
	copy(packet, hash)
	return packet, hash, nil
}

func decodePacket(buf []byte) (packet, NodeID, []byte, error) {
	if len(buf) < headSize+1 {
		return nil, NodeID{}, nil, errPacketTooSmall
	}
	// 外层 hash , 签名， 数据
	hash, sig, sigdata := buf[:macSize], buf[macSize:headSize], buf[headSize:]
	// 校验外层 hash
	shouldhash := crypto.Keccak256(buf[macSize:])
	if !bytes.Equal(hash, shouldhash) {
		return nil, NodeID{}, nil, errBadHash
	}
	// 签名 + 数据 => 公钥
	fromID, err := recoverNodeID(crypto.Keccak256(buf[headSize:]), sig)
	if err != nil {
		return nil, NodeID{}, hash, err
	}
	var req packet
	switch ptype := sigdata[0]; ptype {
	case pingPacket:
		req = new(ping)
	case pongPacket:
		req = new(pong)
	default:
		return nil, fromID, hash, fmt.Errorf("unknown type: %d", ptype)
	}

	s := rlp.NewStream(bytes.NewReader(sigdata[1:]), 0)
	err = s.Decode(req)
	return req, fromID, hash, err
}

func recoverNodeID(hash, sig []byte) (id NodeID, err error) {
	pubkey, err := crypto.Ecrecover(hash, sig)
	if err != nil {
		return id, err
	}
	if len(pubkey)-1 != len(id) {
		return id, fmt.Errorf("recovered pubkey has %d bits, want %d bits", len(pubkey)*8, (len(id)+1)*8)
	}
	for i := range id {
		id[i] = pubkey[i+1]
	}
	return id, nil
}

func expired(ts uint64) bool {
	return time.Unix(int64(ts), 0).Before(time.Now())
}

const (
	pingPacket = iota + 1
	pongPacket
	findnodePacket
	neighborsPacket
)

type packet interface {
	handle(t *udp, from *net.UDPAddr, fromID NodeID, mac []byte) error
	name() string
}

type (
	ping struct {
		Version    uint
		From, To   rpcEndpoint
		Expiration uint64
		Rest       []rlp.RawValue `rlp:"tail"`
	}

	pong struct {
		To         rpcEndpoint
		ReplyTok   []byte
		Expiration uint64
		Rest       []rlp.RawValue `rlp:"tail"`
	}

	rpcEndpoint struct {
		IP  net.IP
		UDP uint16
		TCP uint16
	}
)

func (req *ping) handle(t *udp, from *net.UDPAddr, fromID NodeID, mac []byte) error {
	fmt.Println("[udp] -> handle(): handle ping message...")
	if expired(req.Expiration) {
		return errExpired
	}
	t.send(from, pongPacket, &pong{
		To:         makeEndpoint(from, req.From.TCP),
		ReplyTok:   mac,
		Expiration: uint64(time.Now().Add(expiration).Unix()),
	})
	if !t.handleReply(fromID, pingPacket, req) {
		go t.bond(true, fromID, from, req.From.TCP)
	}

	return nil
}
func (req *ping) name() string { return "PING/v4" }

func (req *pong) handle(t *udp, from *net.UDPAddr, fromID NodeID, mac []byte) error {
	fmt.Println("[udp] -> handle(): handle pong message...")
	return nil
}
func (req *pong) name() string { return "PONG/v4" }
