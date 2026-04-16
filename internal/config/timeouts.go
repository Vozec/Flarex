package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// TimeoutsConfig centralizes every tunable timeout/interval used across the
// project. Values support Go duration syntax ("15s", "500ms", "2m"). Env vars
// FLX_TIMEOUT_<NAME> override config values.
type TimeoutsConfig struct {
	Dial                string `koanf:"dial"`
	Probe               string `koanf:"probe"`
	ProbeFetch          string `koanf:"probe_fetch"`
	TLSHandshake        string `koanf:"tls_handshake"`
	IdleConn            string `koanf:"idle_conn"`
	H2ReadIdle          string `koanf:"h2_read_idle"`
	H2Ping              string `koanf:"h2_ping"`
	HealthCheckInterval string `koanf:"health_check_interval"`
	HealthCheckTimeout  string `koanf:"health_check_timeout"`
	DNSCacheTTL         string `koanf:"dns_cache_ttl"`
	CFBlockedCacheTTL   string `koanf:"cf_blocked_cache_ttl"`
	PrewarmAttempt      string `koanf:"prewarm_attempt"`
	PrewarmRetries      int    `koanf:"prewarm_retries"`
	BreakerInterval     string `koanf:"breaker_interval"`
	BreakerOpen         string `koanf:"breaker_open"`
	CFAPI               string `koanf:"cf_api"`
	DestroyOnExit       string `koanf:"destroy_on_exit"`
	AdminShutdown       string `koanf:"admin_shutdown"`
	AdminTokenOp        string `koanf:"admin_token_op"`
	DrainTimeout        string `koanf:"drain_timeout"`
	DrainPoll           string `koanf:"drain_poll"`
}

// Resolved = parsed durations. Populated via Resolve().
type Timeouts struct {
	Dial                time.Duration
	Probe               time.Duration
	ProbeFetch          time.Duration
	TLSHandshake        time.Duration
	IdleConn            time.Duration
	H2ReadIdle          time.Duration
	H2Ping              time.Duration
	HealthCheckInterval time.Duration
	HealthCheckTimeout  time.Duration
	DNSCacheTTL         time.Duration
	CFBlockedCacheTTL   time.Duration
	PrewarmAttempt      time.Duration
	PrewarmRetries      int
	BreakerInterval     time.Duration
	BreakerOpen         time.Duration
	CFAPI               time.Duration
	DestroyOnExit       time.Duration
	AdminShutdown       time.Duration
	AdminTokenOp        time.Duration
	DrainTimeout        time.Duration
	DrainPoll           time.Duration
}

func parseDur(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

func envDur(name string, fallback time.Duration) time.Duration {
	if v := os.Getenv("FLX_TIMEOUT_" + name); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return fallback
}

func envInt(name string, fallback int) int {
	if v := os.Getenv("FLX_TIMEOUT_" + name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return fallback
}

// Resolve turns the string-based TimeoutsConfig into a typed Timeouts struct,
// applying defaults for any empty/invalid value and then env overrides.
func (t TimeoutsConfig) Resolve() Timeouts {
	return Timeouts{
		Dial:                envDur("DIAL", parseDur(t.Dial, 15*time.Second)),
		Probe:               envDur("PROBE", parseDur(t.Probe, 800*time.Millisecond)),
		ProbeFetch:          envDur("PROBE_FETCH", parseDur(t.ProbeFetch, 1500*time.Millisecond)),
		TLSHandshake:        envDur("TLS_HANDSHAKE", parseDur(t.TLSHandshake, 10*time.Second)),
		IdleConn:            envDur("IDLE_CONN", parseDur(t.IdleConn, 120*time.Second)),
		H2ReadIdle:          envDur("H2_READ_IDLE", parseDur(t.H2ReadIdle, 30*time.Second)),
		H2Ping:              envDur("H2_PING", parseDur(t.H2Ping, 10*time.Second)),
		HealthCheckInterval: envDur("HEALTH_CHECK_INTERVAL", parseDur(t.HealthCheckInterval, 30*time.Second)),
		HealthCheckTimeout:  envDur("HEALTH_CHECK_TIMEOUT", parseDur(t.HealthCheckTimeout, 5*time.Second)),
		DNSCacheTTL:         envDur("DNS_CACHE_TTL", parseDur(t.DNSCacheTTL, 5*time.Minute)),
		CFBlockedCacheTTL:   envDur("CF_BLOCKED_CACHE_TTL", parseDur(t.CFBlockedCacheTTL, 10*time.Minute)),
		PrewarmAttempt:      envDur("PREWARM_ATTEMPT", parseDur(t.PrewarmAttempt, 3*time.Second)),
		PrewarmRetries:      envInt("PREWARM_RETRIES", defaultInt(t.PrewarmRetries, 5)),
		BreakerInterval:     envDur("BREAKER_INTERVAL", parseDur(t.BreakerInterval, 60*time.Second)),
		BreakerOpen:         envDur("BREAKER_OPEN", parseDur(t.BreakerOpen, 30*time.Second)),
		CFAPI:               envDur("CF_API", parseDur(t.CFAPI, 30*time.Second)),
		DestroyOnExit:       envDur("DESTROY_ON_EXIT", parseDur(t.DestroyOnExit, 60*time.Second)),
		AdminShutdown:       envDur("ADMIN_SHUTDOWN", parseDur(t.AdminShutdown, 2*time.Second)),
		AdminTokenOp:        envDur("ADMIN_TOKEN_OP", parseDur(t.AdminTokenOp, 120*time.Second)),
		DrainTimeout:        envDur("DRAIN_TIMEOUT", parseDur(t.DrainTimeout, 30*time.Second)),
		DrainPoll:           envDur("DRAIN_POLL", parseDur(t.DrainPoll, 100*time.Millisecond)),
	}
}

func defaultInt(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

// String returns a concise dump for logging.
func (t Timeouts) String() string {
	return fmt.Sprintf("dial=%s probe=%s probe_fetch=%s tls=%s idle_conn=%s h2_read_idle=%s health=%s dns_ttl=%s cf_block_ttl=%s breaker_open=%s",
		t.Dial, t.Probe, t.ProbeFetch, t.TLSHandshake, t.IdleConn, t.H2ReadIdle, t.HealthCheckInterval, t.DNSCacheTTL, t.CFBlockedCacheTTL, t.BreakerOpen)
}
