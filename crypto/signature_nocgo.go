package crypto

import (
	"crypto/elliptic"

	"github.com/btcsuite/btcd/btcec"
)

func S256() elliptic.Curve {
	return btcec.S256()
}
