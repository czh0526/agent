package proton

import "context"

type VersionReader interface {
	ProtonVersion(ctx context.Context) (string, error)
}
