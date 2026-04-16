package proxy_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Vozec/flarex/internal/filter"
	"github.com/Vozec/flarex/internal/pool"
	"github.com/Vozec/flarex/internal/proxy"
	"github.com/Vozec/flarex/internal/scheduler"
)

func BenchmarkE2E(b *testing.B) {
	wsrv := mockWorkerServerBench(b)
	target := targetServerBench(b)

	tu, _ := url.Parse(target.URL)
	_, tps, _ := net.SplitHostPort(tu.Host)
	tport, _ := strconv.Atoi(tps)

	w := pool.NewWorker("acc", "mock", wsrv.URL)
	p := pool.New([]*pool.Worker{w})
	sched := scheduler.NewRoundRobin(p)
	filt, _ := filter.NewIPFilter(nil, []any{tport, 80, 443, 8080, 8443})

	socksLn, _ := net.Listen("tcp", "127.0.0.1:0")
	defer socksLn.Close()

	server := &proxy.Server{
		Filter: filt, Scheduler: sched, Pool: p,
		HMACSecret: hmacSecret, MaxRetries: 1, BaseBackoff: 10 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.Serve(ctx, socksLn)

	addr := socksLn.Addr().String()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := doOne(addr, "localhost", tport); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func doOne(socksAddr, host string, port int) error {
	c, err := net.DialTimeout("tcp", socksAddr, 3*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := socks5Connect(c, host, port); err != nil {
		return err
	}
	req := fmt.Sprintf("GET / HTTP/1.1\r\nHost: localhost:%d\r\nConnection: close\r\n\r\n", port)
	if _, err := c.Write([]byte(req)); err != nil {
		return err
	}
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

func BenchmarkThroughput(b *testing.B) {
	for _, conc := range []int{1, 10, 50, 200} {
		b.Run(fmt.Sprintf("conc=%d", conc), func(b *testing.B) {
			runThroughput(b, conc)
		})
	}
}

func runThroughput(b *testing.B, concurrency int) {
	wsrv := mockWorkerServerBench(b)
	target := targetServerBench(b)
	tu, _ := url.Parse(target.URL)
	_, tps, _ := net.SplitHostPort(tu.Host)
	tport, _ := strconv.Atoi(tps)

	w := pool.NewWorker("acc", "mock", wsrv.URL)
	p := pool.New([]*pool.Worker{w})
	sched := scheduler.NewRoundRobin(p)
	filt, _ := filter.NewIPFilter(nil, []any{tport, 80, 443, 8080, 8443})
	socksLn, _ := net.Listen("tcp", "127.0.0.1:0")
	defer socksLn.Close()
	server := &proxy.Server{Filter: filt, Scheduler: sched, Pool: p, HMACSecret: hmacSecret, MaxRetries: 1, BaseBackoff: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.Serve(ctx, socksLn)
	addr := socksLn.Addr().String()

	var ok, fail atomic.Uint64
	var wg sync.WaitGroup
	stop := make(chan struct{})

	b.ResetTimer()
	t0 := time.Now()
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := doOne(addr, "localhost", tport); err != nil {
					fail.Add(1)
				} else {
					ok.Add(1)
				}
			}
		}()
	}
	time.Sleep(time.Duration(b.N) * time.Millisecond * 10)
	close(stop)
	wg.Wait()
	elapsed := time.Since(t0)
	b.ReportMetric(float64(ok.Load())/elapsed.Seconds(), "rps")
	b.ReportMetric(float64(fail.Load()), "fail_total")
	b.ReportMetric(float64(ok.Load()), "ok_total")
}

func mockWorkerServerBench(tb testing.TB) *testServer {
	return spinMockWorker(tb)
}
func targetServerBench(tb testing.TB) *testServer {
	return spinTarget(tb)
}

type testServer struct {
	URL string
	ln  net.Listener
	srv *http.Server
}
