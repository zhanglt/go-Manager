package cache

import (
	"container/list"
	"sync"
	"time"
)

type Key struct {
	Token   string
	Cluster string
	Domain  string
}

type entry[V any] struct {
	key       Key
	value     []V
	size      int64
	expiresAt time.Time
}

type Store[V any] struct {
	mu                sync.Mutex
	entries           map[Key]*list.Element
	lru               *list.List
	maxEntries        int
	maxBytes          int64
	ttl               time.Duration
	usedBytes         int64
	capacityEvictions uint64
	now               func() time.Time
}

type Stats struct {
	Entries           int
	Bytes             int64
	CapacityEntries   int
	CapacityBytes     int64
	CapacityEvictions uint64
}

type TokenInvalidator interface {
	DeleteToken(string)
}

type Invalidators []TokenInvalidator

func (invalidators Invalidators) DeleteToken(token string) {
	for _, invalidator := range invalidators {
		if invalidator != nil {
			invalidator.DeleteToken(token)
		}
	}
}

func New[V any](maxEntries int, maxBytes int64, ttl time.Duration) *Store[V] {
	if maxEntries < 1 || maxBytes < 1 || ttl <= 0 {
		panic("cache limits must be positive")
	}
	return &Store[V]{
		entries: make(map[Key]*list.Element), lru: list.New(), maxEntries: maxEntries,
		maxBytes: maxBytes, ttl: ttl, now: time.Now,
	}
}

func (s *Store[V]) Set(key Key, value []V, size int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpired()
	s.delete(key)
	if size < 0 || size > s.maxBytes {
		return false
	}
	copyOfValue := append([]V(nil), value...)
	element := s.lru.PushFront(entry[V]{key: key, value: copyOfValue, size: size, expiresAt: s.now().Add(s.ttl)})
	s.entries[key] = element
	s.usedBytes += size
	for len(s.entries) > s.maxEntries || s.usedBytes > s.maxBytes {
		s.removeElement(s.lru.Back())
		s.capacityEvictions++
	}
	return s.entries[key] != nil
}

func (s *Store[V]) Get(key Key) ([]V, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	element, ok := s.entries[key]
	if !ok {
		return nil, false
	}
	current := element.Value.(entry[V])
	if !current.expiresAt.After(s.now()) {
		s.removeElement(element)
		return nil, false
	}
	s.lru.MoveToFront(element)
	return append([]V(nil), current.value...), true
}

// Take returns a cached value exactly once. It is used for short-lived login handoffs.
func (s *Store[V]) Take(key Key) ([]V, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	element, ok := s.entries[key]
	if !ok {
		return nil, false
	}
	current := element.Value.(entry[V])
	s.removeElement(element)
	if !current.expiresAt.After(s.now()) {
		return nil, false
	}
	return append([]V(nil), current.value...), true
}

func (s *Store[V]) Delete(key Key) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delete(key)
}

func (s *Store[V]) DeleteToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, element := range s.entries {
		if key.Token == token {
			s.removeElement(element)
		}
	}
}

func (s *Store[V]) MaxBytes() int64 {
	return s.maxBytes
}

func (s *Store[V]) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpired()
	return Stats{
		Entries: len(s.entries), Bytes: s.usedBytes, CapacityEntries: s.maxEntries,
		CapacityBytes: s.maxBytes, CapacityEvictions: s.capacityEvictions,
	}
}

func (s *Store[V]) delete(key Key) {
	if element, ok := s.entries[key]; ok {
		s.removeElement(element)
	}
}

func (s *Store[V]) removeExpired() {
	now := s.now()
	for _, element := range s.entries {
		if !element.Value.(entry[V]).expiresAt.After(now) {
			s.removeElement(element)
		}
	}
}

func (s *Store[V]) removeElement(element *list.Element) {
	if element == nil {
		return
	}
	current := element.Value.(entry[V])
	delete(s.entries, current.key)
	s.usedBytes -= current.size
	s.lru.Remove(element)
}
