package logger

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestBannerLineWidthsUniform guards against visual misalignment: every
// rendered line of the banner must have identical rune-count width so the
// figlet column stays straight in any monospace terminal.
func TestBannerLineWidthsUniform(t *testing.T) {
	out := composeBanner()
	lines := strings.Split(strings.Trim(out, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("banner too short: %d lines", len(lines))
	}
	want := utf8.RuneCountInString(lines[0])
	for i, l := range lines {
		got := utf8.RuneCountInString(l)
		if got != want {
			t.Errorf("line %d width = %d, want %d\n  line=%q", i, got, want, l)
		}
	}
}

// TestBannerContainsBrand ensures the project name + tagline survive any future
// refactor of the art strings.
func TestBannerContainsBrand(t *testing.T) {
	out := composeBanner()
	for _, must := range []string{"FlareX", "SOCKS5", "Cloudflare Workers"} {
		if !strings.Contains(out, must) {
			t.Errorf("banner missing %q", must)
		}
	}
}

func TestPadRight(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"abc", 5, "abc  "},
		{"abc", 3, "abc"},
		{"abc", 1, "abc"},
		{"", 3, "   "},
		{"é", 2, "é "}, // 1 rune → pads to 2 runes
	}
	for _, tc := range cases {
		if got := padRight(tc.in, tc.w); got != tc.want {
			t.Errorf("padRight(%q,%d)=%q want %q", tc.in, tc.w, got, tc.want)
		}
	}
}
