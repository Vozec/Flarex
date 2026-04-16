package backend

import (
	"context"
	"fmt"
	"strings"

	"github.com/Vozec/flarex/internal/cfapi"
)

type WorkersDev struct {
	Account   string
	Token     string
	Subdomain string
	client    *cfapi.Client
}

func NewWorkersDev(accountID, token, subdomain string) *WorkersDev {
	return &WorkersDev{
		Account:   accountID,
		Token:     token,
		Subdomain: subdomain,
		client:    cfapi.New(accountID, token),
	}
}

func (w *WorkersDev) Name() string      { return "workers_dev" }
func (w *WorkersDev) AccountID() string { return w.Account }

func (w *WorkersDev) Deploy(ctx context.Context, name, script string) (*Deployed, error) {
	if err := w.client.UploadWorker(ctx, name, script); err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}
	if err := w.client.EnableWorkersDev(ctx, name); err != nil {
		return nil, fmt.Errorf("enable subdomain: %w", err)
	}
	hostname := fmt.Sprintf("%s.%s.workers.dev", name, w.Subdomain)
	return &Deployed{
		Name:      name,
		URL:       "https://" + hostname,
		Hostname:  hostname,
		AccountID: w.Account,
		Backend:   w.Name(),
	}, nil
}

func (w *WorkersDev) Delete(ctx context.Context, d *Deployed) error {
	return w.client.DeleteWorker(ctx, d.Name)
}

func (w *WorkersDev) List(ctx context.Context, prefix string) ([]*Deployed, error) {
	scripts, err := w.client.ListWorkers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Deployed, 0, len(scripts))
	for _, s := range scripts {
		if !strings.HasPrefix(s.ID, prefix) {
			continue
		}
		hostname := fmt.Sprintf("%s.%s.workers.dev", s.ID, w.Subdomain)
		out = append(out, &Deployed{
			Name:      s.ID,
			URL:       "https://" + hostname,
			Hostname:  hostname,
			AccountID: w.Account,
			Backend:   w.Name(),
		})
	}
	return out, nil
}
