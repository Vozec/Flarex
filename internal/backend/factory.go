package backend

import (
	"context"
	"fmt"

	"github.com/Vozec/flarex/internal/cfapi"
)

type Mode string

const (
	ModeAuto         Mode = "auto"
	ModeWorkersDev   Mode = "workers_dev"
	ModeCustomDomain Mode = "custom_domain"
)

func Pick(ctx context.Context, mode Mode, accountID, token, subdomain string) ([]Backend, error) {
	switch mode {
	case ModeWorkersDev:
		if subdomain == "" {
			return nil, fmt.Errorf("workers_dev requires subdomain (auto-discover or set in config)")
		}
		return []Backend{NewWorkersDev(accountID, token, subdomain)}, nil
	case ModeCustomDomain:
		zones, err := listZonesForAccount(ctx, token, accountID)
		if err != nil {
			return nil, err
		}
		if len(zones) == 0 {
			return nil, fmt.Errorf("custom_domain requested but account %s has no zones", accountID)
		}
		out := make([]Backend, 0, len(zones))
		for _, z := range zones {
			out = append(out, NewCustomDomain(accountID, token, z.ID, z.Name))
		}
		return out, nil
	case ModeAuto, "":
		zones, err := listZonesForAccount(ctx, token, accountID)
		if err == nil && len(zones) > 0 {
			out := make([]Backend, 0, len(zones))
			for _, z := range zones {
				out = append(out, NewCustomDomain(accountID, token, z.ID, z.Name))
			}
			return out, nil
		}

		if subdomain == "" {
			return nil, fmt.Errorf("auto fallback failed: no zones and no workers.dev subdomain")
		}
		return []Backend{NewWorkersDev(accountID, token, subdomain)}, nil
	default:
		return nil, fmt.Errorf("unknown backend mode %q", mode)
	}
}

func listZonesForAccount(ctx context.Context, token, accountID string) ([]cfapi.Zone, error) {
	all, err := cfapi.ListZones(ctx, token)
	if err != nil {
		return nil, err
	}

	_ = accountID
	return all, nil
}
