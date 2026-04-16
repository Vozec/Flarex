package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/Vozec/flarex/internal/config"
	"github.com/Vozec/flarex/internal/filter"
	"github.com/Vozec/flarex/internal/logger"
	"github.com/Vozec/flarex/internal/proxy"
)

// applyConfigUpdate is the registry + dispatcher for admin-UI runtime
// config edits. Called from AdminServer.UpdateConfigFunc.
//
// Each case:
//   1. Coerces the JSON value into the expected Go type. Type mismatches
//      return an error so the UI toast can explain.
//   2. Writes the new value onto cfg (source of truth — a `flarex backup`
//      and restart picks up the same value next boot).
//   3. For fields we can swap live (proxy mode, allow_ports, auth creds,
//      admin api_key, rate limits), also re-applies the change to the
//      running components via srv / rl.
//   4. Returns (applied, requiresRestart, err). requiresRestart=true means
//      the mutation is on the in-memory cfg only — the running listener /
//      pool won't honor it until the user restarts the process.
func applyConfigUpdate(
	cfg *config.Config,
	srv *proxy.Server,
	filt *filter.IPFilter,
	path string,
	value any,
) (bool, bool, error) {
	switch path {
	// ---- Worker ------------------------------------------------------
	case "worker.count":
		n, err := asInt(value)
		if err != nil {
			return false, false, err
		}
		if n < 0 || n > 500 {
			return false, false, fmt.Errorf("count must be 0–500")
		}
		cfg.Worker.Count = n
		return true, false, nil // affects next deploy — existing pool unchanged
	case "worker.name_prefix":
		s, err := asString(value)
		if err != nil {
			return false, false, err
		}
		if s == "" {
			return false, false, fmt.Errorf("name_prefix must not be empty (guards `clean` against deleting every Worker)")
		}
		cfg.Worker.NamePrefix = s
		cfg.Worker.NamePrefixDefaulted = false
		return true, false, nil
	case "worker.deploy_backend":
		s, err := asString(value)
		if err != nil {
			return false, false, err
		}
		switch s {
		case "workers_dev", "custom_domain", "auto":
		default:
			return false, false, fmt.Errorf("deploy_backend must be one of: workers_dev, custom_domain, auto")
		}
		cfg.Worker.DeployBackend = s
		return true, false, nil
	case "worker.rotate_interval_sec":
		n, err := asInt(value)
		if err != nil {
			return false, false, err
		}
		cfg.Worker.RotateIntervalS = n
		return true, true, nil
	case "worker.rotate_max_age_sec":
		n, err := asInt(value)
		if err != nil {
			return false, false, err
		}
		cfg.Worker.RotateMaxAgeSec = n
		return true, true, nil
	case "worker.rotate_max_req":
		n, err := asUint64(value)
		if err != nil {
			return false, false, err
		}
		cfg.Worker.RotateMaxReq = n
		return true, true, nil

	// ---- Listener auth (live — Auth struct reads per-conn via cfg) ----
	case "listen.auth_user":
		s, _ := asString(value)
		cfg.Listen.AuthUser = s
		return true, true, nil // rebuild of proxy.Auth at restart needed
	case "listen.auth_pass":
		s, _ := asString(value)
		cfg.Listen.AuthPass = s
		return true, true, nil

	// ---- Admin (applied live) ----------------------------------------
	case "admin.api_key":
		s, _ := asString(value)
		cfg.Admin.APIKey = s
		// Note: the admin.Server we built at boot captured cfg.Admin.APIKey
		// by value. Live swap would need a pointer. For now: flag restart
		// required so the user knows to bounce.
		return true, true, nil
	case "admin.enable_pprof":
		b, err := asBool(value)
		if err != nil {
			return false, false, err
		}
		cfg.Admin.EnablePprof = b
		return true, true, nil

	// ---- Filter (live — read per dial) -------------------------------
	case "filter.allow_ports":
		ports, err := asIntSlice(value)
		if err != nil {
			return false, false, err
		}
		wire := make([]any, len(ports))
		for i, p := range ports {
			wire[i] = p
		}
		cfg.Filter.AllowPorts = wire
		if filt != nil {
			if err := filt.SetAllowPorts(wire); err != nil {
				return false, false, err
			}
		}
		return true, false, nil
	case "filter.deny_cidrs":
		s, err := asStringSlice(value)
		if err != nil {
			return false, false, err
		}
		cfg.Filter.DenyCIDRs = s
		return true, true, nil // filter.IPFilter is rebuilt at boot

	// ---- Pool (mostly live) ------------------------------------------
	case "pool.proxy_mode":
		s, _ := asString(value)
		switch s {
		case "socket", "fetch", "hybrid":
		default:
			return false, false, fmt.Errorf("proxy_mode must be socket|fetch|hybrid")
		}
		cfg.Pool.ProxyMode = s
		if srv != nil {
			srv.SetProxyMode(s)
		}
		return true, false, nil
	case "pool.strategy":
		s, _ := asString(value)
		switch s {
		case "round_robin", "least_inflight":
		default:
			return false, false, fmt.Errorf("strategy must be round_robin|least_inflight")
		}
		cfg.Pool.Strategy = s
		return true, true, nil // scheduler is built at boot
	case "pool.max_retries":
		n, err := asInt(value)
		if err != nil {
			return false, false, err
		}
		cfg.Pool.MaxRetries = n
		if srv != nil {
			srv.MaxRetries = n
		}
		return true, false, nil
	case "pool.backoff_ms":
		n, err := asInt(value)
		if err != nil {
			return false, false, err
		}
		cfg.Pool.BackoffMs = n
		if srv != nil {
			srv.BaseBackoff = time.Duration(n) * time.Millisecond
		}
		return true, false, nil
	case "pool.hedge_after_ms":
		n, err := asInt(value)
		if err != nil {
			return false, false, err
		}
		cfg.Pool.HedgeAfterMs = n
		if srv != nil {
			srv.HedgeAfter = time.Duration(n) * time.Millisecond
		}
		return true, false, nil
	case "pool.tls_rewrap":
		b, err := asBool(value)
		if err != nil {
			return false, false, err
		}
		cfg.Pool.TLSRewrap = b
		if srv != nil {
			srv.TLSRewrap = b
		}
		return true, false, nil
	case "pool.disable_probe":
		b, err := asBool(value)
		if err != nil {
			return false, false, err
		}
		cfg.Pool.DisableProbe = b
		return true, true, nil

	// ---- Rate limit --------------------------------------------------
	case "rate_limit.per_host_qps":
		f, err := asFloat(value)
		if err != nil {
			return false, false, err
		}
		cfg.RateLimit.PerHostQPS = f
		return true, true, nil // ratelimit.PerHost is rebuilt at boot
	case "rate_limit.per_host_burst":
		n, err := asInt(value)
		if err != nil {
			return false, false, err
		}
		cfg.RateLimit.PerHostBurst = n
		return true, true, nil

	// ---- Quota -------------------------------------------------------
	case "quota.daily_limit":
		n, err := asUint64(value)
		if err != nil {
			return false, false, err
		}
		cfg.Quota.DailyLimit = n
		return true, true, nil // used at boot for per-account quota init
	case "quota.warn_percent":
		n, err := asInt(value)
		if err != nil {
			return false, false, err
		}
		if n < 0 || n > 100 {
			return false, false, fmt.Errorf("warn_percent must be 0–100")
		}
		cfg.Quota.WarnPercent = n
		return true, false, nil

	// ---- Alerts ------------------------------------------------------
	case "alerts.cooldown_sec":
		n, err := asInt(value)
		if err != nil {
			return false, false, err
		}
		cfg.Alerts.CooldownSec = n
		return true, true, nil
	case "alerts.discord_webhook_url":
		s, _ := asString(value)
		if s != "" && !strings.HasPrefix(s, "https://") {
			return false, false, fmt.Errorf("discord_webhook_url must start with https://")
		}
		cfg.Alerts.DiscordURL = s
		return true, true, nil

	// ---- Log ---------------------------------------------------------
	case "log.level":
		s, _ := asString(value)
		switch s {
		case "trace", "debug", "info", "warn", "error":
		default:
			return false, false, fmt.Errorf("log.level must be trace|debug|info|warn|error")
		}
		cfg.Log.Level = s
		return true, true, nil

	default:
		return false, false, fmt.Errorf("unknown or read-only path: %s", path)
	}
}

// --- coercion helpers — JSON unmarshalled values come as json.Number or
// interface{}; these flatten the usual cases into Go primitives with a
// single shape of error for the UI.

func asInt(v any) (int, error) {
	switch x := v.(type) {
	case float64:
		return int(x), nil
	case int:
		return x, nil
	case int64:
		return int(x), nil
	case string:
		var n int
		_, err := fmt.Sscanf(x, "%d", &n)
		if err != nil {
			return 0, fmt.Errorf("expected integer, got %q", x)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("expected integer, got %T", v)
	}
}

func asUint64(v any) (uint64, error) {
	switch x := v.(type) {
	case float64:
		if x < 0 {
			return 0, fmt.Errorf("value must be >= 0")
		}
		return uint64(x), nil
	case string:
		var n uint64
		_, err := fmt.Sscanf(x, "%d", &n)
		if err != nil {
			return 0, fmt.Errorf("expected non-negative integer, got %q", x)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("expected number, got %T", v)
	}
}

func asFloat(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	case string:
		var f float64
		_, err := fmt.Sscanf(x, "%f", &f)
		if err != nil {
			return 0, fmt.Errorf("expected number, got %q", x)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("expected number, got %T", v)
	}
}

func asString(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("expected string, got %T", v)
	}
	return s, nil
}

func asBool(v any) (bool, error) {
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("expected bool, got %T", v)
	}
	return b, nil
}

func asIntSlice(v any) ([]int, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array, got %T", v)
	}
	out := make([]int, 0, len(arr))
	for i, x := range arr {
		n, err := asInt(x)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", i, err)
		}
		out = append(out, n)
	}
	return out, nil
}

func asStringSlice(v any) ([]string, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array, got %T", v)
	}
	out := make([]string, 0, len(arr))
	for i, x := range arr {
		s, err := asString(x)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", i, err)
		}
		out = append(out, s)
	}
	return out, nil
}

// configLogApplied centralizes the log line for applied changes — keeps
// main.go free of the ad-hoc logger.L calls that would otherwise clutter
// the closure.
func configLogApplied(path string, restart bool) {
	ev := logger.L.Info().Str("path", path)
	if restart {
		ev = ev.Bool("requires_restart", true)
	}
	ev.Msg("config.update applied via admin UI")
}
