package rpc

type Server interface {
	Start()
	Stop() error
}
