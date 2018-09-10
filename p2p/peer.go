package p2p

import (
	"sync"
	"time"

	"github.com/czh0526/agent/log"
)

const (
	pingInterval = 15 * time.Second
)

type Peer struct {
	rw *conn
	wg sync.WaitGroup
}

func newPeer(conn *conn) *Peer {
	return &Peer{
		rw: conn,
	}
}

func (p *Peer) run() {
	var (
		readErr = make(chan error, 1)
	)

	p.wg.Add(2)
	go p.readLoop(readErr)
	go p.pingLoop()
	p.wg.Wait()
}

func (p *Peer) pingLoop() {
	ping := time.NewTimer(pingInterval)
	defer p.wg.Done()
	defer ping.Stop()

	for {
		select {
		case <-ping.C:
			if _, err := p.rw.fd.Write([]byte("ping")); err != nil {
				log.Error("peer send ping msg error", "error", err)
			}
		}
	}
}

func (p *Peer) readLoop(readErr chan<- error) {
	defer p.wg.Done()

	buf := make([]byte, 4)
	for {
		if _, err := p.rw.fd.Read(buf); err != nil {
			log.Error("peer receive ping msg error", "error", err)
			<-time.After(time.Second)
		}
	}
}
