package cfapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"time"
)

const apiBase = "https://api.cloudflare.com/client/v4"

var APITimeout = 30 * time.Second

type Client struct {
	accountID string
	token     string
	http      *http.Client
}

func New(accountID, token string) *Client {
	return &Client{
		accountID: accountID,
		token:     token,
		http: &http.Client{
			Timeout: APITimeout,
		},
	}
}

type cfResponse struct {
	Success  bool              `json:"success"`
	Errors   []cfError         `json:"errors"`
	Messages []json.RawMessage `json:"messages"`
	Result   json.RawMessage   `json:"result"`
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var cfr cfResponse
	if err := json.Unmarshal(raw, &cfr); err != nil {
		return nil, fmt.Errorf("cf response parse: %w (body=%s)", err, raw)
	}
	if !cfr.Success {
		if len(cfr.Errors) > 0 {
			return nil, fmt.Errorf("cf api: %d %s", cfr.Errors[0].Code, cfr.Errors[0].Message)
		}
		return nil, fmt.Errorf("cf api: unsuccess (status %d)", resp.StatusCode)
	}
	return cfr.Result, nil
}

func (c *Client) UploadWorker(ctx context.Context, name, scriptBody string) error {
	buf := &bytes.Buffer{}
	mw := multipart.NewWriter(buf)

	metadata := map[string]any{
		"main_module":         "worker.js",
		"compatibility_date":  "2024-09-23",
		"compatibility_flags": []string{"nodejs_compat"},
	}
	metaBytes, _ := json.Marshal(metadata)
	mh := make(textproto.MIMEHeader)
	mh.Set("Content-Disposition", `form-data; name="metadata"`)
	mh.Set("Content-Type", "application/json")
	mp, err := mw.CreatePart(mh)
	if err != nil {
		return err
	}
	mp.Write(metaBytes)

	sh := make(textproto.MIMEHeader)
	sh.Set("Content-Disposition", `form-data; name="worker.js"; filename="worker.js"`)
	sh.Set("Content-Type", "application/javascript+module")
	sp, err := mw.CreatePart(sh)
	if err != nil {
		return err
	}
	sp.Write([]byte(scriptBody))
	mw.Close()

	path := fmt.Sprintf("/accounts/%s/workers/scripts/%s", c.accountID, name)
	_, err = c.do(ctx, http.MethodPut, path, buf, mw.FormDataContentType())
	return err
}

func (c *Client) EnableWorkersDev(ctx context.Context, name string) error {
	path := fmt.Sprintf("/accounts/%s/workers/scripts/%s/subdomain", c.accountID, name)
	body := bytes.NewBufferString(`{"enabled":true}`)
	_, err := c.do(ctx, http.MethodPost, path, body, "application/json")
	return err
}

func (c *Client) DeleteWorker(ctx context.Context, name string) error {
	path := fmt.Sprintf("/accounts/%s/workers/scripts/%s", c.accountID, name)
	_, err := c.do(ctx, http.MethodDelete, path, nil, "")
	return err
}

func (c *Client) ListWorkers(ctx context.Context) ([]WorkerScript, error) {
	path := fmt.Sprintf("/accounts/%s/workers/scripts", c.accountID)
	raw, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	var scripts []WorkerScript
	if err := json.Unmarshal(raw, &scripts); err != nil {
		return nil, err
	}
	return scripts, nil
}

type WorkerScript struct {
	ID         string    `json:"id"`
	CreatedOn  time.Time `json:"created_on"`
	ModifiedOn time.Time `json:"modified_on"`
}

type AccountInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func TodayWorkerRequests(ctx context.Context, token, accountID string) (uint64, error) {
	now := time.Now().UTC()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	body := map[string]any{
		"query": `query($acc: String!, $start: Time!, $end: Time!) {
			viewer {
				accounts(filter: {accountTag: $acc}) {
					workersInvocationsAdaptive(filter: {datetime_geq: $start, datetime_leq: $end}, limit: 1) {
						sum { requests }
					}
				}
			}
		}`,
		"variables": map[string]any{
			"acc":   accountID,
			"start": startOfDay.Format(time.RFC3339),
			"end":   now.Format(time.RFC3339),
		},
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.cloudflare.com/client/v4/graphql", bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	httpc := &http.Client{Timeout: APITimeout}
	resp, err := httpc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	var out struct {
		Data struct {
			Viewer struct {
				Accounts []struct {
					WorkersInvocationsAdaptive []struct {
						Sum struct {
							Requests uint64 `json:"requests"`
						} `json:"sum"`
					} `json:"workersInvocationsAdaptive"`
				} `json:"accounts"`
			} `json:"viewer"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(rawBody, &out); err != nil {
		return 0, fmt.Errorf("graphql parse: %w (body=%s)", err, rawBody)
	}
	if len(out.Errors) > 0 {
		return 0, fmt.Errorf("graphql: %s", out.Errors[0].Message)
	}
	if len(out.Data.Viewer.Accounts) == 0 {
		return 0, nil
	}
	if len(out.Data.Viewer.Accounts[0].WorkersInvocationsAdaptive) == 0 {
		return 0, nil
	}
	return out.Data.Viewer.Accounts[0].WorkersInvocationsAdaptive[0].Sum.Requests, nil
}

type SubdomainInfo struct {
	Subdomain string `json:"subdomain"`
}

func ListAccounts(ctx context.Context, token string) ([]AccountInfo, error) {
	bare := &Client{token: token, http: &http.Client{Timeout: APITimeout}}
	return bare.listAccounts(ctx)
}

func (c *Client) listAccounts(ctx context.Context) ([]AccountInfo, error) {
	raw, err := c.do(ctx, http.MethodGet, "/accounts", nil, "")
	if err != nil {
		return nil, err
	}
	var out []AccountInfo
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) GetSubdomain(ctx context.Context) (string, error) {
	path := fmt.Sprintf("/accounts/%s/workers/subdomain", c.accountID)
	raw, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return "", err
	}
	var info SubdomainInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return "", err
	}
	return info.Subdomain, nil
}

func (c *Client) CreateSubdomain(ctx context.Context, name string) error {
	path := fmt.Sprintf("/accounts/%s/workers/subdomain", c.accountID)
	body := bytes.NewBufferString(fmt.Sprintf(`{"subdomain":%q}`, name))
	_, err := c.do(ctx, http.MethodPut, path, body, "application/json")
	return err
}

type Zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func ListZones(ctx context.Context, token string) ([]Zone, error) {
	bare := &Client{token: token, http: &http.Client{Timeout: APITimeout}}
	raw, err := bare.do(ctx, http.MethodGet, "/zones?per_page=50", nil, "")
	if err != nil {
		return nil, err
	}
	var zs []Zone
	if err := json.Unmarshal(raw, &zs); err != nil {
		return nil, err
	}
	return zs, nil
}

type DNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

func ListDNSRecords(ctx context.Context, token, zoneID, namePrefix string) ([]DNSRecord, error) {
	bare := &Client{token: token, http: &http.Client{Timeout: APITimeout}}
	path := fmt.Sprintf("/zones/%s/dns_records?per_page=200", zoneID)
	raw, err := bare.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	var rs []DNSRecord
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, err
	}
	if namePrefix == "" {
		return rs, nil
	}
	out := rs[:0]
	for _, r := range rs {
		if len(r.Name) >= len(namePrefix) && r.Name[:len(namePrefix)] == namePrefix {
			out = append(out, r)
		}
	}
	return out, nil
}

func DeleteDNSRecord(ctx context.Context, token, zoneID, recordID string) error {
	bare := &Client{token: token, http: &http.Client{Timeout: APITimeout}}
	path := fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, recordID)
	_, err := bare.do(ctx, http.MethodDelete, path, nil, "")
	return err
}

// StartTail opens a live log stream for the named Worker script. Returns
// the tail ID (used for Delete) and a WebSocket URL to consume.
// See CF docs: POST /accounts/{id}/workers/scripts/{name}/tails
func (c *Client) StartTail(ctx context.Context, scriptName string) (tailID, wsURL string, err error) {
	path := fmt.Sprintf("/accounts/%s/workers/scripts/%s/tails", c.accountID, scriptName)
	raw, err := c.do(ctx, http.MethodPost, path, bytes.NewBufferString("{}"), "application/json")
	if err != nil {
		return "", "", err
	}
	var out struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", err
	}
	return out.ID, out.URL, nil
}

func (c *Client) DeleteTail(ctx context.Context, scriptName, tailID string) error {
	path := fmt.Sprintf("/accounts/%s/workers/scripts/%s/tails/%s", c.accountID, scriptName, tailID)
	_, err := c.do(ctx, http.MethodDelete, path, nil, "")
	return err
}

type WorkerDomain struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	Service  string `json:"service"`
	ZoneID   string `json:"zone_id"`
	ZoneName string `json:"zone_name"`
}

func (c *Client) AttachWorkerDomain(ctx context.Context, hostname, scriptName, zoneID string) (*WorkerDomain, error) {
	path := fmt.Sprintf("/accounts/%s/workers/domains", c.accountID)
	body := bytes.NewBufferString(fmt.Sprintf(
		`{"environment":"production","hostname":%q,"service":%q,"zone_id":%q}`,
		hostname, scriptName, zoneID,
	))
	raw, err := c.do(ctx, http.MethodPut, path, body, "application/json")
	if err != nil {
		return nil, err
	}
	var wd WorkerDomain
	if err := json.Unmarshal(raw, &wd); err != nil {
		return nil, err
	}
	return &wd, nil
}

func (c *Client) DetachWorkerDomain(ctx context.Context, bindingID string) error {
	path := fmt.Sprintf("/accounts/%s/workers/domains/%s", c.accountID, bindingID)
	_, err := c.do(ctx, http.MethodDelete, path, nil, "")
	return err
}

func (c *Client) ListWorkerDomains(ctx context.Context, hostnamePrefix string) ([]WorkerDomain, error) {
	path := fmt.Sprintf("/accounts/%s/workers/domains", c.accountID)
	raw, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	var ds []WorkerDomain
	if err := json.Unmarshal(raw, &ds); err != nil {
		return nil, err
	}
	if hostnamePrefix == "" {
		return ds, nil
	}
	out := ds[:0]
	for _, d := range ds {
		if len(d.Hostname) >= len(hostnamePrefix) && d.Hostname[:len(hostnamePrefix)] == hostnamePrefix {
			out = append(out, d)
		}
	}
	return out, nil
}
