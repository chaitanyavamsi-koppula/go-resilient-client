package main

import (
	"context"
	"math"
	"math/rand"
	"net/http"
	"time"
)

type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	// RetryOn decides whether a response warrants a retry.
	// Defaults to retrying on 5xx and network errors if nil.
	RetryOn func(resp *http.Response, err error) bool
}

func defaultRetryOn(resp *http.Response, err error) bool {
	if err != nil {
		return true
	}
	return resp.StatusCode >= 500
}

// exponentialBackoff returns the delay for attempt n (0-indexed) with full jitter.
// Formula: random(0, min(maxDelay, base * 2^n))
func exponentialBackoff(base, maxDelay time.Duration, attempt int) time.Duration {
	exp := math.Pow(2, float64(attempt))
	delay := time.Duration(float64(base) * exp)
	if delay > maxDelay {
		delay = maxDelay
	}
	// full jitter — avoids thundering herd when many clients retry simultaneously
	jitter := time.Duration(rand.Int63n(int64(delay) + 1))
	return jitter
}

// doWithRetry executes fn with retry logic defined by cfg.
// It is decoupled from the HTTP client so it can be unit-tested independently.
func doWithRetry(ctx context.Context, cfg RetryConfig, fn func() (*http.Response, error)) (*http.Response, error) {
	if cfg.RetryOn == nil {
		cfg.RetryOn = defaultRetryOn
	}

	var (
		resp *http.Response
		err  error
	)

	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		resp, err = fn()

		if !cfg.RetryOn(resp, err) {
			return resp, err
		}

		// close body to avoid connection leak before retry
		if resp != nil {
			resp.Body.Close()
		}

		if attempt == cfg.MaxAttempts-1 {
			break
		}

		delay := exponentialBackoff(cfg.BaseDelay, cfg.MaxDelay, attempt)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}

	return resp, err
}
