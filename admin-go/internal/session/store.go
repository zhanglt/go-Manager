package session

import "sync"

type Entry struct {
	SUSEToken          string
	ClusterID          string
	TokenJSON          []byte
	TransactionAPI     string
	TransactionCluster string
	sequence           uint64
}

func (s *Store) EnsureTransactionTarget(token, api string) (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.entries[token]
	if entry.TransactionAPI == "" {
		entry.TransactionAPI = api
		entry.TransactionCluster = entry.ClusterID
	}
	s.sequence++
	entry.sequence = s.sequence
	s.entries[token] = entry
	s.evictOldest()
	return entry.TransactionAPI, entry.TransactionCluster
}

func (s *Store) ClearTransactionTarget(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[token]
	if !ok {
		return
	}
	entry.TransactionAPI = ""
	entry.TransactionCluster = ""
	s.entries[token] = entry
}

type Store struct {
	mu       sync.RWMutex
	entries  map[string]Entry
	max      int
	sequence uint64
}

func NewStore(maxEntries int) *Store {
	if maxEntries < 1 {
		panic("session store capacity must be positive")
	}
	return &Store{entries: make(map[string]Entry), max: maxEntries}
}

func (s *Store) Put(token, suseToken string) {
	if token == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	entry := s.entries[token]
	entry.SUSEToken = suseToken
	entry.sequence = s.sequence
	s.entries[token] = entry
	s.evictOldest()
}

func (s *Store) SetCluster(token, clusterID string) {
	if token == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	entry := s.entries[token]
	entry.ClusterID = clusterID
	entry.sequence = s.sequence
	s.entries[token] = entry
	s.evictOldest()
}

func (s *Store) Cluster(token string) (string, bool) {
	entry, ok := s.Get(token)
	return entry.ClusterID, ok && entry.ClusterID != ""
}

func (s *Store) SetTokenJSON(token string, value []byte) {
	if token == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	entry := s.entries[token]
	entry.TokenJSON = append(entry.TokenJSON[:0], value...)
	entry.sequence = s.sequence
	s.entries[token] = entry
	s.evictOldest()
}

func (s *Store) TokenJSON(token string) ([]byte, bool) {
	entry, ok := s.Get(token)
	if !ok || entry.TokenJSON == nil {
		return nil, false
	}
	return append([]byte(nil), entry.TokenJSON...), true
}

func (s *Store) evictOldest() {
	if len(s.entries) <= s.max {
		return
	}
	var oldestToken string
	oldestSequence := ^uint64(0)
	for candidate, entry := range s.entries {
		if entry.sequence < oldestSequence {
			oldestToken = candidate
			oldestSequence = entry.sequence
		}
	}
	delete(s.entries, oldestToken)
}

func (s *Store) Get(token string) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.entries[token]
	return entry, ok
}

func (s *Store) Delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, token)
}

func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}
