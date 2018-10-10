package p2p

type Protocol struct {
	Name    string
	Version uint
	Run     func() error
}
