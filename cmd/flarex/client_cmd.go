package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/Vozec/flarex/internal/clientcli"
	"github.com/Vozec/flarex/internal/logger"
	"github.com/urfave/cli/v3"
)

func clientCmd() *cli.Command {
	return &cli.Command{
		Name:  "client",
		Usage: "remote admin client (login + status + token mgmt)",
		Commands: []*cli.Command{
			{
				Name:  "login",
				Usage: "save server URL + credentials to ~/.config/flarex/client.yaml",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "url", Required: true, Usage: "admin URL, e.g. http://server:9090"},
					&cli.StringFlag{Name: "api-key", Usage: "API key (sent as X-API-Key)"},
					&cli.StringFlag{Name: "bearer", Usage: "Bearer token"},
					&cli.StringFlag{Name: "user", Usage: "Basic auth user"},
					&cli.StringFlag{Name: "pass", Usage: "Basic auth password"},
				},
				Action: runClientLogin,
			},
			{
				Name:   "logout",
				Usage:  "remove the persisted client config",
				Action: runClientLogout,
			},
			{
				Name:   "whoami",
				Usage:  "print active server URL + auth method",
				Action: runClientWhoami,
			},
			{
				Name:  "status",
				Usage: "GET /status (worker pool snapshot)",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "json", Usage: "emit raw JSON instead of a table"},
					&cli.StringFlag{Name: "account", Usage: "filter rows to this account ID"},
				},
				Action: runClientStatus,
			},
			{
				Name:   "metrics",
				Usage:  "GET /metrics (Prometheus format)",
				Action: runClientMetrics,
			},
			{
				Name:  "metrics-history",
				Usage: "GET /metrics/history — daily quota snapshots",
				Flags: []cli.Flag{
					&cli.IntFlag{Name: "days", Value: 7, Usage: "number of days back (1-365)"},
					&cli.StringFlag{Name: "account", Usage: "filter by account ID"},
					&cli.BoolFlag{Name: "json", Usage: "emit raw JSON"},
				},
				Action: runClientMetricsHistory,
			},
			{
				Name:   "health",
				Usage:  "GET /health",
				Action: runClientHealth,
			},
			{
				Name:  "add-token",
				Usage: "POST /tokens — discover account + deploy N Workers",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "token", Required: true, Usage: "CF API token"},
					&cli.IntFlag{Name: "count", Usage: "override server's worker.count for this deploy"},
				},
				Action: runClientAddToken,
			},
			{
				Name:  "logs",
				Usage: "stream live Worker logs (CF tail) — pretty-print one event per line",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Required: true, Usage: "Worker name (see `client status`)"},
					&cli.BoolFlag{Name: "raw", Usage: "emit raw SSE lines instead of pretty JSON"},
				},
				Action: runClientLogs,
			},
			{
				Name:  "remove-token",
				Usage: "DELETE /tokens?account=<id> OR ?token=<...>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "account"},
					&cli.StringFlag{Name: "token"},
				},
				Action: runClientRemoveToken,
			},
			{
				Name:  "list",
				Usage: "GET /workers — full worker inventory (backend + hostname + url)",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "json", Usage: "emit raw JSON instead of a table"},
				},
				Action: runClientList,
			},
			{
				Name:  "deploy",
				Usage: "POST /workers/deploy — deploy N more workers on an existing account",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "account", Required: true, Usage: "account ID (see `client status`)"},
					&cli.IntFlag{Name: "count", Usage: "override server's worker.count"},
				},
				Action: runClientDeploy,
			},
			{
				Name:  "destroy",
				Usage: "DELETE /workers?confirm=true — destroy every worker across all accounts",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "yes", Usage: "skip the interactive confirmation prompt"},
				},
				Action: runClientDestroy,
			},
			{
				Name:  "clean",
				Usage: "POST /workers/clean — prefix-scoped destroy (workers + DNS records)",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "dry-run", Usage: "list targets without deleting"},
					&cli.BoolFlag{Name: "yes", Usage: "skip the interactive confirmation prompt"},
				},
				Action: runClientClean,
			},
			{
				Name:  "recycle",
				Usage: "POST /workers/{name}/recycle — graceful drain + redeploy",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Required: true, Usage: "worker name to recycle"},
				},
				Action: runClientRecycle,
			},
		},
	}
}

func loadClient() (*clientcli.Client, error) {
	cfg, err := clientcli.Load()
	if err != nil {
		return nil, err
	}
	return clientcli.New(cfg), nil
}

func runClientLogin(_ context.Context, c *cli.Command) error {
	cfg := &clientcli.ConfigFile{
		URL:    c.String("url"),
		APIKey: c.String("api-key"),
		Bearer: c.String("bearer"),
		BasicU: c.String("user"),
		BasicP: c.String("pass"),
	}
	if err := clientcli.Save(cfg); err != nil {
		return err
	}
	logger.L.Info().Str("url", cfg.URL).Str("file", clientcli.DefaultPath()).Msg("client logged in")
	return nil
}

func runClientLogout(_ context.Context, _ *cli.Command) error {
	path := clientcli.DefaultPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	logger.L.Info().Str("file", path).Msg("client logged out")
	return nil
}

func runClientWhoami(_ context.Context, _ *cli.Command) error {
	cfg, err := clientcli.Load()
	if err != nil {
		return err
	}
	method := "none"
	switch {
	case cfg.APIKey != "":
		method = "api-key"
	case cfg.Bearer != "":
		method = "bearer"
	case cfg.BasicU != "":
		method = "basic"
	}
	fmt.Printf("url:    %s\nauth:   %s\nfile:   %s\n", cfg.URL, method, clientcli.DefaultPath())
	return nil
}

type statusWorker struct {
	Name        string  `json:"name"`
	URL         string  `json:"url"`
	Account     string  `json:"account"`
	Healthy     bool    `json:"healthy"`
	QuotaPaused bool    `json:"quota_paused"`
	Breaker     string  `json:"breaker"`
	Inflight    int64   `json:"inflight"`
	Requests    uint64  `json:"requests"`
	Errors      uint64  `json:"errors"`
	ErrRate     float64 `json:"err_rate_ewma"`
	AgeSec      int64   `json:"age_sec"`
	Colo        string  `json:"colo,omitempty"`
}

type statusResp struct {
	PoolSize int            `json:"pool_size"`
	Workers  []statusWorker `json:"workers"`
}

func runClientStatus(ctx context.Context, c *cli.Command) error {
	cli, err := loadClient()
	if err != nil {
		return err
	}
	if c.Bool("json") {
		var out any
		if err := cli.GetJSON(ctx, "/status", &out); err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	var st statusResp
	if err := cli.GetJSON(ctx, "/status", &st); err != nil {
		return err
	}
	if acct := c.String("account"); acct != "" {
		filtered := st.Workers[:0]
		for _, w := range st.Workers {
			if w.Account == acct {
				filtered = append(filtered, w)
			}
		}
		st.Workers = filtered
	}
	if len(st.Workers) == 0 {
		fmt.Println("pool empty")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tHEALTH\tBREAKER\tCOLO\tINFLIGHT\tREQ\tERR\tERR%\tAGE")
	fmt.Fprintln(tw, "----\t------\t-------\t----\t--------\t---\t---\t----\t---")
	for _, w := range st.Workers {
		health := "ok"
		if w.QuotaPaused {
			health = "quota"
		} else if !w.Healthy {
			health = "down"
		}
		colo := w.Colo
		if colo == "" {
			colo = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%.1f%%\t%s\n",
			w.Name, health, w.Breaker, colo,
			w.Inflight, w.Requests, w.Errors,
			w.ErrRate*100, fmtAge(w.AgeSec),
		)
	}
	_ = tw.Flush()
	fmt.Printf("\npool_size=%d\n", st.PoolSize)
	return nil
}

// fmtAge renders seconds as "5s" / "12m" / "3h14m" — short form for tables.
func fmtAge(sec int64) string {
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%dm", sec/60)
	}
	h := sec / 3600
	m := (sec % 3600) / 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%02dm", h, m)
}

func runClientMetrics(ctx context.Context, _ *cli.Command) error {
	cli, err := loadClient()
	if err != nil {
		return err
	}
	raw, err := cli.GetRaw(ctx, "/metrics")
	if err != nil {
		return err
	}
	os.Stdout.Write(raw)
	return nil
}

type quotaDay struct {
	Date      string `json:"date"`
	AccountID string `json:"account_id"`
	Used      uint64 `json:"used"`
	Limit     uint64 `json:"limit"`
}

type quotaHistResp struct {
	Days    int        `json:"days"`
	Account string     `json:"account"`
	Series  []quotaDay `json:"series"`
}

func runClientMetricsHistory(ctx context.Context, c *cli.Command) error {
	cli, err := loadClient()
	if err != nil {
		return err
	}
	days := c.Int("days")
	if days <= 0 {
		days = 7
	}
	acct := c.String("account")
	path := fmt.Sprintf("/metrics/history?days=%d", days)
	if acct != "" {
		path += "&account=" + acct
	}
	if c.Bool("json") {
		raw, err := cli.GetRaw(ctx, path)
		if err != nil {
			return err
		}
		os.Stdout.Write(raw)
		return nil
	}
	var resp quotaHistResp
	if err := cli.GetJSON(ctx, path, &resp); err != nil {
		return err
	}
	if len(resp.Series) == 0 {
		fmt.Printf("no quota history yet (days=%d account=%s) — snapshots accumulate every 10 min\n", days, acct)
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "DATE\tACCOUNT\tUSED\tLIMIT\tPCT")
	fmt.Fprintln(tw, "----\t-------\t----\t-----\t---")
	// Group by date so the TOTAL rolls up per-day across accounts.
	byDate := map[string]struct{ used, limit uint64 }{}
	for _, q := range resp.Series {
		acc := q.AccountID
		if len(acc) > 12 {
			acc = acc[:8] + "…"
		}
		pct := 0.0
		if q.Limit > 0 {
			pct = float64(q.Used) / float64(q.Limit) * 100
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%.1f%%\n", q.Date, acc, q.Used, q.Limit, pct)
		s := byDate[q.Date]
		s.used += q.Used
		s.limit += q.Limit
		byDate[q.Date] = s
	}
	// Totals row per date when there's more than one account per day.
	if acct == "" && len(resp.Series) > 0 {
		fmt.Fprintln(tw, "----\t-------\t----\t-----\t---")
		dates := make([]string, 0, len(byDate))
		for d := range byDate {
			dates = append(dates, d)
		}
		sort.Strings(dates)
		for _, d := range dates {
			s := byDate[d]
			pct := 0.0
			if s.limit > 0 {
				pct = float64(s.used) / float64(s.limit) * 100
			}
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%.1f%%\n", d, "TOTAL", s.used, s.limit, pct)
		}
	}
	_ = tw.Flush()
	return nil
}

func runClientLogs(ctx context.Context, c *cli.Command) error {
	cli, err := loadClient()
	if err != nil {
		return err
	}
	name := c.String("name")
	resp, err := cli.Do(ctx, http.MethodGet, "/workers/"+name+"/logs", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	raw := c.Bool("raw")
	br := bufio.NewReader(resp.Body)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if raw {
			fmt.Print(line)
			continue
		}
		line = strings.TrimSpace(line)
		// SSE lines: "data: { ... }". Skip empties + "event:"/"id:" lines.
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var ev struct {
			Outcome string   `json:"outcome"`
			Logs    []any    `json:"logs"`
			Level   string   `json:"level"`
			Event   struct {
				Request struct {
					URL    string `json:"url"`
					Method string `json:"method"`
				} `json:"request"`
			} `json:"event"`
			Exceptions []struct {
				Message string `json:"message"`
			} `json:"exceptions"`
		}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			fmt.Println(payload)
			continue
		}
		marker := "[" + ev.Outcome + "]"
		if len(ev.Exceptions) > 0 {
			fmt.Printf("%s  exception: %s\n", marker, ev.Exceptions[0].Message)
			continue
		}
		if ev.Event.Request.URL != "" {
			fmt.Printf("%s  %s %s\n", marker, ev.Event.Request.Method, ev.Event.Request.URL)
			continue
		}
		fmt.Println(payload)
	}
}

func runClientHealth(ctx context.Context, _ *cli.Command) error {
	cli, err := loadClient()
	if err != nil {
		return err
	}
	raw, err := cli.GetRaw(ctx, "/health")
	if err != nil {
		return err
	}
	fmt.Println(string(raw))
	return nil
}

func runClientAddToken(ctx context.Context, c *cli.Command) error {
	cli, err := loadClient()
	if err != nil {
		return err
	}
	tok := c.String("token")
	body := map[string]any{"token": tok}
	if n := c.Int("count"); n > 0 {
		body["count"] = n
	}
	resp, err := cli.Do(ctx, http.MethodPost, "/tokens", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	var out any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		_ = enc.Encode(out)
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return enc.Encode(out)
}

type listedWorker struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Account   string `json:"account"`
	Backend   string `json:"backend,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

type listResp struct {
	Workers []listedWorker `json:"workers"`
}

func runClientList(ctx context.Context, c *cli.Command) error {
	cli, err := loadClient()
	if err != nil {
		return err
	}
	if c.Bool("json") {
		raw, err := cli.GetRaw(ctx, "/workers")
		if err != nil {
			return err
		}
		os.Stdout.Write(raw)
		return nil
	}
	var resp listResp
	if err := cli.GetJSON(ctx, "/workers", &resp); err != nil {
		return err
	}
	if len(resp.Workers) == 0 {
		fmt.Println("no workers deployed")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tBACKEND\tACCOUNT\tURL")
	fmt.Fprintln(tw, "----\t-------\t-------\t---")
	for _, w := range resp.Workers {
		acc := w.Account
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
	fmt.Printf("\n-- %d worker(s)\n", len(resp.Workers))
	return nil
}

func runClientDeploy(ctx context.Context, c *cli.Command) error {
	cli, err := loadClient()
	if err != nil {
		return err
	}
	body := map[string]any{"account": c.String("account")}
	if n := c.Int("count"); n > 0 {
		body["count"] = n
	}
	return doJSONOp(ctx, cli, http.MethodPost, "/workers/deploy", body)
}

func runClientDestroy(ctx context.Context, c *cli.Command) error {
	cli, err := loadClient()
	if err != nil {
		return err
	}
	if !c.Bool("yes") {
		fmt.Fprint(os.Stderr, "This will destroy EVERY worker across EVERY account on the remote server.\nType 'yes' to continue: ")
		var answer string
		_, _ = fmt.Fscanln(os.Stdin, &answer)
		if strings.TrimSpace(strings.ToLower(answer)) != "yes" {
			return fmt.Errorf("aborted")
		}
	}
	return doJSONOp(ctx, cli, http.MethodDelete, "/workers?confirm=true", nil)
}

func runClientClean(ctx context.Context, c *cli.Command) error {
	cli, err := loadClient()
	if err != nil {
		return err
	}
	dryRun := c.Bool("dry-run")
	if !dryRun && !c.Bool("yes") {
		fmt.Fprint(os.Stderr, "This will DELETE every worker + matching DNS record for the configured prefix.\nType 'yes' to continue: ")
		var answer string
		_, _ = fmt.Fscanln(os.Stdin, &answer)
		if strings.TrimSpace(strings.ToLower(answer)) != "yes" {
			return fmt.Errorf("aborted")
		}
	}
	body := map[string]any{"dry_run": dryRun}
	resp, err := cli.Do(ctx, http.MethodPost, "/workers/clean", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out struct {
		DryRun  bool     `json:"dry_run"`
		Workers []string `json:"workers"`
		DNS     []struct {
			Zone string `json:"zone"`
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"dns_records"`
		Error string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, out.Error)
	}
	heading := "DELETED"
	if out.DryRun {
		heading = "[DRY-RUN] WOULD DELETE"
	}
	if len(out.Workers) == 0 {
		fmt.Printf("\n%s workers: none\n", heading)
	} else {
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(tw, "\n%s WORKERS (%d)\nNAME\n----\n", heading, len(out.Workers))
		for _, n := range out.Workers {
			fmt.Fprintln(tw, n)
		}
		_ = tw.Flush()
	}
	if len(out.DNS) == 0 {
		fmt.Printf("\n%s DNS records: none\n", heading)
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "\n%s DNS (%d)\nZONE\tTYPE\tNAME\n----\t----\t----\n", heading, len(out.DNS))
	for _, r := range out.DNS {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Zone, r.Type, r.Name)
	}
	_ = tw.Flush()
	return nil
}

func runClientRecycle(ctx context.Context, c *cli.Command) error {
	cli, err := loadClient()
	if err != nil {
		return err
	}
	name := c.String("name")
	return doJSONOp(ctx, cli, http.MethodPost, "/workers/"+name+"/recycle", nil)
}

// doJSONOp issues a JSON request, pretty-prints the decoded body, and
// returns a non-nil error on HTTP >= 300. Used for the simple
// deploy/destroy/recycle commands that share the same response shape.
func doJSONOp(ctx context.Context, cli *clientcli.Client, method, path string, body any) error {
	resp, err := cli.Do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func runClientRemoveToken(ctx context.Context, c *cli.Command) error {
	cli, err := loadClient()
	if err != nil {
		return err
	}
	q := ""
	if v := c.String("account"); v != "" {
		q = "?account=" + v
	} else if v := c.String("token"); v != "" {
		q = "?token=" + v
	} else {
		return fmt.Errorf("--account or --token required")
	}
	resp, err := cli.Do(ctx, http.MethodDelete, "/tokens"+q, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	var out any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		_ = enc.Encode(out)
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return enc.Encode(out)
}
