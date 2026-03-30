package main

import (
	"errors"
	"sync"
	"time"
)

type CBState int

const (
	StateClosed   CBState = iota // normal operation, requests allowed
	StateOpen                    // tripped, requests blocked
	StateHalfOpen               // probe state, one request allowed through
)

var ErrCircuitOpen = errors.New("circuit breaker is open")

type CircuitBreaker struct {
	mu              sync.Mutex
	state           CBState
	failureCount    int
	failureThreshold int
	successThreshold int // successes needed in half-open to close
	halfOpenSuccess int
	openedAt        time.Time
	resetTimeout    time.Duration
}

func NewCircuitBreaker(failureThreshold int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:            StateClosed,
		failureThreshold: failureThreshold,
		successThreshold: 1,
		resetTimeout:     resetTimeout,
	}
}

func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return nil
	case StateOpen:
		if time.Since(cb.openedAt) >= cb.resetTimeout {
			cb.state = StateHalfOpen
			cb.halfOpenSuccess = 0
			return nil
		}
		return ErrCircuitOpen
	case StateHalfOpen:
		// only one probe at a time — block subsequent calls until probe resolves
		return ErrCircuitOpen
	}
	return nil
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateHalfOpen:
		cb.halfOpenSuccess++
		if cb.halfOpenSuccess >= cb.successThreshold {
			cb.state = StateClosed
			cb.failureCount = 0
		}
	case StateClosed:
		cb.failureCount = 0
	}
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		cb.failureCount++
		if cb.failureCount >= cb.failureThreshold {
			cb.state = StateOpen
			cb.openedAt = time.Now()
		}
	case StateHalfOpen:
		// probe failed — back to open
		cb.state = StateOpen
		cb.openedAt = time.Now()
	}
}

func (cb *CircuitBreaker) State() CBState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

func (cb *CircuitBreaker) StateName() string {
	switch cb.State() {
	case StateClosed:
		return "CLOSED"
	case StateOpen:
		return "OPEN"
	case StateHalfOpen:
		return "HALF-OPEN"
	}
	return "UNKNOWN"
}
