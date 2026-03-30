package main

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"time"
)

func main() {
	// --- flaky test server ---
	// Returns 500 ~60% of the time to trigger retries and circuit breaking.
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if rand.Float32() < 0.6 {
			fmt.Printf("[server] request #%d → 500\n", callCount)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, `{"error":"internal server error"}`)
			return
		}
		fmt.Printf("[server] request #%d → 200\n", callCount)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"message":"ok","call":%d}`, callCount)
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.MaxAttempts = 3
	cfg.FailureThreshold = 5
	cfg.ResetTimeout = 5 * time.Second

	client := NewResilientClient(cfg)

	fmt.Println("=== Demo: Retry + Circuit Breaker + Idempotency ===")
	fmt.Println()

	// Round 1: normal requests — watch retries in action
	fmt.Println("--- Round 1: 10 requests, expect retries ---")
	for i := 1; i <= 10; i++ {
		send(client, server.URL, i)
		time.Sleep(50 * time.Millisecond)
	}

	fmt.Println()
	fmt.Printf("Circuit breaker state after round 1: %s\n", client.cb.StateName())
	fmt.Println()

	// Round 2: idempotency — same URL → same response from cache
	fmt.Println("--- Round 2: same request twice — second should hit cache ---")
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/stable", nil)
	send(client, server.URL+"/stable", 1)
	_ = req
	send(client, server.URL+"/stable", 2)

	fmt.Println()

	// Round 3: trip the circuit breaker by pointing at a dead server
	fmt.Println("--- Round 3: dead server — watch circuit trip ---")
	for i := 1; i <= 8; i++ {
		send(client, "http://localhost:19999", i) // nothing listening here
		time.Sleep(20 * time.Millisecond)
	}

	fmt.Println()
	fmt.Printf("Circuit breaker state: %s\n", client.cb.StateName())
	fmt.Println()

	// Round 4: wait for reset timeout, watch half-open → closed transition.
	// Use a unique URL so the idempotency cache doesn't short-circuit the probe.
	fmt.Printf("Waiting %s for circuit breaker reset...\n", cfg.ResetTimeout)
	time.Sleep(cfg.ResetTimeout + 500*time.Millisecond)
	fmt.Printf("Circuit breaker state after wait: %s (will move to HALF-OPEN on next Allow())\n", client.cb.StateName())
	fmt.Println("--- Round 4: probe request after reset ---")
	send(client, server.URL+"?probe=1", 1) // unique URL bypasses idempotency cache
	fmt.Printf("Circuit breaker state after probe: %s\n", client.cb.StateName())
}

func send(client *ResilientClient, url string, n int) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		fmt.Printf("[client] request %d: build error: %v\n", n, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Do(ctx, req)
	if err != nil {
		fmt.Printf("[client] request %d: error: %v\n", n, err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("[client] request %d: %d — %s\n", n, resp.StatusCode, body)
}
