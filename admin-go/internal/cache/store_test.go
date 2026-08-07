package cache

import (
	"sync"
	"testing"
	"time"
)

func TestStoreBoundsAndLRU(t *testing.T) {
	store := New[string](2, 7, time.Minute)
	one := Key{Token: "one"}
	two := Key{Token: "two"}
	three := Key{Token: "three"}
	if !store.Set(one, []string{"one"}, 3) || !store.Set(two, []string{"two"}, 3) {
		t.Fatal("Set unexpectedly rejected")
	}
	if _, ok := store.Get(one); !ok {
		t.Fatal("first entry missing")
	}
	store.Set(three, []string{"three"}, 3)
	if _, ok := store.Get(two); ok {
		t.Fatal("least recently used entry was not evicted")
	}
	if store.Set(Key{Token: "large"}, []string{"too large"}, 8) {
		t.Fatal("oversized entry was accepted")
	}
	stats := store.Stats()
	if stats.Entries != 2 || stats.Bytes != 6 || stats.CapacityEvictions != 1 || stats.CapacityEntries != 2 || stats.CapacityBytes != 7 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestStoreExpiryAndTokenInvalidation(t *testing.T) {
	store := New[string](4, 100, time.Minute)
	now := time.Unix(100, 0)
	store.now = func() time.Time { return now }
	local := Key{Token: "token", Domain: "workload"}
	remote := Key{Token: "token", Cluster: "member", Domain: "workload"}
	store.Set(local, []string{"local"}, 5)
	store.Set(remote, []string{"remote"}, 6)
	store.DeleteToken("token")
	if _, ok := store.Get(local); ok {
		t.Fatal("local token entry survived invalidation")
	}
	store.Set(local, []string{"local"}, 5)
	now = now.Add(time.Minute)
	if _, ok := store.Get(local); ok {
		t.Fatal("expired entry was returned")
	}
}

func TestStoreConcurrentAccess(t *testing.T) {
	store := New[int](16, 1024, time.Minute)
	var group sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		group.Add(1)
		go func(id int) {
			defer group.Done()
			for index := 0; index < 100; index++ {
				key := Key{Token: string(rune('a' + id)), Cluster: string(rune('a' + index%2))}
				store.Set(key, []int{index}, 8)
				store.Get(key)
			}
		}(worker)
	}
	group.Wait()
}

func TestInvalidatorsBroadcastTokenDeletion(t *testing.T) {
	first := New[string](2, 100, time.Minute)
	second := New[int](2, 100, time.Minute)
	key := Key{Token: "token"}
	first.Set(key, []string{"value"}, 5)
	second.Set(key, []int{1}, 8)

	Invalidators{first, second}.DeleteToken("token")
	if _, found := first.Get(key); found {
		t.Fatal("first cache survived broadcast invalidation")
	}
	if _, found := second.Get(key); found {
		t.Fatal("second cache survived broadcast invalidation")
	}
}
