package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/Vozec/flarex/internal/admin"
	"github.com/Vozec/flarex/internal/alerts"
	"github.com/Vozec/flarex/internal/cfapi"
	"github.com/Vozec/flarex/internal/config"
	"github.com/Vozec/flarex/internal/discovery"
	"github.com/Vozec/flarex/internal/dnscache"
	"github.com/Vozec/flarex/internal/filter"
	"github.com/Vozec/flarex/internal/logger"
	"github.com/Vozec/flarex/internal/metrics"
	"github.com/Vozec/flarex/internal/pool"
	"github.com/Vozec/flarex/internal/proxy"
	"github.com/Vozec/flarex/internal/ratelimit"
	"github.com/Vozec/flarex/internal/scheduler"
	"github.com/Vozec/flarex/internal/state"
	"github.com/Vozec/flarex/internal/tracing"
	"github.com/Vozec/flarex/internal/worker"
	"github.com/coder/websocket"
	"github.com/urfave/cli/v3"
)

var (
	version   = "dev"
	commit    = ""
	buildDate = ""
)

func fullVersion() string {
	v := version
	if commit != "" {
		v += " (" + commit + ")"
	}
	if buildDate != "" {
		v += " built " + buildDate
	}
	v += " go " + runtime.Version()
	return v
}

func main() {
	app := &cli.Command{
		Name:                  "flarex",
		Usage:                 "SOCKS5 proxy rotating over Cloudflare Workers",
		Version:               fullVersion(),
		EnableShellCompletion: true,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Value:   "config.yaml",
				Sources: cli.EnvVars("FLX_CONFIG"),
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "deploy",
				Usage: "deploy Workers to Cloudflare",
				Flags: []cli.Flag{
					&cli.IntFlag{Name: "count", Aliases: []string{"n"}, Usage: "override worker.count from config"},
				},
				Action: runDeploy,
			},
			{Name: "destroy", Usage: "delete all Workers (matching config prefix)", Action: runDestroy},
			{
				Name:  "clean",
				Usage: "delete Workers + DNS records matching prefix (safe, prefix-scoped)",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "dry-run", Usage: "print what would be deleted without deleting"},
				},
				Action: runClean,
			},
			{
				Name:  "list",
				Usage: "list deployed Workers",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "json", Usage: "emit JSON instead of a table"},
				},
				Action: runList,
			},
			{
				Name:   "version",
				Usage:  "print version + commit + build date",
				Action: runVersion,
			},
			{
				Name:  "config",
				Usage: "config utilities",
				Commands: []*cli.Command{
					{
						Name:   "validate",
						Usage:  "load + validate config.yaml without running anything",
						Action: runConfigValidate,
					},
				},
			},
			{
				Name:    "server",
				Aliases: []string{"serve"},
				Usage:   "run local SOCKS5 proxy + admin HTTP",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "config-example-url",
						Usage:   "URL to fetch config.example.yaml from when local config is missing",
						Sources: cli.EnvVars("FLX_CONFIG_EXAMPLE_URL"),
					},
					&cli.BoolFlag{
						Name:  "deploy",
						Usage: "deploy Workers at startup if pool is empty",
					},
					&cli.BoolFlag{
						Name:  "destroy-on-exit",
						Usage: "destroy all Workers on graceful shutdown",
					},
					&cli.BoolFlag{
						Name:  "ephemeral",
						Usage: "shortcut for --deploy --destroy-on-exit",
					},
					&cli.StringFlag{
						Name:  "proxy-mode",
						Usage: "socket | fetch | hybrid (overrides pool.proxy_mode)",
					},
				},
				Action: runServe,
			},
			{
				Name:  "seed",
				Usage: "manually add a Worker to state (dev/test)",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Required: true},
					&cli.StringFlag{Name: "url", Required: true, Usage: "full URL (http://host:port)"},
					&cli.StringFlag{Name: "account", Value: "seed"},
				},
				Action: runSeed,
			},
			{
				Name:  "backup",
				Usage: "snapshot the bbolt state file to a standalone copy",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "out", Required: true, Usage: "destination file path"},
				},
				Action: runBackup,
			},
			{
				Name:  "restore",
				Usage: "replace state.path with a snapshot produced by `flarex backup`",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "in", Required: true, Usage: "snapshot file to restore"},
					&cli.BoolFlag{Name: "force", Usage: "overwrite state.path if it already exists"},
				},
				Action: runRestore,
			},
			clientCmd(),
		},
	}
	if err := app.Run(context.Background(), os.Args); err != nil {
		logger.L.Fatal().Err(err).Msg("fatal")
	}
}

func loadCfg(c *cli.Command) (*config.Config, error) {
	path := c.String("config")
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	logger.SetLevel(cfg.Log.Level)
	if cfg.Log.JSON {
		logger.SetJSON()
	}
	return cfg, nil
}

// quotaSnapshotLoop persists each account's daily quota usage every 10 min.
// Key = "YYYY-MM-DD|accountID" (UTC), so same-day writes overwrite. Enables
// /metrics/history time-series on admin HTTP.
func quotaSnapshotLoop(ctx context.Context, p *pool.Pool, st *state.Store) {
	if st == nil {
		return
	}
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	snap := func() {
		date := time.Now().UTC().Format("2006-01-02")
		seen := map[string]bool{}
		for _, w := range p.All() {
			if seen[w.AccountID] {
				continue
			}
			seen[w.AccountID] = true
			q := p.QuotaFor(w.AccountID)
			if q == nil {
				continue
			}
			_ = st.PutQuotaSnapshot(state.QuotaDay{
				Date:      date,
				AccountID: w.AccountID,
				Used:      q.Used(),
				Limit:     q.Limit,
			})
		}
	}
	snap()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			snap()
		}
	}
}

// quotaResumeLoop re-enables workers that were paused when their account's
// daily quota hit 100%. Runs every minute; checks each quota's used count.
// When UTC day rolls over Quota.maybeReset() clears the counter → we flip
// workers back to Healthy on the next tick.
func quotaResumeLoop(ctx context.Context, p *pool.Pool) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, w := range p.All() {
				q := p.QuotaFor(w.AccountID)
				if q == nil || q.Limit == 0 {
					continue
				}
				used := q.Used()
				if used < q.Limit && w.QuotaPaused.Load() {
					w.QuotaPaused.Store(false)
					w.Healthy.Store(true)
					logger.L.Info().Str("worker", w.Name).Str("account", w.AccountID).Uint64("used", used).Msg("quota reset: worker resumed")
				}
			}
		}
	}
}

// tailWorkerLogs starts a CF Worker tail and returns a channel of log lines.
// Cleanup deletes the remote tail + closes the WS. Channel closes on WS error
// or ctx cancel. The caller owns the returned chan — never writes back.
func tailWorkerLogs(ctx context.Context, cfg *config.Config, p *pool.Pool, workerName string) (<-chan []byte, func(), error) {
	var (
		acc   config.Account
		found bool
	)
	for _, w := range p.All() {
		if w.Name != workerName {
			continue
		}
		for _, a := range cfg.Accounts {
			if a.ID == w.AccountID {
				acc = a
				found = true
				break
			}
		}
		break
	}
	if !found {
		return nil, nil, fmt.Errorf("worker %s not found or account unknown", workerName)
	}
	cli := cfapi.New(acc.ID, acc.Token)
	tailID, wsURL, err := cli.StartTail(ctx, workerName)
	if err != nil {
		return nil, nil, fmt.Errorf("start tail: %w", err)
	}
	wsCtx, cancel := context.WithCancel(ctx)
	ws, _, err := websocket.Dial(wsCtx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{"trace-v1"},
	})
	if err != nil {
		cancel()
		_ = cli.DeleteTail(context.Background(), workerName, tailID)
		return nil, nil, fmt.Errorf("tail ws dial: %w", err)
	}
	ws.SetReadLimit(1 << 20)
	out := make(chan []byte, 32)
	go func() {
		defer close(out)
		for {
			_, data, err := ws.Read(wsCtx)
			if err != nil {
				return
			}
			select {
			case out <- data:
			case <-wsCtx.Done():
				return
			}
		}
	}()
	cleanup := func() {
		cancel()
		_ = ws.Close(websocket.StatusNormalClosure, "")
		delCtx, dc := context.WithTimeout(context.Background(), 10*time.Second)
		defer dc()
		_ = cli.DeleteTail(delCtx, workerName, tailID)
	}
	return out, cleanup, nil
}

// sanitizedConfig returns a redacted view of the running config: tokens,
// hmac_secret, admin auth, and webhook URLs become "****". Safe to serve
// from the admin /config endpoint.
func sanitizedConfig(cfg *config.Config) map[string]any {
	mask := func(s string) string {
		if s == "" {
			return ""
		}
		return "****"
	}
	accts := make([]map[string]any, 0, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		accts = append(accts, map[string]any{
			"id":        a.ID,
			"token":     mask(a.Token),
			"subdomain": a.Subdomain,
		})
	}
	return map[string]any{
		"log": map[string]any{"level": cfg.Log.Level, "json": cfg.Log.JSON},
		"listen": map[string]any{
			"socks5":    cfg.Listen.Socks5,
			"http":      cfg.Listen.HTTP,
			"auth_user": cfg.Listen.AuthUser,
			"auth_pass": mask(cfg.Listen.AuthPass),
		},
		"tokens_count": len(cfg.Tokens),
		"accounts":     accts,
		"worker": map[string]any{
			"name_prefix":          cfg.Worker.NamePrefix,
			"count":                cfg.Worker.Count,
			"deploy_backend":       cfg.Worker.DeployBackend,
			"rotate_interval_sec":  cfg.Worker.RotateIntervalS,
			"rotate_max_age_sec":   cfg.Worker.RotateMaxAgeSec,
			"rotate_max_req":       cfg.Worker.RotateMaxReq,
			"prefer_ipv4":          cfg.Worker.PreferIPv4,
		},
		"filter":     cfg.Filter,
		"pool":       cfg.Pool,
		"rate_limit": cfg.RateLimit,
		"admin": map[string]any{
			"addr":         cfg.Admin.Addr,
			"enable_pprof": cfg.Admin.EnablePprof,
			"auth":         adminAuthMode(cfg),
		},
		"state": cfg.State,
		"quota": cfg.Quota,
		"alerts": map[string]any{
			"cooldown_sec":  cfg.Alerts.CooldownSec,
			"http_webhooks": len(cfg.Alerts.HTTPWebhooks),
			"discord":       cfg.Alerts.DiscordURL != "",
		},
		"tracing":  cfg.Tracing,
		"timeouts": cfg.ResolvedTimeouts.String(),
	}
}

func adminAuthMode(cfg *config.Config) string {
	if cfg.Admin.Token != "" {
		return "bearer"
	}
	if cfg.Admin.APIKey != "" {
		return "api_key"
	}
	if cfg.Admin.BasicUser != "" {
		return "basic"
	}
	return "none"
}

// recycleNamedWorker finds a worker by name in the pool and triggers the
// rotator's recycle flow (graceful drain → deploy replacement → delete
// old). Returns the new worker name or an error.
func recycleNamedWorker(ctx context.Context, cfg *config.Config, p *pool.Pool, st *state.Store, name string) (string, error) {
	var target *pool.Worker
	for _, w := range p.All() {
		if w.Name == name {
			target = w
			break
		}
	}
	if target == nil {
		return "", fmt.Errorf("worker %q not found", name)
	}
	rot := &worker.Rotator{Cfg: cfg, Pool: p, State: st}
	// recycle is unexported; use public path: mark unhealthy, rely on
	// rotator loop. For immediate effect, directly invoke public rotator
	// API — but we don't have one. Do the minimal equivalent inline.
	script, err := worker.Render(cfg.Security.HMACSecret, cfg.Worker.TemplatePath)
	if err != nil {
		return "", err
	}
	return rot.RecyclePublic(ctx, target, script)
}

// apiKeyStoreAdapter bridges internal/state's concrete bbolt store to the
// admin.APIKeyStore interface. Keeps internal/admin free of a direct
// dependency on internal/state (avoids a cycle).
type apiKeyStoreAdapter struct{ st *state.Store }

func newAPIKeyStoreAdapter(st *state.Store) admin.APIKeyStore {
	if st == nil {
		return nil
	}
	return &apiKeyStoreAdapter{st: st}
}

func (a *apiKeyStoreAdapter) PutAPIKey(k admin.APIKeyRecord) error {
	return a.st.PutAPIKey(toStateKey(k))
}
func (a *apiKeyStoreAdapter) ListAPIKeys() ([]admin.APIKeyRecord, error) {
	raw, err := a.st.ListAPIKeys()
	if err != nil {
		return nil, err
	}
	out := make([]admin.APIKeyRecord, len(raw))
	for i, k := range raw {
		out[i] = toAdminKey(k)
	}
	return out, nil
}
func (a *apiKeyStoreAdapter) GetAPIKey(id string) (admin.APIKeyRecord, error) {
	k, err := a.st.GetAPIKey(id)
	if err != nil {
		return admin.APIKeyRecord{}, err
	}
	return toAdminKey(k), nil
}
func (a *apiKeyStoreAdapter) DeleteAPIKey(id string) error { return a.st.DeleteAPIKey(id) }
func (a *apiKeyStoreAdapter) MarkAPIKeyUsed(hash string)   { a.st.MarkAPIKeyUsed(hash) }
func (a *apiKeyStoreAdapter) SetAPIKeyDisabled(id string, d bool) error {
	return a.st.SetAPIKeyDisabled(id, d)
}

func toStateKey(k admin.APIKeyRecord) state.APIKey {
	return state.APIKey{
		ID: k.ID, Name: k.Name, Hash: k.Hash, Prefix: k.Prefix,
		Scopes: k.Scopes, Disabled: k.Disabled,
		CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt,
	}
}
func toAdminKey(k state.APIKey) admin.APIKeyRecord {
	return admin.APIKeyRecord{
		ID: k.ID, Name: k.Name, Hash: k.Hash, Prefix: k.Prefix,
		Scopes: k.Scopes, Disabled: k.Disabled,
		CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt,
		ExpiresAt: k.ExpiresAt,
	}
}

// auditAdapter bridges state.AuditEvent ↔ admin.AuditRecord.
type auditAdapter struct{ st *state.Store }

func newAuditAdapter(st *state.Store) admin.AuditLog {
	if st == nil {
		return nil
	}
	return &auditAdapter{st: st}
}

func (a *auditAdapter) PutAudit(r admin.AuditRecord) error {
	return a.st.PutAudit(state.AuditEvent{
		At: r.At, Who: r.Who, Action: r.Action, Target: r.Target, Detail: r.Detail,
	})
}

func (a *auditAdapter) ListAudit(limit int) ([]admin.AuditRecord, error) {
	raw, err := a.st.ListAudit(limit)
	if err != nil {
		return nil, err
	}
	out := make([]admin.AuditRecord, len(raw))
	for i, e := range raw {
		out[i] = admin.AuditRecord{
			At: e.At, Who: e.Who, Action: e.Action, Target: e.Target, Detail: e.Detail,
		}
	}
	return out, nil
}

// metricsPersistAdapter bridges state.Store to metrics.Persistor. Used by
// the snapshot loop to backfill the in-memory ring on restart + keep 7
// days of history in bbolt.
type metricsPersistAdapter struct{ st *state.Store }

func (a *metricsPersistAdapter) PutMetricsSample(at time.Time, raw []byte) error {
	if a.st == nil {
		return nil
	}
	return a.st.PutMetricsSample(at, raw)
}
func (a *metricsPersistAdapter) ListMetricsSamples(since time.Time) ([][]byte, error) {
	if a.st == nil {
		return nil, nil
	}
	return a.st.ListMetricsSamples(since)
}
func (a *metricsPersistAdapter) PruneMetricsSamples(before time.Time) error {
	if a.st == nil {
		return nil
	}
	return a.st.PruneMetricsSamples(before)
}

// deriveSessionSecret returns a 32-byte key derived from the HMAC secret
// + a domain separator. Used to sign the session cookie and the CSRF
// token. Restarting with a fresh `security.hmac_secret` invalidates all
// existing sessions — logins must happen again.
func deriveSessionSecret(hmacSecret string) []byte {
	h := sha256.Sum256([]byte(hmacSecret + ":admin-session"))
	return h[:]
}

func boolOnOff(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}

func applyResolvedTimeouts(cfg *config.Config) {
	t := cfg.ResolvedTimeouts
	proxy.DialTimeout = t.Dial
	proxy.ProbeTimeout = t.Probe
	proxy.ProbeFetchTimeout = t.ProbeFetch
	proxy.CFCacheTTL = t.CFBlockedCacheTTL
	proxy.PrewarmAttemptTimeout = t.PrewarmAttempt
	proxy.PrewarmRetries = t.PrewarmRetries
	pool.BreakerIntervalD = t.BreakerInterval
	pool.BreakerOpenTimeoutD = t.BreakerOpen
	pool.HTTPIdleConnD = t.IdleConn
	pool.HTTPTLSHandshakeD = t.TLSHandshake
	pool.HTTPH2ReadIdleD = t.H2ReadIdle
	pool.HTTPH2PingD = t.H2Ping
	pool.HealthIntervalD = t.HealthCheckInterval
	pool.HealthTimeoutD = t.HealthCheckTimeout
	admin.ShutdownTimeoutD = t.AdminShutdown
	admin.TokenOpTimeoutD = t.AdminTokenOp
	cfapi.APITimeout = t.CFAPI
	worker.DrainTimeoutD = t.DrainTimeout
	worker.DrainPollD = t.DrainPoll
	logger.L.Info().Str("timeouts", t.String()).Msg("timeouts applied")
}

func seedQuotaFromCF(ctx context.Context, cfg *config.Config, quotas map[string]*pool.Quota) {
	tokenFor := make(map[string]string)
	for _, t := range cfg.Tokens {
		if t == "" {
			continue
		}
		accs, err := cfapi.ListAccounts(ctx, t)
		if err != nil {
			continue
		}
		for _, a := range accs {
			if _, exists := tokenFor[a.ID]; !exists {
				tokenFor[a.ID] = t
			}
		}
	}
	for _, a := range cfg.Accounts {
		if a.Token != "" && a.ID != "" {
			if _, exists := tokenFor[a.ID]; !exists {
				tokenFor[a.ID] = a.Token
			}
		}
	}
	for accID, q := range quotas {
		tok := tokenFor[accID]
		if tok == "" {
			continue
		}
		used, err := cfapi.TodayWorkerRequests(ctx, tok, accID)
		if err != nil {
			logger.L.Warn().Str("account", accID).Err(err).Msg("quota seed failed (will start at 0)")
			continue
		}
		q.Seed(used)
		logger.L.Info().Str("account", accID).Uint64("seeded_with", used).Uint64("limit", q.Limit).Msg("quota seeded from CF Analytics")
	}
}

func lookupAccountNames(ctx context.Context, cfg *config.Config) map[string]string {
	out := make(map[string]string)
	seenTok := make(map[string]bool)
	tokens := append([]string(nil), cfg.Tokens...)
	for _, a := range cfg.Accounts {
		tokens = append(tokens, a.Token)
	}
	for _, tok := range tokens {
		if tok == "" || seenTok[tok] {
			continue
		}
		seenTok[tok] = true
		accs, err := cfapi.ListAccounts(ctx, tok)
		if err != nil {
			continue
		}
		for _, info := range accs {
			out[info.ID] = info.Name
		}
	}
	return out
}

func resolveBareTokens(ctx context.Context, cfg *config.Config) error {
	known := make(map[string]bool, len(cfg.Accounts))
	for _, a := range cfg.Accounts {
		if a.ID != "" {
			known[a.ID+"|"+a.Token] = true
		}
	}
	for _, tok := range cfg.Tokens {
		details, err := discovery.Resolve(ctx, tok, true)
		if err != nil {
			return fmt.Errorf("discover token: %w", err)
		}
		for _, d := range details {
			key := d.ID + "|" + d.Token
			if known[key] {
				continue
			}
			cfg.Accounts = append(cfg.Accounts, config.Account{
				ID:        d.ID,
				Name:      d.Name,
				Token:     d.Token,
				Subdomain: d.Subdomain,
			})
			known[key] = true
			logger.L.Info().Str("account", d.ID).Str("name", d.Name).Str("subdomain", d.Subdomain).Msg("account resolved from token")
		}
	}
	return nil
}

func runDeploy(ctx context.Context, c *cli.Command) error {
	cfg, err := loadCfg(c)
	if err != nil {
		return err
	}
	if err := cfg.ValidateForDeploy(); err != nil {
		return err
	}
	if err := resolveBareTokens(ctx, cfg); err != nil {
		return err
	}
	if n := c.Int("count"); n > 0 {
		cfg.Worker.Count = n
	}
	st, err := state.Open(cfg.State.Path)
	if err != nil {
		return err
	}
	defer st.Close()
	logger.L.Info().Int("count_per_account", cfg.Worker.Count).Int("accounts", len(cfg.Accounts)).Msg("deploying")
	workers, err := worker.DeployAll(ctx, cfg, st)
	if err != nil {
		return err
	}
	printWorkerSummary("DEPLOYED", workers)
	return nil
}

// printWorkerSummary renders a short table of workers after a
// deploy/destroy/seed operation. Skipped if the slice is empty.
func printWorkerSummary(heading string, ws []*pool.Worker) {
	if len(ws) == 0 {
		fmt.Printf("\n%s: nothing\n", heading)
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "\n%s (%d)\n", heading, len(ws))
	fmt.Fprintln(tw, "NAME\tBACKEND\tACCOUNT\tURL")
	fmt.Fprintln(tw, "----\t-------\t-------\t---")
	for _, w := range ws {
		acc := w.AccountID
		if len(acc) > 12 {
			acc = acc[:8] + "…"
		}
		b := w.Backend
		if b == "" {
			b = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", w.Name, b, acc, w.URL)
	}
	_ = tw.Flush()
}

func runDestroy(ctx context.Context, c *cli.Command) error {
	cfg, err := loadCfg(c)
	if err != nil {
		return err
	}
	if err := cfg.ValidateForDeploy(); err != nil {
		return err
	}
	if err := resolveBareTokens(ctx, cfg); err != nil {
		return err
	}
	st, err := state.Open(cfg.State.Path)
	if err != nil {
		return err
	}
	defer st.Close()
	ws, _ := worker.ListAll(ctx, cfg)
	if err := worker.DestroyAll(ctx, cfg, st); err != nil {
		return err
	}
	printWorkerSummary("DESTROYED", ws)
	return nil
}

func runClean(ctx context.Context, c *cli.Command) error {
	cfg, err := loadCfg(c)
	if err != nil {
		return err
	}
	if err := cfg.ValidateForDeploy(); err != nil {
		return err
	}
	if err := resolveBareTokens(ctx, cfg); err != nil {
		return err
	}
	st, err := state.Open(cfg.State.Path)
	if err != nil {
		return err
	}
	defer st.Close()

	dryRun := c.Bool("dry-run")
	workers, dnsRows, err := runCleanCore(ctx, cfg, st, dryRun)
	if err != nil {
		return err
	}

	if dryRun {
		if len(workers) == 0 {
			fmt.Println("no workers to delete")
		} else {
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "\n[DRY-RUN] WORKERS TO DELETE")
			fmt.Fprintln(tw, "NAME")
			fmt.Fprintln(tw, "----")
			for _, n := range workers {
				fmt.Fprintf(tw, "%s\n", n)
			}
			_ = tw.Flush()
			fmt.Printf("-- %d worker(s)\n", len(workers))
		}
		if len(dnsRows) == 0 {
			fmt.Println("\nno DNS records to delete")
		} else {
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "\n[DRY-RUN] DNS RECORDS TO DELETE")
			fmt.Fprintln(tw, "ZONE\tTYPE\tNAME")
			fmt.Fprintln(tw, "----\t----\t----")
			for _, r := range dnsRows {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Zone, r.Type, r.Name)
			}
			_ = tw.Flush()
			fmt.Printf("-- %d record(s)\n", len(dnsRows))
		}
	}
	return nil
}

// runCleanCore is the shared prefix-scoped purge used by both the local
// `flarex clean` CLI command and the remote POST /workers/clean admin
// endpoint. Returns the list of worker names and DNS records that were
// (or, in dry-run mode, would be) deleted.
func runCleanCore(ctx context.Context, cfg *config.Config, st *state.Store, dryRun bool) ([]string, []admin.CleanDNSRecord, error) {
	prefix := cfg.Worker.NamePrefix
	if prefix == "" {
		return nil, nil, fmt.Errorf("worker.name_prefix empty — refusing to clean (would match everything)")
	}
	if cfg.Worker.NamePrefixDefaulted {
		return nil, nil, fmt.Errorf("worker.name_prefix was not set in config (silently defaulted to %q) — refusing to clean; explicitly set worker.name_prefix in config.yaml to acknowledge the scope", prefix)
	}
	if len(prefix) < 3 {
		return nil, nil, fmt.Errorf("worker.name_prefix=%q is too short (< 3 chars) — refusing to clean; use a more specific prefix", prefix)
	}

	ws, _ := worker.ListAll(ctx, cfg)
	workerNames := make([]string, 0, len(ws))
	for _, w := range ws {
		workerNames = append(workerNames, w.Name)
	}
	if !dryRun {
		if err := worker.DestroyAll(ctx, cfg, st); err != nil {
			logger.L.Warn().Err(err).Msg("some workers failed to delete")
		}
	}

	var dnsRows []admin.CleanDNSRecord
	seen := make(map[string]bool)
	for _, a := range cfg.Accounts {
		if seen[a.Token] {
			continue
		}
		seen[a.Token] = true
		zones, err := cfapi.ListZones(ctx, a.Token)
		if err != nil {
			logger.L.Warn().Err(err).Msg("list zones (token may lack DNS scope)")
			continue
		}
		for _, z := range zones {
			recs, err := cfapi.ListDNSRecords(ctx, a.Token, z.ID, prefix)
			if err != nil {
				logger.L.Warn().Str("zone", z.Name).Err(err).Msg("list dns")
				continue
			}
			for _, r := range recs {
				if !strings.HasPrefix(r.Name, prefix) {
					continue
				}
				row := admin.CleanDNSRecord{Zone: z.Name, Type: r.Type, Name: r.Name, ID: r.ID}
				if dryRun {
					dnsRows = append(dnsRows, row)
					continue
				}
				if err := cfapi.DeleteDNSRecord(ctx, a.Token, z.ID, r.ID); err != nil {
					logger.L.Warn().Str("name", r.Name).Err(err).Msg("delete dns")
					continue
				}
				dnsRows = append(dnsRows, row)
				logger.L.Info().Str("zone", z.Name).Str("name", r.Name).Str("type", r.Type).Msg("DNS record deleted (prefix match)")
			}
		}
	}
	return workerNames, dnsRows, nil
}

func runBackup(_ context.Context, c *cli.Command) error {
	cfg, err := loadCfg(c)
	if err != nil {
		return err
	}
	out := c.String("out")
	if out == "" {
		return fmt.Errorf("--out required")
	}
	st, err := state.Open(cfg.State.Path)
	if err != nil {
		return fmt.Errorf("open state %s: %w", cfg.State.Path, err)
	}
	defer st.Close()
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", out, err)
	}
	defer f.Close()
	if err := st.Backup(f); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	fi, _ := f.Stat()
	logger.L.Info().Str("src", cfg.State.Path).Str("dst", out).Int64("bytes", fi.Size()).Msg("state snapshot written")
	return nil
}

func runRestore(_ context.Context, c *cli.Command) error {
	cfg, err := loadCfg(c)
	if err != nil {
		return err
	}
	in := c.String("in")
	if in == "" {
		return fmt.Errorf("--in required")
	}
	// Quick validation: open the snapshot to confirm it's a valid bbolt file.
	chk, err := state.Open(in)
	if err != nil {
		return fmt.Errorf("snapshot %s not a valid bbolt file: %w", in, err)
	}
	_ = chk.Close()
	dst := cfg.State.Path
	if _, err := os.Stat(dst); err == nil {
		if !c.Bool("force") {
			return fmt.Errorf("destination %s exists — rerun with --force to overwrite", dst)
		}
		// Back up the existing file alongside with .bak suffix so the user
		// can revert a bad restore. Best-effort.
		_ = os.Rename(dst, dst+".bak")
		logger.L.Warn().Str("bak", dst+".bak").Msg("existing state renamed — remove after verifying restore")
	}
	// Copy in → dst.
	src, err := os.Open(in)
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	logger.L.Info().Str("src", in).Str("dst", dst).Msg("state restored")
	return nil
}

func runSeed(ctx context.Context, c *cli.Command) error {
	cfg, err := loadCfg(c)
	if err != nil {
		return err
	}
	st, err := state.Open(cfg.State.Path)
	if err != nil {
		return err
	}
	defer st.Close()
	rec := state.WorkerRec{
		Name:      c.String("name"),
		AccountID: c.String("account"),
		URL:       c.String("url"),
		CreatedAt: time.Now(),
	}
	if err := st.PutWorker(rec); err != nil {
		return err
	}
	logger.L.Info().Str("name", rec.Name).Str("url", rec.URL).Msg("seeded")
	return nil
}

func runList(ctx context.Context, c *cli.Command) error {
	cfg, err := loadCfg(c)
	if err != nil {
		return err
	}
	if err := resolveBareTokens(ctx, cfg); err != nil {
		return err
	}
	ws, err := worker.ListAll(ctx, cfg)
	if err != nil {
		return err
	}
	if c.Bool("json") {
		type row struct {
			Name, URL, Account, Backend, Hostname string
		}
		out := make([]row, 0, len(ws))
		for _, w := range ws {
			out = append(out, row{w.Name, w.URL, w.AccountID, w.Backend, w.Hostname})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	if len(ws) == 0 {
		fmt.Println("no workers deployed")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tBACKEND\tACCOUNT\tURL")
	fmt.Fprintln(tw, "----\t-------\t-------\t---")
	for _, w := range ws {
		acc := w.AccountID
		if len(acc) > 12 {
			acc = acc[:8] + "…"
		}
		backend := w.Backend
		if backend == "" {
			backend = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", w.Name, backend, acc, w.URL)
	}
	_ = tw.Flush()
	logger.L.Info().Int("count", len(ws)).Msg("workers")
	return nil
}

// runVersion prints the ldflags-injected version / commit / build date.
// Hooks the `flarex version` subcommand.
func runVersion(_ context.Context, _ *cli.Command) error {
	fmt.Printf("flarex %s (commit %s, built %s)\n", version, commit, buildDate)
	return nil
}

// runConfigValidate loads config.yaml + resolves timeouts + runs the
// deploy/server validators, then exits cleanly. No network I/O, no state
// DB touched — safe to run in CI.
func runConfigValidate(ctx context.Context, c *cli.Command) error {
	cfg, err := loadCfg(c)
	if err != nil {
		return err
	}
	if err := cfg.ValidateForServer(); err != nil {
		return fmt.Errorf("server validation: %w", err)
	}
	if err := cfg.ValidateForDeploy(); err != nil {
		// Non-fatal; users may only care about server validation.
		logger.L.Warn().Err(err).Msg("deploy validators failed — ok if you only run `server` without --deploy")
	}
	fmt.Printf("config ok  path=%s\n", c.String("config"))
	fmt.Printf("  accounts=%d tokens=%d listen.socks5=%s admin=%s state=%s\n",
		len(cfg.Accounts), len(cfg.Tokens), cfg.Listen.Socks5, cfg.Admin.Addr, cfg.State.Path)
	fmt.Printf("  worker.count=%d prefix=%q backend=%s proxy_mode=%s\n",
		cfg.Worker.Count, cfg.Worker.NamePrefix, cfg.Worker.DeployBackend, cfg.Pool.ProxyMode)
	fmt.Printf("  timeouts: %s\n", cfg.ResolvedTimeouts.String())
	return nil
}

func runServe(ctx context.Context, c *cli.Command) error {
	logger.PrintBanner(version, commit, buildDate)

	cfgPath := c.String("config")
	exampleURL := c.String("config-example-url")
	dl, err := config.Bootstrap(ctx, cfgPath, exampleURL)
	if err != nil {
		return fmt.Errorf("bootstrap config: %w", err)
	}
	if dl {
		logger.L.Warn().Str("path", cfgPath).Msg("config downloaded — edit it with real CF credentials before re-running")
		return fmt.Errorf("config %s freshly downloaded; edit it then re-run", cfgPath)
	}

	if marker, err := config.LooksLikeTemplate(cfgPath); err == nil && marker != "" {
		return fmt.Errorf("config %s still contains template placeholder %q — edit it first", cfgPath, marker)
	}

	cfg, err := loadCfg(c)
	if err != nil {
		return err
	}
	if err := cfg.ValidateForServer(); err != nil {
		return err
	}

	autoDeploy := c.Bool("deploy") || c.Bool("ephemeral")
	destroyOnExit := c.Bool("destroy-on-exit") || c.Bool("ephemeral")
	if m := c.String("proxy-mode"); m != "" {
		cfg.Pool.ProxyMode = m
	}

	st, err := state.Open(cfg.State.Path)
	if err != nil {
		return err
	}
	defer st.Close()

	ws, err := worker.LoadFromState(st)
	if err != nil || len(ws) == 0 {
		logger.L.Info().Msg("state empty, fetching via CF API")
		ws, err = worker.ListAll(ctx, cfg)
		if err != nil {
			return err
		}
		for _, w := range ws {
			_ = st.PutWorker(state.WorkerRec{
				Name:      w.Name,
				AccountID: w.AccountID,
				URL:       w.URL,
				CreatedAt: w.CreatedAt,
				Backend:   w.Backend,
				Hostname:  w.Hostname,
				ZoneID:    w.ZoneID,
				RecordID:  w.RecordID,
			})
		}
	}
	if len(ws) == 0 && autoDeploy {
		if err := cfg.ValidateForDeploy(); err != nil {
			return err
		}
		if err := resolveBareTokens(ctx, cfg); err != nil {
			return err
		}
		logger.L.Info().Msg("auto-deploy: no Workers in state, deploying...")
		ws, err = worker.DeployAll(ctx, cfg, st)
		if err != nil {
			return fmt.Errorf("auto-deploy: %w", err)
		}
	} else if len(cfg.Tokens) > 0 && len(cfg.Accounts) == 0 {
		// Even without --deploy, resolve tokens→accounts so that admin
		// runtime ops (recycle, add-token, remove-token) have the data
		// they need. Failure is non-fatal — just skip.
		if err := resolveBareTokens(ctx, cfg); err != nil {
			logger.L.Warn().Err(err).Msg("token resolution failed; admin recycle/remove-token will be unavailable")
		}
	}
	if len(ws) == 0 {
		return fmt.Errorf("no worker found — run 'deploy' first or pass --deploy/--ephemeral")
	}
	p := pool.New(ws)
	logger.L.Info().Int("pool_size", p.Size()).Bool("auto_deploy", autoDeploy).Bool("destroy_on_exit", destroyOnExit).Msg("pool loaded")

	var sched scheduler.Scheduler
	switch cfg.Pool.Strategy {
	case "least_inflight":
		sched = scheduler.NewLeastInflight(p)
	default:
		sched = scheduler.NewRoundRobin(p)
	}

	filt, err := filter.NewIPFilter(cfg.Filter.DenyCIDRs, cfg.Filter.AllowPorts)
	if err != nil {
		return err
	}

	var authOpt *proxy.Auth
	if cfg.Listen.AuthUser != "" {
		authOpt = &proxy.Auth{User: cfg.Listen.AuthUser, Pass: cfg.Listen.AuthPass}
	}

	var rl *ratelimit.PerHost
	if cfg.RateLimit.PerHostQPS > 0 {
		rl = ratelimit.NewPerHost(cfg.RateLimit.PerHostQPS, cfg.RateLimit.PerHostBurst)
	}

	quotas := make(map[string]*pool.Quota)
	for _, a := range cfg.Accounts {
		if a.ID != "" {
			quotas[a.ID] = pool.NewQuota(a.ID, cfg.Quota.DailyLimit)
		}
	}
	for _, w := range p.All() {
		if w.AccountID == "" {
			continue
		}
		if _, ok := quotas[w.AccountID]; !ok {
			quotas[w.AccountID] = pool.NewQuota(w.AccountID, cfg.Quota.DailyLimit)
		}
	}
	p.SetQuotas(quotas)

	if cfg.Quota.SeedFromCloudflare {
		seedQuotaFromCF(ctx, cfg, quotas)
	}

	var sinks []alerts.Sink
	for _, h := range cfg.Alerts.HTTPWebhooks {
		if h.URL != "" {
			sinks = append(sinks, alerts.NewHTTPSink(h.URL, h.Headers))
		}
	}
	if cfg.Alerts.DiscordURL != "" {
		sinks = append(sinks, alerts.NewDiscordSink(cfg.Alerts.DiscordURL, cfg.Alerts.DiscordName))
	}
	disp := alerts.NewDispatcher(time.Duration(cfg.Alerts.CooldownSec)*time.Second, sinks...)
	warnThreshold := uint64(float64(cfg.Quota.DailyLimit) * float64(cfg.Quota.WarnPercent) / 100.0)

	totalDaily := cfg.Quota.DailyLimit * uint64(len(quotas))
	logger.PrintConfig([]logger.ConfigSection{
		{Title: "Pool", Rows: []logger.ConfigRow{
			{Key: "workers", Value: fmt.Sprintf("%d loaded across %d account(s)", p.Size(), len(quotas))},
			{Key: "backend", Value: cfg.Worker.DeployBackend},
			{Key: "strategy", Value: cfg.Pool.Strategy},
			{Key: "proxy_mode", Value: cfg.Pool.ProxyMode},
		}},
		{Title: "Listeners", Rows: []logger.ConfigRow{
			{Key: "socks5", Value: cfg.Listen.Socks5},
			{Key: "http", Value: cfg.Listen.HTTP},
			{Key: "admin", Value: cfg.Admin.Addr},
		}},
		{Title: "Quota", Rows: []logger.ConfigRow{
			{Key: "daily_limit_per_account", Value: fmt.Sprintf("%d", cfg.Quota.DailyLimit)},
			{Key: "daily_limit_total", Value: fmt.Sprintf("%d", totalDaily)},
			{Key: "warn_threshold", Value: fmt.Sprintf("%d%% (%d req)", cfg.Quota.WarnPercent, warnThreshold)},
		}},
		{Title: "Alerts", Rows: []logger.ConfigRow{
			{Key: "sinks", Value: fmt.Sprintf("%d", len(sinks))},
			{Key: "discord", Value: boolOnOff(cfg.Alerts.DiscordURL != "")},
			{Key: "http_webhooks", Value: fmt.Sprintf("%d", len(cfg.Alerts.HTTPWebhooks))},
		}},
		{Title: "Features", Rows: []logger.ConfigRow{
			{Key: "tls_rewrap", Value: boolOnOff(cfg.Pool.TLSRewrap)},
			{Key: "hedge_after_ms", Value: fmt.Sprintf("%d", cfg.Pool.HedgeAfterMs)},
			{Key: "tracing", Value: cfg.Tracing.Endpoint},
			{Key: "state_path", Value: cfg.State.Path},
		}},
	})
	logger.L.Info().Msg("FlareX server ready")

	accountNames := lookupAccountNames(ctx, cfg)

	quotaHook := func(accountID string) {
		q := p.QuotaFor(accountID)
		if q == nil {
			return
		}
		n, hit := q.Inc()
		name := accountNames[accountID]
		if hit {
			for _, w := range p.ByAccount(accountID) {
				w.QuotaPaused.Store(true)
				w.Healthy.Store(false)
			}
			disp.Fire(context.Background(), alerts.Event{
				Kind:        alerts.KindQuotaLimit,
				AccountID:   accountID,
				AccountName: name,
				Used:        n,
				Limit:       q.Limit,
				Message:     fmt.Sprintf("Daily quota exhausted (%d/%d). Workers paused until UTC midnight reset.", n, q.Limit),
			})
			return
		}
		if warnThreshold > 0 && n >= warnThreshold {
			disp.Fire(context.Background(), alerts.Event{
				Kind:        alerts.KindQuotaWarn,
				AccountID:   accountID,
				AccountName: name,
				Used:        n,
				Limit:       q.Limit,
				Message:     fmt.Sprintf("Crossed %d%% of daily quota. Consider rotating accounts or slowing down.", cfg.Quota.WarnPercent),
			})
		}
	}

	srv := &proxy.Server{
		Auth:        authOpt,
		Filter:      filt,
		Scheduler:   sched,
		Pool:        p,
		RateLimit:   rl,
		HMACSecret:  cfg.Security.HMACSecret,
		MaxRetries:  cfg.Pool.MaxRetries,
		BaseBackoff: time.Duration(cfg.Pool.BackoffMs) * time.Millisecond,
		HedgeAfter:  time.Duration(cfg.Pool.HedgeAfterMs) * time.Millisecond,
		TLSRewrap:   cfg.Pool.TLSRewrap,
		PoolSize:    cfg.Pool.GoroutineSize,
		QuotaHook:   quotaHook,
	}
	srv.SetProxyMode(cfg.Pool.ProxyMode)

	ln, err := proxy.Listen(ctx, cfg.Listen.Socks5, cfg.Listen.UnixPerms)
	if err != nil {
		return err
	}
	logger.L.Info().Str("addr", cfg.Listen.Socks5).Msg("SOCKS5 listening")

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-sigCtx.Done()
		_ = ln.Close()
	}()

	applyResolvedTimeouts(cfg)
	if cfg.Tracing.Endpoint != "" {
		shutdown, err := tracing.Init(sigCtx, cfg.Tracing.Endpoint, cfg.Tracing.Insecure)
		if err != nil {
			logger.L.Warn().Err(err).Str("endpoint", cfg.Tracing.Endpoint).Msg("tracing init failed")
		} else {
			logger.L.Info().Str("endpoint", cfg.Tracing.Endpoint).Bool("insecure", cfg.Tracing.Insecure).Msg("otlp tracing enabled")
			defer func() {
				tctx, tcancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer tcancel()
				_ = shutdown(tctx)
			}()
		}
	}
	dc := dnscache.New(cfg.ResolvedTimeouts.DNSCacheTTL)
	hostnames := make([]string, 0, p.Size())
	for _, w := range p.All() {
		if w.Hostname != "" {
			hostnames = append(hostnames, w.Hostname)
		}
	}
	dc.Warm(sigCtx, hostnames)
	go dc.RefreshLoop(sigCtx)
	pool.SharedDialer = dc.DialContext
	proxy.PreferIPv4 = cfg.Worker.PreferIPv4
	proxy.PreferIPv4Resolver = dc.LookupIPv4
	proxy.ProbeDisabled = cfg.Pool.DisableProbe
	logger.L.Info().Int("hostnames", len(hostnames)).Msg("DNS cache warmed")

	go proxy.Prewarm(sigCtx, p)
	go proxy.KeepAlive(sigCtx, p)

	go func() {
		tmpRot := &worker.Rotator{Cfg: cfg, Pool: p, State: st}
		time.Sleep(3 * time.Second)
		tmpRot.VerifyTemplates(sigCtx)
	}()

	go quotaResumeLoop(sigCtx, p)
	go quotaSnapshotLoop(sigCtx, p, st)

	hc := pool.NewHealthChecker(p)
	go hc.Run(sigCtx)

	if cfg.Worker.RotateMaxAgeSec > 0 || cfg.Worker.RotateMaxReq > 0 {
		rot := &worker.Rotator{
			Cfg:      cfg,
			Pool:     p,
			State:    st,
			Interval: time.Duration(cfg.Worker.RotateIntervalS) * time.Second,
			MaxAge:   time.Duration(cfg.Worker.RotateMaxAgeSec) * time.Second,
			MaxReq:   cfg.Worker.RotateMaxReq,
		}
		go rot.Run(sigCtx)
		logger.L.Info().Dur("interval", rot.Interval).Dur("max_age", rot.MaxAge).Uint64("max_req", rot.MaxReq).Msg("auto-rotation enabled")
	}

	if cfg.Admin.Addr != "" {
		// When the UI is on, start the in-memory metrics ring so the
		// /metrics/series chart has data to show. 1-min cadence. Passes
		// the state store as a persistor so the ring survives restarts.
		if cfg.Admin.UI {
			metrics.SetWorkerRequestsFn(func() map[string]uint64 {
				out := make(map[string]uint64)
				for _, w := range p.All() {
					out[w.Name] = w.Requests.Load()
				}
				return out
			})
			// 15 s snapshot — 60-slot ring covers 15 min of history, and
			// throughput shows up in the UI within seconds of first traffic
			// instead of the dead-zero period a 1-minute ticker caused.
			stopSeries := metrics.StartSnapshotLoop(15*time.Second, nil, &metricsPersistAdapter{st: st})
			defer stopSeries()
		}
		adm := &admin.Server{
			Addr:        cfg.Admin.Addr,
			Pool:        p,
			Token:       cfg.Admin.Token,
			APIKey:      cfg.Admin.APIKey,
			BasicUser:   cfg.Admin.BasicUser,
			BasicPass:   cfg.Admin.BasicPass,
			EnablePprof: cfg.Admin.EnablePprof,
			UIEnabled:   cfg.Admin.UI,
			TOTPSecret:  cfg.Admin.TOTPSecret,
			APIKeys:     newAPIKeyStoreAdapter(st),
			Audit:       newAuditAdapter(st),
			TestHistory: st,
			AccountNamesFunc: func() map[string]string {
				out := make(map[string]string, len(cfg.Accounts))
				for _, a := range cfg.Accounts {
					if a.Name != "" {
						out[a.ID] = a.Name
					}
				}
				return out
			},
			UpdateConfigFunc: func(path string, value any) (bool, bool, error) {
				applied, restart, err := applyConfigUpdate(cfg, srv, filt, path, value)
				if err == nil {
					configLogApplied(path, restart)
				}
				return applied, restart, err
			},
			DeployMoreFunc: func(aCtx context.Context, accountID string, count int) ([]string, error) {
				// Lookup the stored token from cfg.Accounts so the UI doesn't
				// have to re-ask. Single-token accounts only — if the same id
				// is registered twice with different tokens we take the first.
				var acc *config.Account
				for i := range cfg.Accounts {
					if cfg.Accounts[i].ID == accountID {
						acc = &cfg.Accounts[i]
						break
					}
				}
				if acc == nil {
					return nil, fmt.Errorf("account %s not found in running config", accountID)
				}
				if count <= 0 {
					count = cfg.Worker.Count
				}
				ws, err := worker.DeployOnAccount(aCtx, cfg, st, *acc, count)
				if err != nil {
					return nil, err
				}
				var names []string
				for _, w := range ws {
					p.Add(w)
					names = append(names, w.Name)
				}
				if quotas[acc.ID] == nil {
					quotas[acc.ID] = pool.NewQuota(acc.ID, cfg.Quota.DailyLimit)
				}
				return names, nil
			},
			AlertHook: func(aCtx context.Context, source, summary, severity, status string) {
				disp.Fire(aCtx, alerts.Event{
					Kind:      alerts.Kind("alertmanager." + status),
					AccountID: source,
					Message:   "[" + severity + "] " + summary,
					At:        time.Now().UTC(),
				})
			},
			SeriesFunc:  func() any { return metrics.DefaultSeries.Snapshot() },
			AddTokenFunc: func(aCtx context.Context, tok string, count int) ([]string, error) {
				// Validation step: Resolve exercises cfapi.ListAccounts +
				// GetSubdomain which together cover the Account + Workers
				// scope checks. Invalid token → CF code 9109. Token valid
				// but missing Workers: Scripts scope → 1000/9106 or
				// empty-subdomain error. Surface all with a clean prefix
				// so the UI toast is readable.
				trimmed := strings.TrimSpace(tok)
				if trimmed == "" {
					return nil, fmt.Errorf("token is empty")
				}
				details, err := discovery.Resolve(aCtx, trimmed, true)
				if err != nil {
					return nil, fmt.Errorf("token validation failed: %w", err)
				}
				if len(details) == 0 {
					return nil, fmt.Errorf("token resolved to 0 accounts — needs Account.Workers Scripts: Edit")
				}

				// Dedup: reject if the resolved account is already registered
				// AND has workers in the pool. Re-posting the same token
				// would otherwise double the pool silently.
				existing := make(map[string]int, len(cfg.Accounts))
				for _, a := range cfg.Accounts {
					existing[a.ID]++
				}
				for _, d := range details {
					if existing[d.ID] > 0 && len(p.ByAccount(d.ID)) > 0 {
						label := d.Name
						if label == "" {
							label = d.ID
						}
						return nil, fmt.Errorf("account already registered: %s (%s) — use the Accounts tab → Deploy more instead", label, d.ID[:8])
					}
				}

				if count <= 0 {
					count = cfg.Worker.Count
				}
				var deployedNames []string
				for _, d := range details {
					acc := config.Account{ID: d.ID, Name: d.Name, Token: d.Token, Subdomain: d.Subdomain}
					ws, err := worker.DeployOnAccount(aCtx, cfg, st, acc, count)
					if err != nil {
						return deployedNames, fmt.Errorf("deploy on %s: %w", d.ID, err)
					}
					for _, w := range ws {
						p.Add(w)
						deployedNames = append(deployedNames, w.Name)
					}
					if quotas[acc.ID] == nil {
						quotas[acc.ID] = pool.NewQuota(acc.ID, cfg.Quota.DailyLimit)
					}
					cfg.Accounts = append(cfg.Accounts, acc)
					logger.L.Info().Str("account", acc.ID).Str("name", acc.Name).Int("workers", len(ws)).Msg("admin: token added + workers deployed")
				}
				return deployedNames, nil
			},
			LogTailFunc: func(aCtx context.Context, workerName string) (<-chan []byte, func(), error) {
				return tailWorkerLogs(aCtx, cfg, p, workerName)
			},
			DestroyAllFunc: func(aCtx context.Context) ([]string, error) {
				ws, _ := worker.ListAll(aCtx, cfg)
				names := make([]string, 0, len(ws))
				for _, w := range ws {
					names = append(names, w.Name)
				}
				if err := worker.DestroyAll(aCtx, cfg, st); err != nil {
					return names, err
				}
				// Drain the in-memory pool so the CLI sees an empty /status
				// right after a successful destroy.
				for _, w := range p.All() {
					p.Remove(w)
				}
				return names, nil
			},
			CleanFunc: func(aCtx context.Context, dryRun bool) ([]string, []admin.CleanDNSRecord, error) {
				workers, dns, err := runCleanCore(aCtx, cfg, st, dryRun)
				if err != nil {
					return nil, nil, err
				}
				if !dryRun {
					for _, w := range p.All() {
						p.Remove(w)
					}
				}
				return workers, dns, nil
			},
			ListWorkersFunc: func(aCtx context.Context) ([]admin.ListedWorker, error) {
				ws, err := worker.ListAll(aCtx, cfg)
				if err != nil {
					return nil, err
				}
				out := make([]admin.ListedWorker, 0, len(ws))
				for _, w := range ws {
					out = append(out, admin.ListedWorker{
						Name: w.Name, URL: w.URL, Account: w.AccountID,
						Backend: w.Backend, Hostname: w.Hostname,
						CreatedAt: w.CreatedAt.UTC().Format(time.RFC3339),
					})
				}
				return out, nil
			},
			ConfigDumpFunc: func() map[string]any {
				return sanitizedConfig(cfg)
			},
			RecycleWorkerFunc: func(aCtx context.Context, workerName string) (string, error) {
				return recycleNamedWorker(aCtx, cfg, p, st, workerName)
			},
			TestRequestFunc: func(aCtx context.Context, targetURL string) (admin.TestRequestResult, error) {
				return runTestRequest(aCtx, srv, targetURL)
			},
			SetProxyModeFunc: func(mode string) error {
				srv.SetProxyMode(mode)
				cfg.Pool.ProxyMode = mode
				logger.L.Info().Str("mode", mode).Msg("proxy mode switched at runtime")
				return nil
			},
			QuotaHistoryFunc: func(days int, accountID string) ([]any, error) {
				if st == nil {
					return nil, fmt.Errorf("state store unavailable")
				}
				all, err := st.ListQuotaHistory()
				if err != nil {
					return nil, err
				}
				cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
				out := make([]any, 0, len(all))
				for _, q := range all {
					if q.Date < cutoff {
						continue
					}
					if accountID != "" && q.AccountID != accountID {
						continue
					}
					out = append(out, q)
				}
				return out, nil
			},
			RemoveTokenFunc: func(aCtx context.Context, accountID, tok string) ([]string, error) {

				var targets []config.Account
				for _, a := range cfg.Accounts {
					match := false
					if accountID != "" && a.ID == accountID {
						match = true
					}
					if tok != "" && a.Token == tok {
						match = true
					}
					if match {
						targets = append(targets, a)
					}
				}
				if len(targets) == 0 {
					return nil, fmt.Errorf("no matching account in current config")
				}
				var removed []string
				for _, a := range targets {

					for _, w := range p.ByAccount(a.ID) {
						w.Healthy.Store(false)
					}

					singleAccCfg := *cfg
					singleAccCfg.Accounts = []config.Account{a}
					if err := worker.DestroyAll(aCtx, &singleAccCfg, st); err != nil {
						logger.L.Warn().Str("account", a.ID).Err(err).Msg("partial CF delete")
					}

					for _, w := range p.ByAccount(a.ID) {
						p.Remove(w)
						removed = append(removed, w.Name)
					}

					out := cfg.Accounts[:0]
					for _, x := range cfg.Accounts {
						if x.ID != a.ID || x.Token != a.Token {
							out = append(out, x)
						}
					}
					cfg.Accounts = out
				}
				return removed, nil
			},
		}
		// Session cookie signing key — derived from the HMAC secret +
		// "admin-session" so a config rotation invalidates all in-flight
		// sessions. Enables /session login + CSRF when admin.ui is on.
		if cfg.Admin.UI {
			adm.SessionSecret(deriveSessionSecret(cfg.Security.HMACSecret))
		}
		go func() {
			if err := adm.Serve(sigCtx); err != nil && err.Error() != "http: Server closed" {
				logger.L.Warn().Err(err).Msg("admin stopped")
			}
		}()
	}

	if cfg.Listen.HTTP != "" {
		hln, err := proxy.Listen(ctx, cfg.Listen.HTTP, cfg.Listen.UnixPerms)
		if err != nil {
			logger.L.Warn().Err(err).Str("addr", cfg.Listen.HTTP).Msg("http listener disabled")
		} else {
			go func() {
				<-sigCtx.Done()
				_ = hln.Close()
			}()
			go func() {
				if err := srv.ServeHTTP(sigCtx, hln); err != nil && sigCtx.Err() == nil {
					logger.L.Warn().Err(err).Msg("http frontend stopped")
				}
			}()
			logger.L.Info().Str("addr", cfg.Listen.HTTP).Msg("HTTP CONNECT frontend listening")
		}
	}

	serveErr := srv.Serve(sigCtx, ln)

	if destroyOnExit {
		logger.L.Info().Msg("destroy-on-exit: tearing down Workers")

		dctx, cancel := context.WithTimeout(context.Background(), cfg.ResolvedTimeouts.DestroyOnExit)
		defer cancel()
		if err := worker.DestroyAll(dctx, cfg, st); err != nil {
			logger.L.Warn().Err(err).Msg("destroy-on-exit: partial failure")
		}
	}
	// Listener close during SIGTERM is expected, not a fatal. Swallow the
	// canonical net.ErrClosed so the exit log is clean.
	if serveErr != nil && isExpectedShutdownErr(serveErr) {
		logger.L.Info().Msg("shutdown complete")
		return nil
	}
	return serveErr
}

// isExpectedShutdownErr matches listener-close errors raised by net.Listener
// when we deliberately Close() it during graceful shutdown. Prevents the
// top-level logger.Fatal() from printing "FTL fatal" on a clean exit.
func isExpectedShutdownErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "Server closed") ||
		strings.Contains(s, "context canceled")
}
