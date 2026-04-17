package admin

import (
	"encoding/json"
	"net/http"
)

const rateLimitMsg = `{"error":"rate limit exceeded — slow down API key mutations"}`

// writeJSON sets the Content-Type header and encodes v as JSON.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// methodPOST returns true if the method is POST, otherwise writes a
// 405 with the Allow header and returns false.
func methodPOST(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodPost {
		return true
	}
	w.Header().Set("Allow", http.MethodPost)
	http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	return false
}
