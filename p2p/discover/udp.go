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
	errClockWarp = errors.New("reply deadline too far in the future")
	errClosed    = errors.New("socket closed")
)

const (
	respTimeout = 500 * time.Millisecond
	expiration  = 20 * time.Second
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

type udp struct {
	conn        *net.UDPConn
	priv        *ecdsa.PrivateKey
	ourEndpoint rpcEndpoint

	addpending chan *pending

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
		plist       = list.New()
		timeout     = time.NewTimer(0)
		nextTimeout *pending
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
				timeout.Reset(dist)
				return
			}
			nextTimeout.errc <- errClockWarp
			plist.Remove(el)
		}
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
			p.deadline = time.Now().Add(respTimeout)
			plist.PushBack(p)
		}
	}

}

func (t *udp) readLoop() {
	defer t.conn.Close()

	// udp 包的最大尺寸
	buf := make([]byte, 1280)
	for {
		fmt.Println("[udp] -> readLoop(): read From UDP ...")
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
	fmt.Printf("[udp] -> handlePacket(): [%v] handle packet: 0x%x. \n",  from ,buf)
	return nil	
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

func (t *udp) write(toaddr *net.UDPAddr, what string, packet []byte) error {
	_, err := t.conn.WriteToUDP(packet, toaddr)
	fmt.Printf(">> %v , addr = %v, err = %v", what, toaddr, err)
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
	return nil
}
func (req *ping) name() string { return "PING/v4" }

func (req *pong) handle(t *udp, from *net.UDPAddr, fromID NodeID, mac []byte) error {
	fmt.Println("[udp] -> handle(): handle pong message...")
	return nil
}
func (req *pong) name() string { return "PONG/v4" }
