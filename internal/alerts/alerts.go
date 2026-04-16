package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Vozec/flarex/internal/logger"
)

type Kind string

const (
	KindQuotaWarn  Kind = "quota_warn"
	KindQuotaLimit Kind = "quota_limit"
	KindWorkerDown Kind = "worker_down"
)

type Event struct {
	Kind        Kind      `json:"kind"`
	AccountID   string    `json:"account_id,omitempty"`
	AccountName string    `json:"account_name,omitempty"`
	Message     string    `json:"message"`
	Used        uint64    `json:"used,omitempty"`
	Limit       uint64    `json:"limit,omitempty"`
	Detail      any       `json:"detail,omitempty"`
	At          time.Time `json:"at"`
}

type Sink interface {
	Send(ctx context.Context, ev Event) error
	Name() string
}

type Dispatcher struct {
	sinks   []Sink
	cooldwn time.Duration
	last    sync.Map
}

func NewDispatcher(cooldown time.Duration, sinks ...Sink) *Dispatcher {
	return &Dispatcher{sinks: sinks, cooldwn: cooldown}
}

func (d *Dispatcher) Fire(ctx context.Context, ev Event) {
	if len(d.sinks) == 0 {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now()
	}

	if d.cooldwn > 0 {
		key := string(ev.Kind) + "|" + ev.AccountID
		v, _ := d.last.LoadOrStore(key, new(atomic.Int64))
		ctr := v.(*atomic.Int64)
		now := ev.At.Unix()
		prev := ctr.Load()
		if prev > 0 && now-prev < int64(d.cooldwn.Seconds()) {
			return
		}
		if !ctr.CompareAndSwap(prev, now) {
			return
		}
	}
	for _, s := range d.sinks {
		s := s
		go func() {
			sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := s.Send(sctx, ev); err != nil {
				logger.L.Warn().Str("sink", s.Name()).Str("kind", string(ev.Kind)).Err(err).Msg("alert send failed")
				return
			}
			logger.L.Info().Str("sink", s.Name()).Str("kind", string(ev.Kind)).Str("account", ev.AccountID).Msg("alert sent")
		}()
	}
}

type HTTPSink struct {
	URL     string
	Headers map[string]string
	client  *http.Client
}

func NewHTTPSink(url string, headers map[string]string) *HTTPSink {
	return &HTTPSink{URL: url, Headers: headers, client: &http.Client{Timeout: 10 * time.Second}}
}

func (h *HTTPSink) Name() string { return "http:" + h.URL }

func (h *HTTPSink) Send(ctx context.Context, ev Event) error {
	raw, _ := json.Marshal(ev)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("http sink status %d", resp.StatusCode)
	}
	return nil
}

type DiscordSink struct {
	WebhookURL string
	Username   string
	AvatarURL  string
	client     *http.Client
}

const defaultCFLogoURL = "https://www.cloudflare.com/static/b30a57477bde900ba55c0b5f98c4e524/c238b/cf-logo-on-white-bg.png"

func NewDiscordSink(url, username string) *DiscordSink {
	if username == "" {
		username = "FlareX"
	}
	return &DiscordSink{
		WebhookURL: url,
		Username:   username,
		AvatarURL:  defaultCFLogoURL,
		client:     &http.Client{Timeout: 10 * time.Second},
	}
}

func (d *DiscordSink) Name() string { return "discord" }

type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type discordThumb struct {
	URL string `json:"url"`
}

type discordEmbed struct {
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Color       int            `json:"color"`
	Timestamp   string         `json:"timestamp"`
	Fields      []discordField `json:"fields,omitempty"`
	Thumbnail   *discordThumb  `json:"thumbnail,omitempty"`
}

type discordPayload struct {
	Username  string         `json:"username,omitempty"`
	AvatarURL string         `json:"avatar_url,omitempty"`
	Content   string         `json:"content,omitempty"`
	Embeds    []discordEmbed `json:"embeds,omitempty"`
}

func progressBar(used, limit uint64, width int) (string, int) {
	if limit == 0 {
		return "", 0
	}
	pct := int(float64(used) / float64(limit) * 100.0)
	if pct > 100 {
		pct = 100
	}
	filled := pct * width / 100
	bar := ""
	for i := 0; i < width; i++ {
		if i < filled {
			bar += "█"
		} else {
			bar += "░"
		}
	}
	return bar, pct
}

func (d *DiscordSink) Send(ctx context.Context, ev Event) error {
	var title string
	color := 0x3498DB
	switch ev.Kind {
	case KindQuotaWarn:
		color = 0xF38020
		title = "⚠️ Cloudflare Workers — Quota Warning"
	case KindQuotaLimit:
		color = 0xE74C3C
		title = "🔥 Cloudflare Workers — Quota Limit Reached"
	case KindWorkerDown:
		color = 0xE74C3C
		title = "💀 Worker Down"
	default:
		title = string(ev.Kind)
	}

	bar, pct := progressBar(ev.Used, ev.Limit, 20)

	desc := ev.Message
	if bar != "" {
		desc = fmt.Sprintf("```\n%s  %d%%\n```\n%s", bar, pct, ev.Message)
	}

	fields := []discordField{}
	if ev.AccountName != "" || ev.AccountID != "" {
		name := prettifyAccountName(ev.AccountName)
		if name == "" {
			name = "(unknown)"
		}
		idShort := ev.AccountID
		if len(idShort) > 12 {
			idShort = idShort[:8] + "…"
		}
		fields = append(fields, discordField{Name: "Account", Value: fmt.Sprintf("**%s**\n`%s`", name, idShort), Inline: true})
	}
	if ev.Limit > 0 {
		fields = append(fields, discordField{Name: "Used", Value: fmt.Sprintf("%d / %d", ev.Used, ev.Limit), Inline: true})
		fields = append(fields, discordField{Name: "Remaining", Value: fmt.Sprintf("%d", ev.Limit-min(ev.Used, ev.Limit)), Inline: true})
	}

	embed := discordEmbed{
		Title:       title,
		Description: desc,
		Color:       color,
		Timestamp:   ev.At.UTC().Format(time.RFC3339),
		Fields:      fields,
		Thumbnail:   &discordThumb{URL: defaultCFLogoURL},
	}

	payload := discordPayload{
		Username:  d.Username,
		AvatarURL: d.AvatarURL,
		Embeds:    []discordEmbed{embed},
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.WebhookURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("discord status %d", resp.StatusCode)
	}
	return nil
}

func min(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func prettifyAccountName(s string) string {
	s = strings.TrimSpace(s)
	for _, suf := range []string{"'s Account", "’s Account", "'s account", "’s account"} {
		if strings.HasSuffix(s, suf) {
			s = s[:len(s)-len(suf)]
			break
		}
	}
	if i := strings.Index(s, "@"); i > 0 {
		return strings.ToLower(s)
	}
	return s
}
