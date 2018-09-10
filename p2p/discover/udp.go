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
	"github.com/czh0526/agent/log"

	"github.com/czh0526/agent/rlp"
)

const Version = 4

var (
	errPacketTooSmall   = errors.New("too small")
	errBadHash          = errors.New("bad hash")
	errExpired          = errors.New("expired")
	errUnsolicitedReply = errors.New("unsolicited reply")
	errUnknownNode      = errors.New("unknown node")
	errTimeout          = errors.New("RPC timeout")
	errClockWarp        = errors.New("reply deadline too far in the future")
	errClosed           = errors.New("socket closed")
)

const (
	respTimeout = 2500 * time.Millisecond
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

	maxNeighbors int
)

func init() {
	p := neighbors{Expiration: ^uint64(0)}
	maxSizeNode := rpcNode{IP: make(net.IP, 16), UDP: ^uint16(0), TCP: ^uint16(0)}
	for n := 0; ; n++ {
		p.Nodes = append(p.Nodes, maxSizeNode)
		size, _, err := rlp.EncodeToReader(p)
		if err != nil {
			// If this ever happens, it will be caught by the unit tests.
			panic("cannot encode: " + err.Error())
		}
		if headSize+size+1 >= 1280 {
			maxNeighbors = n
			break
		}
	}
}

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
	tab, err := newTable(udp, PubkeyID(&cfg.PrivateKey.PublicKey), realaddr, cfg.NodeDBPath, cfg.Bootnodes)
	if err != nil {
		return nil, nil, err
	}
	udp.Table = tab

	// 启动 udp 例程
	log.Info(" >> 启动 UDP 消息处理模块.")
	go udp.loop()
	log.Info(" << UDP 消息处理模块启动完成.")

	log.Info(" >> 启动 UDP 报文读取循环")
	go udp.readLoop()
	log.Info(" << UDP 报文读取循环启动完成.")
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
		resetTimeout()

		select {
		case <-t.closing:
			for el := plist.Front(); el != nil; el = el.Next() {
				el.Value.(*pending).errc <- errClosed
			}
			return
		case p := <-t.addpending:
			fmt.Printf("addpending ==> %v ==> plist \n", getTypeString(p.ptype))
			p.deadline = time.Now().Add(respTimeout)
			plist.PushBack(p)

		case r := <-t.gotreply:
			fmt.Printf("gotreply ==> %v \n", getTypeString(r.ptype))
			var matched bool
			for el := plist.Front(); el != nil; el = el.Next() {
				p := el.Value.(*pending)
				if p.from == r.from && p.ptype == r.ptype {
					matched = true
					if p.callback(r.data) {
						p.errc <- nil
						plist.Remove(el)
						fmt.Printf("plist =\\=> %v \n", getTypeString(p.ptype))
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
					log.Info("Remove an expired pending obj", "type", getTypeString(p.ptype), "deadline", fmt.Sprintf("%v", p.deadline))
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

func getTypeString(ptype byte) string {
	switch ptype {
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
			log.Error(fmt.Sprintf("udp -> readLoop(): read error: %v", err))
			return
		}

		if err := t.handlePacket(from, buf[:nbytes]); err != nil {
			log.Error(fmt.Sprintf("udp -> readLoop(): handle error: %v", err))
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
	log.Trace(fmt.Sprintf("%v %v <= 0x%x...", prefix, packet.name(), fromID[:8]))
}

func (t *udp) Stop() {
	t.closing <- struct{}{}
}

func (t *udp) pending(id NodeID, ptype byte, callback func(interface{}) bool) <-chan error {
	// 构造 pending 对象
	ch := make(chan error, 1)
	p := &pending{from: id, ptype: ptype, callback: callback, errc: ch}
	// 将 pending 对象纳入管理
	select {
	case t.addpending <- p:
		fmt.Printf("addpending <== %v \n", getTypeString(p.ptype))
	case <-t.closing:
		ch <- errClosed
	}
	// 返回 pending 对象的监控管道
	return ch
}

/*
	我们收到的 req 需要我们发出一个消息响应，
	我们通过 gotreply chan 做响应消息的管理
*/
func (t *udp) handleReply(from NodeID, ptype byte, req packet) bool {
	matched := make(chan bool, 1)
	// 将 reply 对象纳入管理
	select {
	case t.gotreply <- reply{from, ptype, req, matched}:
		fmt.Printf("gotreply <== %v  \n", getTypeString(ptype))
		// 等待 reply 消息在 loop 循环中被处理
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
	return <-t.pending(from, pingPacket, func(interface{}) bool { return true })
}

func (t *udp) findnode(toid NodeID, toaddr *net.UDPAddr, target NodeID) ([]*Node, error) {
	// 构造列表，接收用户数据
	nodes := make([]*Node, 0, bucketSize)
	nreceived := 0
	// 构造 pending 对象，注册处理函数
	errc := t.pending(toid, neighborsPacket, func(r interface{}) bool {
		reply := r.(*neighbors)
		for _, rn := range reply.Nodes {
			nreceived++
			n, err := t.nodeFromRPC(toaddr, rn)
			if err != nil {
				fmt.Printf("Invalid neighbor node received, ip = %v, addr = %v, err = %v", rn.IP, toaddr, err)
				continue
			}
			fmt.Printf("[udp]: findnode() --> callback func. node = %v:%v|%v \n\n", n.IP, n.UDP, n.TCP)
			nodes = append(nodes, n)
		}
		return nreceived >= bucketSize
	})
	t.send(toaddr, findnodePacket, &findnode{
		Target:     target,
		Expiration: uint64(time.Now().Add(expiration).Unix()),
	})
	err := <-errc
	return nodes, err
}

func (t *udp) close() {
	close(t.closing)
	t.conn.Close()
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
	log.Trace(fmt.Sprintf("[udp]: >> %v , addr = %v, err = %v", what, toaddr, err))
	return err
}

/*
	rpcNode ==> Node
*/
func (t *udp) nodeFromRPC(sender *net.UDPAddr, rn rpcNode) (*Node, error) {
	if rn.UDP <= 1024 {
		return nil, errors.New("low port")
	}

	n := NewNode(rn.ID, rn.IP, rn.UDP, rn.TCP)
	err := n.validateComplete()
	return n, err
}

/*
	把节点转化成 TCP 连接用的节点
	Node ==> rpcNode
*/
func nodeToRPC(n *Node) rpcNode {
	return rpcNode{ID: n.ID, IP: n.IP, UDP: n.UDP, TCP: n.TCP}
}

func (n *Node) Incomplete() bool {
	return n.IP == nil
}

func (n *Node) validateComplete() error {
	if n.Incomplete() {
		return errors.New("incomplete node")
	}
	if n.UDP == 0 {
		return errors.New("missing UDP port")
	}
	if n.TCP == 0 {
		return errors.New("missing TCP port")
	}
	_, err := n.ID.Pubkey()
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
	case findnodePacket:
		req = new(findnode)
	case neighborsPacket:
		req = new(neighbors)
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

	findnode struct {
		Target     NodeID
		Expiration uint64
		Rest       []rlp.RawValue `rlp:"tail"`
	}

	neighbors struct {
		Nodes      []rpcNode
		Expiration uint64
		Rest       []rlp.RawValue `rlp:"tail"`
	}

	rpcNode struct {
		IP  net.IP
		UDP uint16
		TCP uint16
		ID  NodeID
	}

	rpcEndpoint struct {
		IP  net.IP
		UDP uint16
		TCP uint16
	}
)

func (req *ping) handle(t *udp, from *net.UDPAddr, fromID NodeID, mac []byte) error {
	log.Trace(fmt.Sprintf("[udp] -> ping.handle(): ping <== %v \n", from))
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
	log.Trace(fmt.Sprintf("[udp] -> pong.handle(): pong <== %v \n", from))
	if expired(req.Expiration) {
		return errExpired
	}
	if !t.handleReply(fromID, pongPacket, req) {
		log.Info("pong.handle() handleReply error...")
		return errUnsolicitedReply
	}
	return nil
}
func (req *pong) name() string { return "PONG/v4" }

func (req *findnode) handle(t *udp, from *net.UDPAddr, fromID NodeID, mac []byte) error {
	if expired(req.Expiration) {
		return errExpired
	}
	if !t.db.hasBond(fromID) {
		return errUnknownNode
	}

	// 计算目标节点的矢量值, 并找出几个最近的节点
	target := crypto.Keccak256Hash(req.Target[:])
	t.mutex.Lock()
	closest := t.closest(target, bucketSize).entries
	t.mutex.Unlock()

	p := neighbors{Expiration: uint64(time.Now().Add(expiration).Unix())}
	var sent bool

	for _, n := range closest {
		p.Nodes = append(p.Nodes, nodeToRPC(n))
		if len(p.Nodes) == maxNeighbors {
			t.send(from, neighborsPacket, &p)
			p.Nodes = p.Nodes[:0]
			sent = true
		}
	}
	if len(p.Nodes) > 0 || !sent {
		// 发出去未放满的 packet, 或者是空的 packet
		t.send(from, neighborsPacket, &p)
	}
	return nil
}

func (req *findnode) name() string {
	return "FINDNODE/v4"
}

func (req *neighbors) handle(t *udp, from *net.UDPAddr, fromID NodeID, mac []byte) error {
	if expired(req.Expiration) {
		return errExpired
	}
	if !t.handleReply(fromID, neighborsPacket, req) {
		log.Info("neighbors.handle() handleReply error...")
		return errUnsolicitedReply
	}
	return nil
}

func (req *neighbors) name() string {
	return "NEIGHBORS/v4"
}
