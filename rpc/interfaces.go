package rpc

type Server interface {
	Start() error
	Stop() error
}
