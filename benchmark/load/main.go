// A small bounded HTTP load driver; measure gateway CPU/RSS externally.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	url := flag.String("url", "", "HTTP endpoint")
	body := flag.String("body", `{}`, "JSON request body")
	duration := flag.Duration("duration", 30*time.Second, "duration")
	workers := flag.Int("concurrency", 16, "workers")
	flag.Parse()
	if *url == "" || *workers < 1 || *duration <= 0 {
		panic("url, positive duration/concurrency required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()
	var mu sync.Mutex
	var durations []float64
	var failures atomic.Int64
	var count atomic.Int64
	transport := &http.Transport{MaxIdleConns: *workers, MaxIdleConnsPerHost: *workers}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Go(func() {
			for ctx.Err() == nil {
				begin := time.Now()
				req, e := http.NewRequestWithContext(ctx, "POST", *url, bytes.NewBufferString(*body))
				if e != nil {
					panic(e)
				}
				req.Header.Set("Content-Type", "application/json")
				r, e := client.Do(req)
				if ctx.Err() != nil {
					if r != nil {
						r.Body.Close()
					}
					return
				}
				if e != nil {
					failures.Add(1)
				} else {
					_, readErr := io.Copy(io.Discard, r.Body)
					r.Body.Close()
					if r.StatusCode >= 400 || readErr != nil {
						failures.Add(1)
					}
				}
				count.Add(1)
				mu.Lock()
				durations = append(durations, float64(time.Since(begin))/float64(time.Millisecond))
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	sort.Float64s(durations)
	pct := func(p float64) float64 {
		if len(durations) == 0 {
			return 0
		}
		return durations[int(float64(len(durations)-1)*p)]
	}
	rate := float64(0)
	if count.Load() > 0 {
		rate = float64(failures.Load()) / float64(count.Load())
	}
	out := map[string]any{"requests": count.Load(), "errors": failures.Load(), "errorRate": rate, "qps": float64(count.Load()) / elapsed, "p50ms": pct(.5), "p95ms": pct(.95), "p99ms": pct(.99), "seconds": elapsed}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
}
