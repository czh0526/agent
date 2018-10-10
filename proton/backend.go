package proton

import (
	"fmt"

	"github.com/czh0526/agent/p2p"
	"github.com/czh0526/agent/rpc"
)

type Proton struct {
}

func New() (*Proton, error) {
	return &Proton{}, nil
}

func (p *Proton) APIs() []rpc.API {
	return []rpc.API{
		{
			Namespace: "proton",
			Version:   "1.0",
			Service:   NewPublicProtonAPI(p),
			Public:    true,
		},
	}
}

func (p *Proton) Protocols() []p2p.Protocol {

	protocols := make([]p2p.Protocol, len(ProtocolVersions))
	for _, version := range ProtocolVersions {
		protocols = append(protocols, p2p.Protocol{
			Name:    ProtocolName,
			Version: version,
			Run: func() error {
				fmt.Printf("%v-%v run. \n", ProtocolName, version)
				return nil
			},
		})
	}

	return protocols
}

func (p *Proton) Start(server *p2p.P2PServer) error {
	return nil
}

func (p *Proton) Stop() error {
	return nil
}

func (p *Proton) version() string {
	return "proton-1.0"
}
