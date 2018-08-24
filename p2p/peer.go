package p2p

type Peer struct {
	rw *conn
}

func newPeer(conn *conn) *Peer {
	return &Peer{
		rw: conn,
	}
}
