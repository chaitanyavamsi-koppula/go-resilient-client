# go-resilient-client

A production-grade resilient HTTP client in Go implementing three core reliability patterns used in distributed systems.

## Patterns Implemented

### 1. Retry with Exponential Backoff + Full Jitter
Failed requests are retried up to `MaxAttempts` times. Each retry waits longer than the last (exponential backoff), with randomized jitter to prevent thundering herd — a problem where many clients retry simultaneously and overwhelm a recovering service.

```
delay = random(0, min(maxDelay, baseDelay * 2^attempt))
```

### 2. Circuit Breaker (Closed → Open → Half-Open)
Tracks consecutive failures. After `FailureThreshold` failures, the circuit trips **OPEN** and immediately rejects requests without hitting the downstream service. After `ResetTimeout`, it enters **HALF-OPEN** and allows one probe request through. If the probe succeeds, the circuit closes and normal traffic resumes.

```
CLOSED ──(5 failures)──► OPEN ──(5s timeout)──► HALF-OPEN ──(success)──► CLOSED
                                                              └──(failure)──► OPEN
```

### 3. Idempotency Cache
Responses are cached by a SHA-256 hash of `(method + URL + body)`. Identical requests within the TTL window return the cached response without touching the network. Expired entries are evicted in a background goroutine.

## Project Structure

```
├── client.go          # ResilientClient — composes all three patterns
├── circuit_breaker.go # State machine: closed/open/half-open
├── retry.go           # Exponential backoff + full jitter
├── idempotency.go     # In-memory cache with TTL eviction
└── main.go            # Demo: flaky test server + all patterns in action
```

## Run the Demo

```bash
git clone https://github.com/chaitanyavamsi-koppula/go-resilient-client.git
cd go-resilient-client
go run .
```

The demo spins up a local test server that returns 500s ~60% of the time and walks through four scenarios:

| Round | What it shows |
|-------|--------------|
| 1 | Retry fires on 5xx, idempotency cache takes over after first success |
| 2 | Identical request returns cached response without hitting the server |
| 3 | Circuit trips OPEN after 5 failures, subsequent calls rejected instantly |
| 4 | Circuit resets to HALF-OPEN after timeout, closes on successful probe |

## Configuration

```go
cfg := DefaultConfig()
// cfg.MaxAttempts      = 3               // retry attempts per request
// cfg.BaseDelay        = 100ms           // starting backoff delay
// cfg.MaxDelay         = 2s              // backoff ceiling
// cfg.FailureThreshold = 5               // failures before circuit opens
// cfg.ResetTimeout     = 10s             // how long before half-open probe
// cfg.IdempotencyTTL   = 30s             // cache entry lifetime

client := NewResilientClient(cfg)
```

## Why These Three Together

In production systems, retries alone can make things worse — if a service is down, retrying floods it further. The circuit breaker stops that. Idempotency ensures that retries of non-idempotent operations (like POST requests) don't cause duplicate side effects. Together they form the standard reliability layer used in microservice clients.

## Requirements

Go 1.21+. No external dependencies.
