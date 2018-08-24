package discover

import (
	"crypto/ecdsa"
	"fmt"
	"net"
	"time"
)

type Config struct {
	PrivateKey   *ecdsa.PrivateKey
	AnnounceAddr *net.UDPAddr
	NodeDBPath   string
	Bootnodes    []*Node
}

type udp struct {
	conn    *net.UDPConn
	priv    *ecdsa.PrivateKey
	closing chan struct{}
}

func ListenUDP(c *net.UDPConn, cfg Config) (*Table, error) {
	realaddr := c.LocalAddr().(*net.UDPAddr)
	if cfg.AnnounceAddr != nil {
		realaddr = cfg.AnnounceAddr
	}
	tab := &Table{
		self: NewNode(PubkeyID(&cfg.PrivateKey.PublicKey), realaddr.IP, uint16(realaddr.Port), uint16(realaddr.Port)),
	}
	return tab, nil
}

func newUDP(c *net.UDPConn, cfg Config) (*udp, error) {
	// 构建 udp 对象
	udp := &udp{
		conn:    c,
		priv:    cfg.PrivateKey,
		closing: make(chan struct{}),
	}

	// 启动 udp 例程
	go udp.loop()
	go udp.readLoop()
	return udp, nil
}

func (t *udp) loop() {

running:
	for {
		fmt.Println("[udp] -> loop(): loop")

		select {
		case <-t.closing:
			break running
		case <-time.After(time.Second * 2):
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
	fmt.Printf("udp -> handlePacket: handle packet: 0x%x. \n", buf)
	return nil
}

func (t *udp) Stop() {
	t.closing <- struct{}{}
}
