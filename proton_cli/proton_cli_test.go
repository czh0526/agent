package proton_cli

import (
	"context"
	"fmt"
	"testing"

	"github.com/czh0526/agent"
)

var (
	_ = proton.VersionReader(&Client{})
)

func TestAgentVersion(t *testing.T) {
	client, err := Dial("ws://127.0.0.1:8545")
	if err != nil {
		t.Error(err)
	}

	clientVersion, err := client.ProtonVersion(context.Background())
	if err != nil {
		t.Error(err)
	}

	fmt.Printf("agent version = %v \n", clientVersion)
}
