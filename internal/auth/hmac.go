package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

func Sign(secret, host string, port int, tls bool, mode string) (ts int64, sig string) {
	ts = time.Now().Unix()
	t := 0
	if tls {
		t = 1
	}
	if mode == "" {
		mode = "socket"
	}
	msg := fmt.Sprintf("%d|%s|%d|%d|%s", ts, host, port, t, mode)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	sig = hex.EncodeToString(mac.Sum(nil))
	return
}
