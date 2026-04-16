package proxy

import (
	"bufio"
	"strings"
	"testing"
)

func TestReadHTTPRequest_Connect(t *testing.T) {
	req := "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\nProxy-Authorization: Basic YWxpY2U6cHc=\r\n\r\n"
	r := bufio.NewReader(strings.NewReader(req))
	got, auth, err := readHTTPRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	if got.method != "CONNECT" {
		t.Errorf("method = %q", got.method)
	}
	if got.target != "example.com:443" {
		t.Errorf("target = %q", got.target)
	}
	if auth != "Basic YWxpY2U6cHc=" {
		t.Errorf("auth = %q", auth)
	}
}

func TestReadHTTPRequest_Malformed(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("garbage\r\n\r\n"))
	if _, _, err := readHTTPRequest(r); err == nil {
		t.Error("expected error on malformed request")
	}
}

func TestReadHTTPRequest_HeaderCaseInsensitive(t *testing.T) {
	req := "CONNECT x:443 HTTP/1.1\r\nproxy-AuThoRiZation: Basic abc\r\n\r\n"
	r := bufio.NewReader(strings.NewReader(req))
	_, auth, err := readHTTPRequest(r)
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Basic abc" {
		t.Errorf("case-insensitive header parse failed: %q", auth)
	}
}

func TestDecodeBasicAuth(t *testing.T) {
	cases := []struct {
		in     string
		wantU  string
		wantP  string
		wantOK bool
	}{
		{"Basic YWxpY2U6cHc=", "alice", "pw", true},
		{"basic YWxpY2U6cHc=", "alice", "pw", true}, // case-insensitive prefix
		{"Basic YWxpY2U=", "", "", false},           // no colon in decoded
		{"Digest foo", "", "", false},               // wrong scheme
		{"", "", "", false},
		{"Basic !!!notbase64", "", "", false},
		{"Basic " + b64("user:with:colons"), "user", "with:colons", true},
	}
	for _, tc := range cases {
		u, p, ok := decodeBasicAuth(tc.in)
		if ok != tc.wantOK || u != tc.wantU || p != tc.wantP {
			t.Errorf("decodeBasicAuth(%q) = (%q,%q,%v) want (%q,%q,%v)",
				tc.in, u, p, ok, tc.wantU, tc.wantP, tc.wantOK)
		}
	}
}

func TestSplitHostPort(t *testing.T) {
	cases := []struct {
		in      string
		host    string
		port    int
		wantErr bool
	}{
		{"example.com:443", "example.com", 443, false},
		{"1.2.3.4:8080", "1.2.3.4", 8080, false},
		{"[::1]:22", "::1", 22, false},
		{"https://example.com:443", "example.com", 443, false},
		{"example.com", "", 0, true},
		{"example.com:abc", "", 0, true},
		{"", "", 0, true},
	}
	for _, tc := range cases {
		h, p, err := splitHostPort(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("splitHostPort(%q) err=%v wantErr=%v", tc.in, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && (h != tc.host || p != tc.port) {
			t.Errorf("splitHostPort(%q) = (%q,%d) want (%q,%d)", tc.in, h, p, tc.host, tc.port)
		}
	}
}

func b64(s string) string {
	const alph = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	out := make([]byte, 0, 4*((len(s)+2)/3))
	for i := 0; i < len(s); i += 3 {
		var v uint32
		pad := 0
		for j := 0; j < 3; j++ {
			v <<= 8
			if i+j < len(s) {
				v |= uint32(s[i+j])
			} else {
				pad++
			}
		}
		out = append(out,
			alph[(v>>18)&0x3F],
			alph[(v>>12)&0x3F],
			alph[(v>>6)&0x3F],
			alph[v&0x3F],
		)
		for p := 0; p < pad; p++ {
			out[len(out)-1-p] = '='
		}
	}
	return string(out)
}
