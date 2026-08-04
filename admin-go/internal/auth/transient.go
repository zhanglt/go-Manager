package auth

import (
	"sync"
	"time"
)

type transientValue[T any] struct {
	value     T
	expiresAt time.Time
}

// transientStore keeps short-lived SSO capabilities in process memory and
// atomically removes them when consumed.
type transientStore[T any] struct {
	mu         sync.Mutex
	values     map[string]transientValue[T]
	maxEntries int
	ttl        time.Duration
	now        func() time.Time
}

func newTransientStore[T any](maxEntries int, ttl time.Duration) *transientStore[T] {
	return &transientStore[T]{
		values:     make(map[string]transientValue[T]),
		maxEntries: maxEntries,
		ttl:        ttl,
		now:        time.Now,
	}
}

func (s *transientStore[T]) put(key string, value T) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpiredLocked()
	if key == "" || len(s.values) >= s.maxEntries {
		return false
	}
	s.values[key] = transientValue[T]{value: value, expiresAt: s.now().Add(s.ttl)}
	return true
}

func (s *transientStore[T]) take(key string) (T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, found := s.values[key]
	if found {
		delete(s.values, key)
	}
	if !found || !s.now().Before(entry.expiresAt) {
		var zero T
		return zero, false
	}
	return entry.value, true
}

func (s *transientStore[T]) removeExpiredLocked() {
	now := s.now()
	for key, entry := range s.values {
		if !now.Before(entry.expiresAt) {
			delete(s.values, key)
		}
	}
}
