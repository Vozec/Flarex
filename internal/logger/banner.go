package logger

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

const (
	cfOrange  = "\033[38;5;208m"
	cfAccent  = "\033[38;5;214m"
	dimColor  = "\033[38;5;240m"
	boldColor = "\033[1m"
	resetCol  = "\033[0m"
)

// cloudArt is the Cloudflare cloud mark — 9 lines, visual width 19 runes.
// All rows are right-padded with spaces so the figlet column stays straight.
var cloudArt = []string{
	`      ▄▄█████▄▄    `,
	`   ▄██▀      ▀██▄  `,
	`  ██▀          ▀██ `,
	` ██              ██`,
	`█▀  ▄▄██████▄▄    █`,
	`█ ▄██▀      ▀██▄  █`,
	` ▀██          ██▀  `,
	`   ▀██▄     ▄██▀   `,
	`      ▀████▀       `,
}

// textArt is the FlareX figlet (Standard font) + a two-line tagline.
// Double-quoted to allow inline backticks + escaped backslashes in the art.
var textArt = []string{
	" _____ _                __   __",
	"|  ___| | __ _ _ __ ___ \\ \\ / /",
	"| |_  | |/ _` | '__/ _ \\ \\ V / ",
	"|  _| | | (_| | | |  __/  > <  ",
	"|_|   |_|\\__,_|_|  \\___| /_/\\_\\",
	"                               ",
	"  FlareX — SOCKS5 / HTTP rotator",
	"  over Cloudflare Workers      ",
}

// visualWidth returns the rune count of s — a decent approximation of the
// monospace column count for strings that only use single-width runes (ASCII +
// block-drawing chars). Good enough for banner alignment.
func visualWidth(s string) int { return utf8.RuneCountInString(s) }

// padRight pads s with spaces until its visual width reaches w. If already ≥w,
// returns s unchanged.
func padRight(s string, w int) string {
	n := visualWidth(s)
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}

// composeBanner zips cloudArt + textArt line by line. Both slices can differ
// in length; the shorter side pads with spaces matching its reference width.
func composeBanner() string {
	cloudW, textW := 0, 0
	for _, l := range cloudArt {
		if v := visualWidth(l); v > cloudW {
			cloudW = v
		}
	}
	for _, l := range textArt {
		if v := visualWidth(l); v > textW {
			textW = v
		}
	}
	lines := len(cloudArt)
	if len(textArt) > lines {
		lines = len(textArt)
	}
	var b strings.Builder
	b.WriteByte('\n')
	for i := 0; i < lines; i++ {
		left := strings.Repeat(" ", cloudW)
		if i < len(cloudArt) {
			left = padRight(cloudArt[i], cloudW)
		}
		right := strings.Repeat(" ", textW)
		if i < len(textArt) {
			right = padRight(textArt[i], textW)
		}
		b.WriteString(left)
		b.WriteString("  ")
		b.WriteString(right)
		b.WriteByte('\n')
	}
	return b.String()
}

// PrintBanner writes the ASCII banner + a metadata line. Safe for non-TTY
// output (colors stripped).
func PrintBanner(version, commit, buildDate string) {
	w := os.Stderr
	useColor := isTTY(w)

	art := composeBanner()
	if useColor {
		fmt.Fprint(w, cfOrange, art, resetCol)
	} else {
		fmt.Fprint(w, art)
	}

	meta := fmt.Sprintf("  version=%s", version)
	if commit != "" {
		meta += " commit=" + commit
	}
	if buildDate != "" {
		meta += " built=" + buildDate
	}
	if useColor {
		fmt.Fprintln(w, dimColor+meta+resetCol)
	} else {
		fmt.Fprintln(w, meta)
	}
	fmt.Fprintln(w)
}

type ConfigRow struct{ Key, Value string }
type ConfigSection struct {
	Title string
	Rows  []ConfigRow
}

func PrintConfig(sections []ConfigSection) {
	w := os.Stderr
	useColor := isTTY(w)

	maxKey := 0
	for _, s := range sections {
		for _, r := range s.Rows {
			if r.Value == "" {
				continue
			}
			if n := len(r.Key); n > maxKey {
				maxKey = n
			}
		}
	}

	border := strings.Repeat("─", 64)
	if useColor {
		fmt.Fprintln(w, dimColor+border+resetCol)
	} else {
		fmt.Fprintln(w, border)
	}
	for i, s := range sections {
		if i > 0 {
			fmt.Fprintln(w)
		}
		title := fmt.Sprintf("▸ %s", s.Title)
		if useColor {
			fmt.Fprintln(w, boldColor+cfAccent+title+resetCol)
		} else {
			fmt.Fprintln(w, title)
		}
		for _, r := range s.Rows {
			if r.Value == "" {
				continue
			}
			pad := strings.Repeat(" ", maxKey-len(r.Key))
			if useColor {
				fmt.Fprintf(w, "  %s%s%s %s:%s %s\n",
					dimColor, r.Key, resetCol, dimColor, resetCol, pad+r.Value)
			} else {
				fmt.Fprintf(w, "  %s%s : %s\n", r.Key, pad, r.Value)
			}
		}
	}
	if useColor {
		fmt.Fprintln(w, dimColor+border+resetCol)
	} else {
		fmt.Fprintln(w, border)
	}
	fmt.Fprintln(w)
}

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
