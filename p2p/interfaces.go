package p2p

type Server interface {
	Start()
	Stop() error
}
