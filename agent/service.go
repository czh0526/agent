package agent

import (
	"github.com/czh0526/agent/p2p"
	"github.com/czh0526/agent/rpc"
)

type ServiceConstructor func() (Service, error)

type Service interface {
	Protocols() []p2p.Protocol
	APIs() []rpc.API
	Start(server *p2p.P2PServer) error
	Stop() error
}
