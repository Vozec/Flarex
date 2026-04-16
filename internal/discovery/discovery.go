package discovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/Vozec/flarex/internal/cfapi"
	"github.com/Vozec/flarex/internal/logger"
)

type AccountDetails struct {
	Token     string
	ID        string
	Name      string
	Subdomain string
}

func Resolve(ctx context.Context, token string, createMissing bool) ([]AccountDetails, error) {
	accounts, err := cfapi.ListAccounts(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("token has access to 0 accounts (need Workers scope at account level)")
	}
	out := make([]AccountDetails, 0, len(accounts))
	for _, a := range accounts {
		c := cfapi.New(a.ID, token)
		sub, err := c.GetSubdomain(ctx)
		if err != nil || sub == "" {
			if !createMissing {
				return nil, fmt.Errorf("account %s: no workers.dev subdomain (enable one in dashboard or pass --create-subdomain)", a.ID)
			}
			newSub := "cft-" + randHex(4)
			if cerr := c.CreateSubdomain(ctx, newSub); cerr != nil {
				return nil, fmt.Errorf("account %s: create subdomain: %w", a.ID, cerr)
			}
			logger.L.Info().Str("account", a.ID).Str("subdomain", newSub).Msg("workers.dev subdomain created")
			sub = newSub
		}
		out = append(out, AccountDetails{
			Token:     token,
			ID:        a.ID,
			Name:      a.Name,
			Subdomain: sub,
		})
	}
	return out, nil
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
