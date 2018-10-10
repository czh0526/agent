package proton

type PublicProtonAPI struct {
	p *Proton
}

func NewPublicProtonAPI(p *Proton) *PublicProtonAPI {
	return &PublicProtonAPI{p}
}

func (self *PublicProtonAPI) Version() string {
	return self.p.version()
}
