package worker

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplateHash_Deterministic(t *testing.T) {
	a, err := TemplateHash("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := TemplateHash("")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("same input → different hash: %s vs %s", a, b)
	}
	if len(a) != 64 {
		t.Errorf("sha256 hex = %d chars, want 64", len(a))
	}
}

func TestTemplateHash_OverridePath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tpl.js")
	content := []byte("export default { fetch: () => new Response('hi') }")
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := TemplateHash(p)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	if h != hex.EncodeToString(sum[:]) {
		t.Errorf("TemplateHash != sha256(file)")
	}
	if _, err := TemplateHash(filepath.Join(dir, "missing.js")); err == nil {
		t.Error("missing path: expected error")
	}
}

func TestRender_ReplacesPlaceholders(t *testing.T) {
	out, err := Render("my-secret-key", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "__HMAC_SECRET__") {
		t.Error("__HMAC_SECRET__ not replaced")
	}
	if strings.Contains(out, "__TEMPLATE_HASH__") {
		t.Error("__TEMPLATE_HASH__ not replaced")
	}
	expectedHash, _ := TemplateHash("")
	if !strings.Contains(out, expectedHash) {
		t.Error("rendered template missing expected hash value")
	}
}

func TestRender_EscapesSecretQuotes(t *testing.T) {
	out, err := Render(`bad"secret\with"quotes`, "")
	if err != nil {
		t.Fatal(err)
	}
	// JS string "__HMAC_SECRET__" → quotes/backslashes must be escaped.
	if strings.Contains(out, `"bad"secret\with"`) {
		t.Error("unescaped secret found in rendered template — JS injection risk")
	}
}
