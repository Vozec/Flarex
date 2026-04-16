package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTmp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	p := writeTmp(t, `
accounts:
  - id: "a"
    token: "t"
    subdomain: "s"
security:
  hmac_secret: "x"
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen.Socks5 != "tcp://127.0.0.1:1080" {
		t.Errorf("default socks5: %q", c.Listen.Socks5)
	}
	if c.Worker.NamePrefix != "flarex-" {
		t.Errorf("default prefix: %q", c.Worker.NamePrefix)
	}
	if c.Worker.Count != 10 {
		t.Errorf("default count: %d", c.Worker.Count)
	}
	if c.Quota.DailyLimit != 100000 {
		t.Errorf("default quota: %d", c.Quota.DailyLimit)
	}
	if c.Quota.WarnPercent != 80 {
		t.Errorf("default warn: %d", c.Quota.WarnPercent)
	}
}

func TestLoadHMACFromEnv(t *testing.T) {
	t.Setenv("FLX_HMAC_SECRET", "from-env")
	p := writeTmp(t, `
accounts:
  - id: "a"
    token: "t"
    subdomain: "s"
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Security.HMACSecret != "from-env" {
		t.Errorf("env override failed: %q", c.Security.HMACSecret)
	}
}

func TestLoadMissingHMAC(t *testing.T) {
	t.Setenv("FLX_HMAC_SECRET", "")
	p := writeTmp(t, `
accounts:
  - id: "a"
    token: "t"
    subdomain: "s"
`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for missing hmac_secret")
	}
}

func TestLoadAccountMissingToken(t *testing.T) {
	p := writeTmp(t, `
accounts:
  - id: "a"
    subdomain: "s"
security:
  hmac_secret: "x"
`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestLoadValidateForDeployEmpty(t *testing.T) {
	p := writeTmp(t, `
security:
  hmac_secret: "x"
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateForDeploy(); err == nil {
		t.Error("expected error: no accounts/tokens")
	}
}

func TestLoadTokensOnly(t *testing.T) {
	p := writeTmp(t, `
tokens:
  - "t1"
  - "t2"
security:
  hmac_secret: "x"
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Tokens) != 2 {
		t.Errorf("got %d tokens", len(c.Tokens))
	}
	if err := c.ValidateForDeploy(); err != nil {
		t.Errorf("ValidateForDeploy with tokens-only: %v", err)
	}
}

func TestLoadAlertsConfig(t *testing.T) {
	p := writeTmp(t, `
accounts:
  - id: "a"
    token: "t"
    subdomain: "s"
security:
  hmac_secret: "x"
alerts:
  cooldown_sec: 60
  discord_webhook_url: "https://discord/api/x"
  http_webhooks:
    - url: "https://h1"
      headers:
        X-Foo: bar
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Alerts.CooldownSec != 60 {
		t.Errorf("cooldown=%d", c.Alerts.CooldownSec)
	}
	if c.Alerts.DiscordURL != "https://discord/api/x" {
		t.Errorf("discord=%s", c.Alerts.DiscordURL)
	}
	if len(c.Alerts.HTTPWebhooks) != 1 || c.Alerts.HTTPWebhooks[0].URL != "https://h1" {
		t.Errorf("http_webhooks=%+v", c.Alerts.HTTPWebhooks)
	}
	if c.Alerts.HTTPWebhooks[0].Headers["X-Foo"] != "bar" {
		t.Errorf("headers=%+v", c.Alerts.HTTPWebhooks[0].Headers)
	}
}

func TestValidateForServerBasicAuthIncomplete(t *testing.T) {
	cases := []struct {
		name string
		u, p string
		ok   bool
	}{
		{"both empty", "", "", true},
		{"both set", "alice", "secret", true},
		{"user only", "alice", "", false},
		{"pass only", "", "secret", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{}
			c.Admin.BasicUser = tc.u
			c.Admin.BasicPass = tc.p
			err := c.ValidateForServer()
			if tc.ok && err != nil {
				t.Errorf("expected ok, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestLoadAllowPortsWildcard(t *testing.T) {
	p := writeTmp(t, `
accounts:
  - id: "a"
    token: "t"
    subdomain: "s"
security:
  hmac_secret: "x"
filter:
  allow_ports: ["*"]
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Filter.AllowPorts) != 1 || c.Filter.AllowPorts[0] != "*" {
		t.Errorf("ports=%v", c.Filter.AllowPorts)
	}
}
