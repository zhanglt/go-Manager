package observability

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRegistryExportsBoundedOperationalMetrics(t *testing.T) {
	registry := NewRegistry()
	registry.ObserveRequest(http.MethodPost, "/auth", http.StatusUnauthorized, 75*time.Millisecond)
	registry.ObserveController(http.MethodGet, 0, 20*time.Millisecond, errors.New("unavailable"))
	registry.RegisterCache("test", func() CacheSnapshot {
		return CacheSnapshot{Entries: 2, Bytes: 12, CapacityEntries: 4, CapacityBytes: 64, CapacityEvictions: 1}
	})
	registry.RegisterSession(func() (int, int) { return 3, 10 })
	registry.SupportStarted()
	registry.SupportFinished("success", time.Second)
	registry.SupportDownloaded(42)

	response := httptest.NewRecorder()
	registry.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	for _, want := range []string{
		`manager_http_requests_total{method="POST",route="/auth",status="401"} 1`,
		`manager_login_failures_total{route="/auth"} 1`,
		`manager_controller_failures_total{reason="transport"} 1`,
		`manager_cache_evictions_total{cache="test"} 1`,
		`manager_sessions 3`,
		`manager_support_operations_total{outcome="success"} 1`,
		`manager_support_download_bytes_total 42`,
		`go_goroutines `,
		`process_resident_memory_bytes `,
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("metrics do not contain %q:\n%s", want, response.Body.String())
		}
	}
	if strings.Count(response.Body.String(), "# HELP manager_http_request_duration_seconds ") != 1 {
		t.Error("HTTP histogram metadata must be emitted exactly once")
	}
}

func TestRegistryRejectsNonGET(t *testing.T) {
	response := httptest.NewRecorder()
	NewRegistry().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", response.Code)
	}
}
