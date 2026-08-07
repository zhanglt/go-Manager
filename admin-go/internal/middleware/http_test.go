package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/observability"
)

func TestAccessLogDoesNotRecordQueryOrHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	engine := gin.New()
	engine.Use(RequestID(), AccessLog(logger))
	engine.GET("/resource", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/resource?password=do-not-log", nil)
	request.Header.Set("Token", "secret-token")
	engine.ServeHTTP(httptest.NewRecorder(), request)
	for _, secret := range []string{"do-not-log", "secret-token"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("access log contains sensitive value %q: %s", secret, logs.String())
		}
	}
	for _, field := range []string{"request_id", "method", "route", "status", "latency_ms", "response_bytes", "controller_status", "cluster_present"} {
		if !strings.Contains(logs.String(), `"`+field+`"`) {
			t.Errorf("access log is missing %q: %s", field, logs.String())
		}
	}
}

func TestRecoveryDoesNotExposePanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logs bytes.Buffer
	engine := gin.New()
	engine.Use(RequestID(), Recovery(slog.New(slog.NewJSONHandler(&logs, nil))))
	engine.GET("/panic", func(*gin.Context) { panic("secret panic value") })
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if strings.Contains(logs.String(), "secret panic value") {
		t.Fatalf("panic log contains recovered value: %s", logs.String())
	}
}

func TestMetricsUseNormalizedRouteAndMethod(t *testing.T) {
	registry := observability.NewRegistry()
	engine := gin.New()
	engine.Use(Metrics(registry))
	engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("ATTACKER-METHOD", "/secret-object-id", nil))

	response := httptest.NewRecorder()
	registry.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	if strings.Contains(body, "secret-object-id") || !strings.Contains(body, `method="OTHER",route="unmatched"`) {
		t.Fatalf("metrics contain an unbounded label: %s", body)
	}
}

func TestRemoveSecurityHeadersSupportsCompatibilityException(t *testing.T) {
	engine := gin.New()
	engine.Use(SecurityHeaders(true))
	engine.GET("/legacy", func(c *gin.Context) {
		RemoveSecurityHeaders(c.Writer.Header())
		c.Status(http.StatusOK)
	})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/legacy", nil))
	for _, name := range []string{"Cache-Control", "Content-Security-Policy", "Strict-Transport-Security", "X-Frame-Options"} {
		if value := response.Header().Get(name); value != "" {
			t.Errorf("%s=%q", name, value)
		}
	}
}
