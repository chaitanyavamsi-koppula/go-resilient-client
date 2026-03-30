package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"sync"
	"time"
)

type cachedResponse struct {
	statusCode int
	body       []byte
	storedAt   time.Time
}

// IdempotencyStore caches responses keyed by a hash of (method + URL + body).
// Safe for concurrent use. Entries expire after ttl.
type IdempotencyStore struct {
	mu    sync.RWMutex
	cache map[string]cachedResponse
	ttl   time.Duration
}

func NewIdempotencyStore(ttl time.Duration) *IdempotencyStore {
	s := &IdempotencyStore{
		cache: make(map[string]cachedResponse),
		ttl:   ttl,
	}
	go s.evict()
	return s
}

// requestKey produces a stable hash for a request.
// Body is read and restored so the caller can still send the request.
func requestKey(req *http.Request) (string, error) {
	h := sha256.New()
	h.Write([]byte(req.Method))
	h.Write([]byte(req.URL.String()))

	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return "", err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		h.Write(body)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *IdempotencyStore) Get(key string) (cachedResponse, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.cache[key]
	if !ok || time.Since(entry.storedAt) > s.ttl {
		return cachedResponse{}, false
	}
	return entry, true
}

func (s *IdempotencyStore) Set(key string, statusCode int, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[key] = cachedResponse{
		statusCode: statusCode,
		body:       body,
		storedAt:   time.Now(),
	}
}

// evict runs periodically to remove expired entries and prevent unbounded growth.
func (s *IdempotencyStore) evict() {
	ticker := time.NewTicker(s.ttl)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		for k, v := range s.cache {
			if time.Since(v.storedAt) > s.ttl {
				delete(s.cache, k)
			}
		}
		s.mu.Unlock()
	}
}
