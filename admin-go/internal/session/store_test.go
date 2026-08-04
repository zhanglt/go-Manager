package session

import (
	"strconv"
	"sync"
	"testing"
)

func TestStoreEvictsOldestEntry(t *testing.T) {
	store := NewStore(2)
	store.Put("first", "one")
	store.Put("second", "two")
	store.Put("third", "three")
	if _, ok := store.Get("first"); ok {
		t.Fatal("oldest entry was not evicted")
	}
	if entry, ok := store.Get("third"); !ok || entry.SUSEToken != "three" {
		t.Fatalf("third entry = %+v, %v", entry, ok)
	}
}

func TestStoreConcurrentAccess(t *testing.T) {
	store := NewStore(32)
	var wait sync.WaitGroup
	for i := 0; i < 100; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			token := strconv.Itoa(index)
			store.Put(token, token)
			store.Get(token)
			if index%2 == 0 {
				store.Delete(token)
			}
		}(i)
	}
	wait.Wait()
	if store.Len() > 32 {
		t.Fatalf("store length = %d, exceeds capacity", store.Len())
	}
}

func TestStorePreservesClusterWhenSessionIsRefreshed(t *testing.T) {
	store := NewStore(2)
	store.SetCluster("token", "member/one")
	store.Put("token", "cookie")
	entry, ok := store.Get("token")
	if !ok || entry.ClusterID != "member/one" || entry.SUSEToken != "cookie" {
		t.Fatalf("entry = %+v, %v", entry, ok)
	}
}

func TestStoreCopiesTokenJSON(t *testing.T) {
	store := NewStore(1)
	value := []byte(`{"token":"value"}`)
	store.SetTokenJSON("token", value)
	value[0] = 'x'
	stored, ok := store.TokenJSON("token")
	if !ok || string(stored) != `{"token":"value"}` {
		t.Fatalf("stored token = %q, %v", stored, ok)
	}
	stored[0] = 'x'
	again, _ := store.TokenJSON("token")
	if string(again) != `{"token":"value"}` {
		t.Fatalf("TokenJSON returned mutable storage: %q", again)
	}
}

func TestTransactionTargetKeepsInitialControllerUntilCleared(t *testing.T) {
	store := NewStore(2)
	store.SetCluster("token", "member-one")
	api, cluster := store.EnsureTransactionTarget("token", "v1")
	if api != "v1" || cluster != "member-one" {
		t.Fatalf("initial target = %q %q", api, cluster)
	}
	store.SetCluster("token", "member-two")
	api, cluster = store.EnsureTransactionTarget("token", "v2")
	if api != "v1" || cluster != "member-one" {
		t.Fatalf("cached target = %q %q", api, cluster)
	}
	store.ClearTransactionTarget("token")
	api, cluster = store.EnsureTransactionTarget("token", "v2")
	if api != "v2" || cluster != "member-two" {
		t.Fatalf("new target = %q %q", api, cluster)
	}
}
