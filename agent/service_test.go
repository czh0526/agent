package agent

import (
	"testing"

	"github.com/czh0526/agent/p2p"
	"github.com/czh0526/agent/rpc"
)

type NoopService struct{}

func (s *NoopService) Protocols() []p2p.Protocol  { return nil }
func (s *NoopService) APIs() []rpc.API            { return nil }
func (s *NoopService) Start(*p2p.P2PServer) error { return nil }
func (s *NoopService) Stop() error                { return nil }

func NewNoopService() (Service, error) {
	return new(NoopService), nil
}

type NoopServiceA struct {
	NoopService
}

type NoopServiceB struct {
	NoopService
}

type NoopServiceC struct {
	NoopService
}

func NewNoopServiceA() (Service, error) { return new(NoopServiceA), nil }
func NewNoopServiceB() (Service, error) { return new(NoopServiceB), nil }
func NewNoopServiceC() (Service, error) { return new(NoopServiceC), nil }

func TestContextServices(t *testing.T) {
	agent, err := NewAgent()
	if err != nil {
		t.Fatalf("failed to create protocol stack: %v", err)
	}

	if err := agent.Register(NewNoopServiceA); err != nil {
		t.Fatalf("failed to register service: %v", err)
	}
	if err := agent.Register(NewNoopServiceB); err != nil {
		t.Fatalf("failed to register service: %v", err)
	}

	if err := agent.Start(); err != nil {
		t.Fatalf("failed to start agent: %v", err)
	}
	defer agent.Stop()
}
