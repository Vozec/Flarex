package metrics

import (
	"fmt"

	vm "github.com/VictoriaMetrics/metrics"
)

var (
	ConnectionsTotal   = vm.NewCounter(`cft_connections_total`)
	ConnectionsActive  = vm.NewCounter(`cft_connections_active`)
	FilterDenied       = vm.NewCounter(`cft_filter_denied_total`)
	DialFailTotal      = vm.NewCounter(`cft_dial_fail_total`)
	DialSuccessTotal   = vm.NewCounter(`cft_dial_success_total`)
	HandshakeFailTotal = vm.NewCounter(`cft_handshake_fail_total`)
	BytesUpstream      = vm.NewCounter(`cft_bytes_upstream_total`)
	BytesDownstream    = vm.NewCounter(`cft_bytes_downstream_total`)
	FetchFallbackTotal = vm.NewCounter(`cft_fetch_fallback_total`)
	HedgeFiredTotal    = vm.NewCounter(`cft_dial_hedge_fired_total`)
	HedgeWinsTotal     = vm.NewCounter(`cft_dial_hedge_wins_total`)

	DialLatencyHist = vm.NewHistogram(`cft_dial_latency_seconds`)
	ReqLatencyHist  = vm.NewHistogram(`cft_request_duration_seconds`)
)

func WorkerReq(name string) *vm.Counter {
	return vm.GetOrCreateCounter(fmt.Sprintf(`cft_worker_requests_total{name=%q}`, name))
}
