package config

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultExampleURL = "https://raw.githubusercontent.com/Vozec/FlareX/main/config.example.yaml"

var templateMarkers = []string{
	"change_me_32b_minimum_random_string",
	"your_cf_account_id",
	"cf_api_token_with_workers_edit",
	"cf_api_token_with_workers_edit_scope",
	"your-subdomain",
}

func Bootstrap(ctx context.Context, path, url string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	if url == "" {
		url = os.Getenv("FLX_CONFIG_EXAMPLE_URL")
	}
	if url == "" {
		url = DefaultExampleURL
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("mkdir for config: %w", err)
	}

	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false, fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

func LooksLikeTemplate(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := string(raw)
	for _, m := range templateMarkers {
		if strings.Contains(s, m) {
			return m, nil
		}
	}
	return "", nil
}
