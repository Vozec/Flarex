package backend

import (
	"context"
	"fmt"
	"strings"

	"github.com/Vozec/flarex/internal/cfapi"
)

type CustomDomain struct {
	Account  string
	Token    string
	ZoneID   string
	ZoneName string
	client   *cfapi.Client
}

func NewCustomDomain(accountID, token, zoneID, zoneName string) *CustomDomain {
	return &CustomDomain{
		Account:  accountID,
		Token:    token,
		ZoneID:   zoneID,
		ZoneName: zoneName,
		client:   cfapi.New(accountID, token),
	}
}

func (c *CustomDomain) Name() string      { return "custom_domain" }
func (c *CustomDomain) AccountID() string { return c.Account }

func (c *CustomDomain) Deploy(ctx context.Context, name, script string) (*Deployed, error) {
	if err := c.client.UploadWorker(ctx, name, script); err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}
	hostname := fmt.Sprintf("%s.%s", name, c.ZoneName)
	wd, err := c.client.AttachWorkerDomain(ctx, hostname, name, c.ZoneID)
	if err != nil {

		_ = c.client.DeleteWorker(ctx, name)
		return nil, fmt.Errorf("attach domain: %w", err)
	}
	return &Deployed{
		Name:      name,
		URL:       "https://" + hostname,
		Hostname:  hostname,
		AccountID: c.Account,
		Backend:   c.Name(),
		ZoneID:    c.ZoneID,
		RecordID:  wd.ID,
	}, nil
}

func (c *CustomDomain) Delete(ctx context.Context, d *Deployed) error {
	if d.RecordID != "" {
		if err := c.client.DetachWorkerDomain(ctx, d.RecordID); err != nil {

			_ = err
		}
	}
	return c.client.DeleteWorker(ctx, d.Name)
}

func (c *CustomDomain) List(ctx context.Context, prefix string) ([]*Deployed, error) {

	hostnamePrefix := prefix
	bindings, err := c.client.ListWorkerDomains(ctx, hostnamePrefix)
	if err != nil {
		return nil, err
	}
	out := make([]*Deployed, 0, len(bindings))
	for _, b := range bindings {
		if b.ZoneID != c.ZoneID {
			continue
		}
		if !strings.HasSuffix(b.Hostname, "."+c.ZoneName) && b.Hostname != c.ZoneName {
			continue
		}
		out = append(out, &Deployed{
			Name:      b.Service,
			URL:       "https://" + b.Hostname,
			Hostname:  b.Hostname,
			AccountID: c.Account,
			Backend:   c.Name(),
			ZoneID:    c.ZoneID,
			RecordID:  b.ID,
		})
	}
	return out, nil
}
