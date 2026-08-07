package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/observability"
)

const RequestIDKey = "request_id"

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" || len(requestID) > 128 {
			var value [16]byte
			if _, err := io.ReadFull(rand.Reader, value[:]); err == nil {
				requestID = hex.EncodeToString(value[:])
			} else {
				requestID = "unavailable"
			}
		}
		c.Set(RequestIDKey, requestID)
		c.Next()
	}
}

func Recovery(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("request panic",
					"request_id", requestID(c),
					"method", c.Request.Method,
					"path", c.Request.URL.Path,
					"panic_type", fmt.Sprintf("%T", recovered),
					"stack", string(debug.Stack()),
				)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Server internal error"})
			}
		}()
		c.Next()
	}
}

func AccessLog(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		logger.Info("request completed",
			"request_id", requestID(c),
			"method", c.Request.Method,
			"route", normalizedRoute(c),
			"status", c.Writer.Status(),
			"latency_ms", time.Since(started).Milliseconds(),
			"response_bytes", c.Writer.Size(),
			"controller_status", 0,
			"cluster_present", c.GetHeader("X-Cluster-ID") != "",
		)
	}
}

func Metrics(registry *observability.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		registry.ObserveRequest(c.Request.Method, normalizedRoute(c), c.Writer.Status(), time.Since(started))
	}
}

func LimitBody(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

func SecurityHeaders(tlsEnabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.Writer.Header()
		header.Set("X-Frame-Options", "SAMEORIGIN")
		header.Set("Cache-Control", "private, no-cache, no-store, must-revalidate")
		header.Set("X-XSS-Protection", "1; mode=block")
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Content-Security-Policy", "default-src 'self'; font-src 'self' data: */fonts; img-src 'self' data:; script-src 'self' 'unsafe-eval' 'unsafe-inline'; style-src 'self' 'unsafe-inline';")
		if tlsEnabled {
			header.Set("Strict-Transport-Security", "max-age=15724800; includeSubDomains; preload")
		}
		c.Next()
	}
}

func RemoveSecurityHeaders(header http.Header) {
	for _, name := range []string{
		"X-Frame-Options", "Cache-Control", "X-XSS-Protection", "X-Content-Type-Options",
		"Content-Security-Policy", "Strict-Transport-Security",
	} {
		header.Del(name)
	}
}

func requestID(c *gin.Context) string {
	value, _ := c.Get(RequestIDKey)
	requestID, _ := value.(string)
	return requestID
}

func normalizedRoute(c *gin.Context) string {
	if route := c.FullPath(); route != "" {
		return route
	}
	return "unmatched"
}
