package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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
