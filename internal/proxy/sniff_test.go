package proxy

import "testing"

func TestLooksLikeHTTP(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"http1.1 get", "GET / HTTP/1.1\r\n", true},
		{"http1.1 post", "POST /api HTTP/1.1\r\n", true},
		{"http1.1 connect", "CONNECT host:443 HTTP/1.1\r\n", true},
		{"http2 preface", "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n", true},
		{"method without space", "GETVAL", false},
		{"tls clienthello", "\x16\x03\x01\x00\xa0\x01", false},
		{"ssh banner", "SSH-2.0-OpenSSH_9.6", false},
		{"raw bytes", "\x00\x01\x02\x03\x04\x05\x06\x07", false},
		{"redis ping", "*1\r\n$4\r\nPING\r\n", false},
		{"too short", "GE", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		if got := LooksLikeHTTP([]byte(tc.in)); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestIsTLSClientHello(t *testing.T) {
	if !IsTLSClientHello([]byte{0x16, 0x03, 0x01, 0x00}) {
		t.Error("TLS 1.0 ClientHello not detected")
	}
	if !IsTLSClientHello([]byte{0x16, 0x03, 0x03}) {
		t.Error("TLS 1.2 ClientHello not detected")
	}
	if IsTLSClientHello([]byte("GET / HTTP/1.1")) {
		t.Error("HTTP misdetected as TLS")
	}
	if IsTLSClientHello([]byte{0x16}) {
		t.Error("too-short buffer accepted")
	}
}
