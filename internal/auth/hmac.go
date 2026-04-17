package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

func Sign(secret, host string, port int, tls bool, mode string) (ts int64, sig string) {
	ts = time.Now().Unix()
	if mode == "" {
		mode = "socket"
	}
	var b strings.Builder
	b.Grow(len(host) + len(mode) + 30) // pre-size: ts + pipes + port + tls flag
	b.WriteString(strconv.FormatInt(ts, 10))
	b.WriteByte('|')
	b.WriteString(host)
	b.WriteByte('|')
	b.WriteString(strconv.Itoa(port))
	b.WriteByte('|')
	if tls {
		b.WriteByte('1')
	} else {
		b.WriteByte('0')
	}
	b.WriteByte('|')
	b.WriteString(mode)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(b.String()))
	sig = hex.EncodeToString(mac.Sum(nil))
	return
}
