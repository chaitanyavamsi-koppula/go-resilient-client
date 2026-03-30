package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ResilientClient wraps http.Client with retry, circuit breaking, and idempotency.
type ResilientClient struct {
	http       *http.Client
	retry      RetryConfig
	cb         *CircuitBreaker
	idempotent *IdempotencyStore
}

type Config struct {
	// Retry
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration

	// Circuit breaker
	FailureThreshold int
	ResetTimeout     time.Duration

	// Idempotency cache TTL (0 = disabled)
	IdempotencyTTL time.Duration
}

func DefaultConfig() Config {
	return Config{
		MaxAttempts:      3,
		BaseDelay:        100 * time.Millisecond,
		MaxDelay:         2 * time.Second,
		FailureThreshold: 5,
		ResetTimeout:     10 * time.Second,
		IdempotencyTTL:   30 * time.Second,
	}
}

func NewResilientClient(cfg Config) *ResilientClient {
	return &ResilientClient{
		http:  &http.Client{Timeout: 5 * time.Second},
		retry: RetryConfig{MaxAttempts: cfg.MaxAttempts, BaseDelay: cfg.BaseDelay, MaxDelay: cfg.MaxDelay},
		cb:    NewCircuitBreaker(cfg.FailureThreshold, cfg.ResetTimeout),
		idempotent: NewIdempotencyStore(cfg.IdempotencyTTL),
	}
}

// Do executes the request through all three resilience layers:
//  1. Idempotency check — return cached response if available
//  2. Circuit breaker  — block if downstream is known to be unhealthy
//  3. Retry            — retry with exponential backoff + jitter on failure
func (c *ResilientClient) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	// --- idempotency ---
	key, err := requestKey(req)
	if err != nil {
		return nil, fmt.Errorf("idempotency key: %w", err)
	}
	if cached, ok := c.idempotent.Get(key); ok {
		fmt.Printf("[idempotency] cache hit — returning cached %d\n", cached.statusCode)
		return buildCachedResponse(cached), nil
	}

	// --- circuit breaker check ---
	if err := c.cb.Allow(); err != nil {
		return nil, fmt.Errorf("[circuit breaker] %s: %w", c.cb.StateName(), err)
	}

	// --- retry loop ---
	resp, err := doWithRetry(ctx, c.retry, func() (*http.Response, error) {
		// clone the request for each attempt (body may have been consumed)
		cloned, cloneErr := cloneRequest(ctx, req)
		if cloneErr != nil {
			return nil, cloneErr
		}
		return c.http.Do(cloned)
	})

	if err != nil || (resp != nil && resp.StatusCode >= 500) {
		c.cb.RecordFailure()
		fmt.Printf("[circuit breaker] failure recorded — state: %s\n", c.cb.StateName())
		return resp, err
	}

	c.cb.RecordSuccess()

	// cache the response body for idempotency
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr == nil {
		c.idempotent.Set(key, resp.StatusCode, body)
		resp.Body = io.NopCloser(bytes.NewReader(body))
	}

	return resp, nil
}

// cloneRequest creates a fresh *http.Request so each retry has its own body reader.
func cloneRequest(ctx context.Context, orig *http.Request) (*http.Request, error) {
	cloned := orig.Clone(ctx)
	if orig.Body == nil {
		return cloned, nil
	}
	body, err := io.ReadAll(orig.Body)
	if err != nil {
		return nil, err
	}
	orig.Body = io.NopCloser(bytes.NewReader(body))
	cloned.Body = io.NopCloser(bytes.NewReader(body))
	return cloned, nil
}

func buildCachedResponse(c cachedResponse) *http.Response {
	return &http.Response{
		StatusCode: c.statusCode,
		Body:       io.NopCloser(bytes.NewReader(c.body)),
	}
}
