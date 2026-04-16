package backend

import (
	"context"
)

type Deployed struct {
	Name      string
	URL       string
	AccountID string

	Backend string

	Hostname string

	ZoneID   string
	RecordID string
}

type Backend interface {
	Name() string

	AccountID() string

	Deploy(ctx context.Context, name, script string) (*Deployed, error)

	Delete(ctx context.Context, d *Deployed) error

	List(ctx context.Context, prefix string) ([]*Deployed, error)
}
