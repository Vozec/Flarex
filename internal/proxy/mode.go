package proxy

const (
	ModeSocket = "socket"
	ModeFetch  = "fetch"
	ModeHybrid = "hybrid"
)

// PickMode returns "socket" or "fetch" for the actual Worker call.
// Policy:
//   - socket → always socket
//   - fetch  → always fetch
//   - hybrid → always socket initially. If the Worker rejects with 4001
//     (ErrUpstreamBlocked) because the target is CF-hosted, the
//     server layer byte-sniffs the client's first payload: promotes
//     to fetch only when the bytes look like HTTP. Non-HTTP streams
//     (TLS, SSH, Redis, raw TCP) close cleanly instead of silent
//     corruption through HTTP fetch wrapping.
func PickMode(configured, host string, port int) string {
	switch configured {
	case ModeSocket, "":
		return ModeSocket
	case ModeFetch:
		return ModeFetch
	case ModeHybrid:
		return ModeSocket
	}
	return ModeSocket
}
