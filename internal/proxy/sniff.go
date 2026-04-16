package proxy

// Minimum bytes we want buffered before trying to classify.
// Enough for "CONNECT host HTTP/1.1" but short enough to not stall on slow
// clients that send small initial chunks.
const sniffMinBytes = 7

// LooksLikeHTTP reports whether buf plausibly starts an HTTP/1.x or HTTP/2
// request. Used to gate the socket→fetch fallback for CF-hosted targets —
// fetch mode is HTTP-only at the Worker, so using it on a non-HTTP stream
// (SSH, Redis, raw TCP) would silently corrupt bytes.
//
// Detection is intentionally conservative: false negatives (we treat HTTP
// as non-HTTP) cause a dial error, which is recoverable. False positives
// (we treat non-HTTP as HTTP) would wrap the stream in fetch and deliver
// an HTTP response to a client that expected raw bytes — much worse.
func LooksLikeHTTP(buf []byte) bool {
	if len(buf) < sniffMinBytes {
		return false
	}
	// HTTP/2 client preface. Exact sequence.
	if len(buf) >= 24 && string(buf[:24]) == "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n" {
		return true
	}
	// HTTP/1.x — method token followed by space. Full set from RFC 7231 + common
	// extensions. Validate the method ends with ' ' to avoid matching e.g. "GETV".
	methods := []string{
		"GET ", "POST ", "HEAD ", "PUT ", "DELETE ",
		"OPTIONS ", "PATCH ", "TRACE ", "CONNECT ",
	}
	for _, m := range methods {
		if len(buf) >= len(m) && string(buf[:len(m)]) == m {
			return true
		}
	}
	return false
}

// IsTLSClientHello reports whether buf starts a TLS record (ContentType=22,
// version major=3). Used as a hint that socket mode is required (fetch cannot
// proxy TLS — the Worker would try to HTTP-parse the ClientHello).
func IsTLSClientHello(buf []byte) bool {
	return len(buf) >= 3 && buf[0] == 0x16 && buf[1] == 0x03
}
