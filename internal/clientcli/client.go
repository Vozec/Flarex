package clientcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	yaml "go.yaml.in/yaml/v3"
)

type ConfigFile struct {
	URL      string `yaml:"url"`
	APIKey   string `yaml:"api_key,omitempty"` //nolint:gosec // G117: intentionally stored in local config file with 0600 perms
	Bearer   string `yaml:"bearer,omitempty"`
	BasicU   string `yaml:"basic_user,omitempty"`
	BasicP   string `yaml:"basic_pass,omitempty"`
	Insecure bool   `yaml:"insecure,omitempty"`
}

func DefaultPath() string {
	if env := os.Getenv("FLX_CLIENT_CONFIG"); env != "" {
		return env
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "flarex", "client.yaml")
}

func Save(cfg *ConfigFile) error {
	return SaveTo(cfg, DefaultPath())
}

func SaveTo(cfg *ConfigFile, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func Load() (*ConfigFile, error) { return LoadFrom(DefaultPath()) }

func LoadFrom(path string) (*ConfigFile, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotLoggedIn
	}
	if err != nil {
		return nil, err
	}
	var c ConfigFile
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	if c.URL == "" {
		return nil, ErrNotLoggedIn
	}
	return &c, nil
}

var ErrNotLoggedIn = errors.New("not logged in: run `flarex client login --url ... --api-key ...`")

type Client struct {
	cfg  *ConfigFile
	http *http.Client
}

// Default HTTP client timeout. Admin mutations (destroy, clean, deploy,
// add-token) fan out across multiple CF API calls per account and can
// easily exceed 30 s on a fleet of a few dozen workers. Server-side
// TokenOpTimeoutD is 120 s; give the client a ~40 s headroom so the
// server's own error response is what the user sees, not a client
// timeout.
const defaultTimeout = 180 * time.Second

func New(cfg *ConfigFile) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: defaultTimeout}}
}

func (c *Client) Do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	u, err := url.Parse(c.cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("bad URL: %w", err)
	}
	// path may contain a query string ("/metrics/history?days=7"); split it
	// so url.URL.String() doesn't escape '?' into '%3F'.
	q := ""
	if i := strings.IndexByte(path, '?'); i >= 0 {
		q = path[i+1:]
		path = path[:i]
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	if q != "" {
		u.RawQuery = q
	}

	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.applyAuth(req)
	return c.http.Do(req)
}

func (c *Client) applyAuth(req *http.Request) {
	switch {
	case c.cfg.APIKey != "":
		req.Header.Set("X-API-Key", c.cfg.APIKey)
	case c.cfg.Bearer != "":
		req.Header.Set("Authorization", "Bearer "+c.cfg.Bearer)
	case c.cfg.BasicU != "":
		req.SetBasicAuth(c.cfg.BasicU, c.cfg.BasicP)
	}
}

func (c *Client) GetJSON(ctx context.Context, path string, out any) error {
	resp, err := c.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, raw)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) GetRaw(ctx context.Context, path string) ([]byte, error) {
	resp, err := c.Do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return raw, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return raw, nil
}
