package worker

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"os"
	"strings"
)

//go:embed template.js
var defaultTemplate string

// TemplateHash returns sha256 of the raw template (pre-replacement).
// Deterministic across deploys with same template; changes when JS is edited.
// overridePath reads an alt template; empty = embedded default.
func TemplateHash(overridePath string) (string, error) {
	tpl := defaultTemplate
	if overridePath != "" {
		b, err := os.ReadFile(overridePath)
		if err != nil {
			return "", err
		}
		tpl = string(b)
	}
	sum := sha256.Sum256([]byte(tpl))
	return hex.EncodeToString(sum[:]), nil
}

func Render(hmacSecret, overridePath string) (string, error) {
	tpl := defaultTemplate
	if overridePath != "" {
		b, err := os.ReadFile(overridePath)
		if err != nil {
			return "", err
		}
		tpl = string(b)
	}
	sum := sha256.Sum256([]byte(tpl))
	hash := hex.EncodeToString(sum[:])
	tpl = strings.ReplaceAll(tpl, "__HMAC_SECRET__", escapeJS(hmacSecret))
	tpl = strings.ReplaceAll(tpl, "__TEMPLATE_HASH__", hash)
	return tpl, nil
}

func escapeJS(s string) string {

	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return r.Replace(s)
}
