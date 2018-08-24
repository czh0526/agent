package p2p

import (
	"net"

	"github.com/czh0526/agent/p2p/discover"
)

type Server interface {
	Start() error
	Stop() error
	Lookup(node discover.NodeID) []*discover.Node
	SetupConn(fd net.Conn, flag connFlag, node *discover.Node) error
	Dialer() NodeDialer
}
