package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/neuvector/manager/admin-go/internal/config"
	"github.com/neuvector/manager/admin-go/internal/controller"
)

func TestCompatibilityRoutes(t *testing.T) {
	controllerServer := httptest.NewServer(http.NotFoundHandler())
	defer controllerServer.Close()
	baseURL, _ := url.Parse(controllerServer.URL + "/v1")
	cfg := testConfig(baseURL)
	cfg.Server.PathPrefix = "/manager"
	cfg.UI.GravatarEnabled = "true"
	cfg.UI.CustomPolicy = "policy"
	handler := NewHandler(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), controller.NewWithHTTPClient(baseURL, controllerServer.Client()))

	for _, test := range []struct {
		name       string
		path       string
		token      bool
		wantStatus int
		wantBody   string
	}{
		{"gravatar", "/manager/gravatar", false, http.StatusOK, "true"},
		{"rebrand", "/manager/rebrand", false, http.StatusOK, `"customPolicy":"policy"`},
		{"version", "/manager/version", true, http.StatusOK, "interim/master.xxxx"},
		{"version token required", "/manager/version", false, http.StatusBadRequest, "Token header is required"},
		{"prefix required", "/gravatar", false, http.StatusNotFound, "404"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			if test.token {
				request.Header.Set("Token", "test-token")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), test.wantBody) {
				t.Fatalf("response = %d %q, want %d containing %q", response.Code, response.Body.String(), test.wantStatus, test.wantBody)
			}
			for name, want := range map[string]string{
				"X-Frame-Options": "SAMEORIGIN", "X-Content-Type-Options": "nosniff",
			} {
				if got := response.Header().Get(name); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
		})
	}
}

func TestHealthHandler(t *testing.T) {
	handler := NewHealthHandler(func() bool { return false })
	for path, want := range map[string]int{"/livez": http.StatusOK, "/readyz": http.StatusServiceUnavailable} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != want {
			t.Errorf("GET %s = %d, want %d", path, response.Code, want)
		}
	}
}

func TestRequestBodyLimit(t *testing.T) {
	controllerServer := httptest.NewServer(http.NotFoundHandler())
	defer controllerServer.Close()
	baseURL, _ := url.Parse(controllerServer.URL + "/v1")
	cfg := testConfig(baseURL)
	cfg.Server.MaxBodyBytes = 10
	handler := NewHandler(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), controller.NewWithHTTPClient(baseURL, controllerServer.Client()))
	request := httptest.NewRequest(http.MethodPost, "/auth", strings.NewReader(`{"username":"far-too-large"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestUpdateConfigPreservesScalaHeaderException(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "controller-value")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer controllerServer.Close()
	baseURL, _ := url.Parse(controllerServer.URL + "/v1")
	handler := NewHandler(
		testConfig(baseURL), slog.New(slog.NewTextHandler(io.Discard, nil)),
		controller.NewWithHTTPClient(baseURL, controllerServer.Client()),
	)
	request := httptest.NewRequest(http.MethodPatch, "/config", strings.NewReader(`{"cluster_name":"fixture"}`))
	request.Header.Set("Token", "test-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "controller-value" {
		t.Fatalf("response = %d %v", response.Code, response.Header())
	}
	for _, name := range []string{
		"Content-Security-Policy", "Strict-Transport-Security", "X-Content-Type-Options",
		"X-Frame-Options", "X-Xss-Protection",
	} {
		if value := response.Header().Get(name); value != "" {
			t.Errorf("%s = %q, want absent", name, value)
		}
	}
}

func testConfig(baseURL *url.URL) config.Config {
	return config.Config{
		Server:     config.ServerConfig{MaxBodyBytes: 50 << 20},
		Controller: config.ControllerConfig{BaseURL: baseURL, Timeout: time.Second},
		Session:    config.SessionConfig{MaxEntries: 100},
		Cache:      config.CacheConfig{MaxEntries: 10, MaxBytes: 1 << 20, TTL: time.Minute},
	}
}
