package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	Log       LogConfig       `koanf:"log"`
	Listen    ListenConfig    `koanf:"listen"`
	Tokens    []string        `koanf:"tokens"`
	Accounts  []Account       `koanf:"accounts"`
	Worker    WorkerConfig    `koanf:"worker"`
	Filter    FilterConfig    `koanf:"filter"`
	Pool      PoolConfig      `koanf:"pool"`
	Security  SecurityConfig  `koanf:"security"`
	RateLimit RateLimitConfig `koanf:"rate_limit"`
	Admin     AdminConfig     `koanf:"admin"`
	State     StateConfig     `koanf:"state"`
	Quota     QuotaConfig     `koanf:"quota"`
	Alerts    AlertsConfig    `koanf:"alerts"`
	Tracing   TracingConfig   `koanf:"tracing"`
	Timeouts  TimeoutsConfig  `koanf:"timeouts"`

	// ResolvedTimeouts is populated by Load() after applying defaults + env.
	ResolvedTimeouts Timeouts `koanf:"-"`
}

type LogConfig struct {
	Level string `koanf:"level"`
	JSON  bool   `koanf:"json"`
}

type ListenConfig struct {
	Socks5    string `koanf:"socks5"`
	HTTP      string `koanf:"http"`
	AuthUser  string `koanf:"auth_user"`
	AuthPass  string `koanf:"auth_pass"`
	UnixPerms uint32 `koanf:"unix_perms"`
}

type Account struct {
	ID        string `koanf:"id"`
	Name      string `koanf:"name"`
	Token     string `koanf:"token"`
	Subdomain string `koanf:"subdomain"`
}

type WorkerConfig struct {
	NamePrefix      string `koanf:"name_prefix"`
	Count           int    `koanf:"count"`
	DeployTimeout   int    `koanf:"deploy_timeout"`
	TemplatePath    string `koanf:"template_path"`
	RotateIntervalS int    `koanf:"rotate_interval_sec"`
	RotateMaxAgeSec int    `koanf:"rotate_max_age_sec"`
	RotateMaxReq    uint64 `koanf:"rotate_max_req"`

	DeployBackend string `koanf:"deploy_backend"`

	// PreferIPv4 tells the Worker template to prefer IPv4 over IPv6 when
	// resolving targets. Useful when a target's AAAA record is broken or
	// when you want consistent IPv4 egress (some legacy anti-bot systems
	// block IPv6 by default).
	PreferIPv4 bool `koanf:"prefer_ipv4"`

	// NamePrefixDefaulted is set true by applyDefaults when the user left
	// name_prefix empty in YAML. Used by destructive commands (clean) to
	// refuse running with a silently-defaulted prefix — otherwise an empty
	// user value would secretly match "flarex-" and delete every worker
	// on the account.
	NamePrefixDefaulted bool `koanf:"-"`
}

type FilterConfig struct {
	DenyCIDRs  []string `koanf:"deny_cidrs"`
	AllowPorts []any    `koanf:"allow_ports"`
}

type RateLimitConfig struct {
	PerHostQPS   float64 `koanf:"per_host_qps"`
	PerHostBurst int     `koanf:"per_host_burst"`
}

type PoolConfig struct {
	Strategy      string `koanf:"strategy"`
	MaxRetries    int    `koanf:"max_retries"`
	BackoffMs     int    `koanf:"backoff_ms"`
	HedgeAfterMs  int    `koanf:"hedge_after_ms"`
	GoroutineSize int    `koanf:"goroutine_size"`
	ProxyMode     string `koanf:"proxy_mode"`    // socket | fetch | hybrid
	DisableProbe  bool   `koanf:"disable_probe"` // max throughput, lose CF auto-fallback
	TLSRewrap     bool   `koanf:"tls_rewrap"`    // proxy terminates TLS with uTLS (random JA3); client speaks plaintext
}

type SecurityConfig struct {
	HMACSecret string `koanf:"hmac_secret"`
}

type AdminConfig struct {
	Addr        string `koanf:"addr"`
	Token       string `koanf:"token"`
	APIKey      string `koanf:"api_key"`
	BasicUser   string `koanf:"basic_user"`
	BasicPass   string `koanf:"basic_pass"`
	EnablePprof bool   `koanf:"enable_pprof"`
	// UI gates the full React admin dashboard + its supporting endpoints
	// (/ui/*, /apikeys, /accounts/{id}/pause|resume, /metrics/series).
	// Default false = legacy single-file dashboard only.
	UI bool `koanf:"ui"`

	// TOTPSecret (optional) is a base32-encoded TOTP shared secret. When
	// set, POST /session additionally requires a 6-digit code. Pair with
	// `oathtool --totp -b $FLX_ADMIN_TOTP_SECRET` or Google Authenticator
	// (scan "otpauth://totp/FlareX?secret=<secret>&issuer=FlareX"). Leaving
	// it empty keeps 2FA off — the bootstrap key alone is sufficient.
	TOTPSecret string `koanf:"totp_secret"`
}

type StateConfig struct {
	Path string `koanf:"path"`
}

type QuotaConfig struct {
	DailyLimit         uint64 `koanf:"daily_limit"`
	WarnPercent        int    `koanf:"warn_percent"`
	SeedFromCloudflare bool   `koanf:"seed_from_cloudflare"`
}

type TracingConfig struct {
	Endpoint string `koanf:"endpoint"` // host:port, e.g. "localhost:4317"; empty = disabled
	Insecure bool   `koanf:"insecure"` // plaintext gRPC (no TLS)
}

type AlertsConfig struct {
	CooldownSec  int           `koanf:"cooldown_sec"`
	HTTPWebhooks []HTTPWebhook `koanf:"http_webhooks"`
	DiscordURL   string        `koanf:"discord_webhook_url"`
	DiscordName  string        `koanf:"discord_username"`
}

type HTTPWebhook struct {
	URL     string            `koanf:"url"`
	Headers map[string]string `koanf:"headers"`
}

func Load(path string) (*Config, error) {
	k := koanf.New(".")
	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return nil, fmt.Errorf("load config %s: %w", path, err)
	}
	c := &Config{}
	if err := k.Unmarshal("", c); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	c.applyEnvOverrides()
	c.applyDefaults()
	c.ResolvedTimeouts = c.Timeouts.Resolve()
	return c, c.validate()
}

func (c *Config) applyDefaults() {
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Listen.Socks5 == "" {
		c.Listen.Socks5 = "tcp://127.0.0.1:1080"
	}
	if c.Listen.UnixPerms == 0 {
		c.Listen.UnixPerms = 0o600
	}
	if c.Worker.NamePrefix == "" {
		c.Worker.NamePrefix = "flarex-"
		c.Worker.NamePrefixDefaulted = true
	}
	if c.Worker.DeployBackend == "" {
		c.Worker.DeployBackend = "auto"
	}
	if c.Worker.Count == 0 {
		c.Worker.Count = 10
	}
	if c.Worker.DeployTimeout == 0 {
		c.Worker.DeployTimeout = 30
	}
	if len(c.Filter.AllowPorts) == 0 {
		c.Filter.AllowPorts = []any{80, 443, 8080, 8443}
	}
	if c.Pool.Strategy == "" {
		c.Pool.Strategy = "round_robin"
	}
	if c.Pool.MaxRetries == 0 {
		c.Pool.MaxRetries = 3
	}
	if c.Pool.BackoffMs == 0 {
		c.Pool.BackoffMs = 50
	}
	if c.Pool.ProxyMode == "" {
		c.Pool.ProxyMode = "hybrid"
	}
	if c.RateLimit.PerHostBurst == 0 {
		c.RateLimit.PerHostBurst = 10
	}
	if c.State.Path == "" {
		c.State.Path = "./flarex.db"
	}
	if c.Quota.DailyLimit == 0 {
		c.Quota.DailyLimit = 100000
	}
	if c.Quota.WarnPercent == 0 {
		c.Quota.WarnPercent = 80
	}
	if c.Alerts.CooldownSec == 0 {
		c.Alerts.CooldownSec = 900
	}
}

func (c *Config) applyEnvOverrides() {
	if v := os.Getenv("FLX_HMAC_SECRET"); v != "" {
		c.Security.HMACSecret = v
	}
	if v := os.Getenv("FLX_TOKENS"); v != "" {
		c.Tokens = append(c.Tokens, splitCSV(v)...)
	}
	if v := os.Getenv("FLX_LISTEN_SOCKS5"); v != "" {
		c.Listen.Socks5 = v
	}
	if v := os.Getenv("FLX_LISTEN_HTTP"); v != "" {
		c.Listen.HTTP = v
	}
	if v := os.Getenv("FLX_LISTEN_AUTH_USER"); v != "" {
		c.Listen.AuthUser = v
	}
	if v := os.Getenv("FLX_LISTEN_AUTH_PASS"); v != "" {
		c.Listen.AuthPass = v
	}
	if v := os.Getenv("FLX_ADMIN_ADDR"); v != "" {
		c.Admin.Addr = v
	}
	if v := os.Getenv("FLX_ADMIN_API_KEY"); v != "" {
		c.Admin.APIKey = v
	}
	if v := os.Getenv("FLX_ADMIN_TOKEN"); v != "" {
		c.Admin.Token = v
	}
	if v := os.Getenv("FLX_ADMIN_BASIC_USER"); v != "" {
		c.Admin.BasicUser = v
	}
	if v := os.Getenv("FLX_ADMIN_BASIC_PASS"); v != "" {
		c.Admin.BasicPass = v
	}
	if v := os.Getenv("FLX_ADMIN_UI"); v != "" {
		c.Admin.UI = v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
	}
	if v := os.Getenv("FLX_ADMIN_TOTP_SECRET"); v != "" {
		c.Admin.TOTPSecret = v
	}
	if v := os.Getenv("FLX_DISCORD_WEBHOOK_URL"); v != "" {
		c.Alerts.DiscordURL = v
	}
	if v := os.Getenv("FLX_WORKER_COUNT"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			c.Worker.Count = n
		}
	}
	if v := os.Getenv("FLX_WORKER_PREFIX"); v != "" {
		c.Worker.NamePrefix = v
	}
	if v := os.Getenv("FLX_WORKER_BACKEND"); v != "" {
		c.Worker.DeployBackend = v
	}
	if v := os.Getenv("FLX_LOG_LEVEL"); v != "" {
		c.Log.Level = v
	}
	if v := os.Getenv("FLX_STATE_PATH"); v != "" {
		c.State.Path = v
	}
}

func splitCSV(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			if t := trimSpace(cur); t != "" {
				out = append(out, t)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if t := trimSpace(cur); t != "" {
		out = append(out, t)
	}
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

func (c *Config) validate() error {
	for i, a := range c.Accounts {
		if a.Token == "" {
			return fmt.Errorf("account #%d: token required", i)
		}
	}
	if c.Security.HMACSecret == "" {
		return fmt.Errorf("hmac_secret required (config or env FLX_HMAC_SECRET)")
	}
	return nil
}

func (c *Config) ValidateForDeploy() error {
	if len(c.Accounts) == 0 && len(c.Tokens) == 0 {
		return fmt.Errorf("at least 1 CF token or account required for deploy/destroy/list")
	}
	return nil
}

func (c *Config) ValidateForServer() error {
	bu, bp := c.Admin.BasicUser != "", c.Admin.BasicPass != ""
	if bu != bp {
		return fmt.Errorf("admin.basic_user and admin.basic_pass must both be set or both empty")
	}
	return nil
}
