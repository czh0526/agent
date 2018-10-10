package proton_cli

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/czh0526/agent/rpc"
)

func Dial(rawurl string) (*Client, error) {
	return DialContext(context.Background(), rawurl)
}

func DialContext(ctx context.Context, rawurl string) (*Client, error) {
	c, err := rpc.DialContext(ctx, rawurl)
	if err != nil {
		return nil, err
	}
	return NewClient(c), nil
}

type Client struct {
	c *rpc.Client
}

func NewClient(c *rpc.Client) *Client {
	return &Client{c}
}

func (ec *Client) Close() {
	ec.c.Close()
}

func (ec *Client) ProtonVersion(ctx context.Context) (string, error) {
	var raw json.RawMessage
	err := ec.c.CallContext(ctx, &raw, "proton_version")
	if err != nil {
		return "", err
	} else if len(raw) == 0 {
		return "", errors.New("not found")
	}

	var clientVersion string
	if err := json.Unmarshal(raw, &clientVersion); err != nil {
		return "", err
	}

	return clientVersion, nil
}
