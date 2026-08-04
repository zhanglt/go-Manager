package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/hashutil"
	managerMiddleware "github.com/neuvector/manager/admin-go/internal/middleware"
)

var testVersionHash = hashutil.CompatibilityHash("test-version")[:10]

var testWebFiles = fstest.MapFS{
	"index.html":     &fstest.MapFile{Data: []byte("<html>fixture</html>")},
	"app.js":         &fstest.MapFile{Data: []byte("plain-js")},
	"app.js.gz":      &fstest.MapFile{Data: []byte("gzip-js")},
	"styles.css":     &fstest.MapFile{Data: []byte("body{}")},
	"assets/en.json": &fstest.MapFile{Data: []byte(`{"name":"fixture"}`)},
	"image.svg":      &fstest.MapFile{Data: []byte("<svg></svg>")},
	"font.woff2":     &fstest.MapFile{Data: []byte("font-data")},
}

func TestStaticIndexRedirects(t *testing.T) {
	for _, test := range []struct {
		name   string
		prefix string
		path   string
		status int
		body   string
	}{
		{"root", "", "/", http.StatusMovedPermanently, ""},
		{"index without version", "", "/index.html", http.StatusMovedPermanently, ""},
		{"index old version", "", "/index.html?v=old", http.StatusMovedPermanently, ""},
		{"index current version", "", "/index.html?v=" + testVersionHash, http.StatusOK, "<html>fixture</html>"},
		{"prefix root", "/manager", "/manager", http.StatusMovedPermanently, ""},
		{"prefix trailing slash", "/manager", "/manager/", http.StatusMovedPermanently, ""},
		{"prefix index", "/manager", "/manager/index.html?v=" + testVersionHash, http.StatusOK, "<html>fixture</html>"},
		{"outside prefix", "/manager", "/index.html?v=" + testVersionHash, http.StatusNotFound, "404 page not found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := serveStaticRequest(test.prefix, false, false, http.MethodGet, test.path, nil)
			if response.Code != test.status || (test.status != http.StatusMovedPermanently && response.Body.String() != test.body) {
				t.Fatalf("response = %d %q, want %d %q", response.Code, response.Body.String(), test.status, test.body)
			}
			if test.status == http.StatusMovedPermanently {
				want := test.prefix + "/index.html?v=" + testVersionHash
				if got := response.Header().Get("Location"); got != want {
					t.Errorf("Location = %q, want %q", got, want)
				}
			}
			if got := response.Header().Get("Cache-Control"); test.status == http.StatusOK && got != "private, no-cache, no-store, must-revalidate" {
				t.Errorf("index Cache-Control = %q", got)
			}
		})
	}
}

func TestStaticProductionAndDevelopmentJavaScript(t *testing.T) {
	for _, test := range []struct {
		name        string
		development bool
		accept      string
		body        string
		encoding    string
	}{
		{"gzip without negotiation", false, "", "gzip-js", "gzip"},
		{"gzip despite identity", false, "identity", "gzip-js", "gzip"},
		{"development source", true, "gzip", "plain-js", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			header := http.Header{"Accept-Encoding": []string{test.accept}}
			response := serveStaticRequest("", test.development, false, http.MethodGet, "/app.js", header)
			if response.Code != http.StatusOK || response.Body.String() != test.body {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
			if got := response.Header().Get("Content-Encoding"); got != test.encoding {
				t.Errorf("Content-Encoding = %q, want %q", got, test.encoding)
			}
			if got := response.Header().Get("Content-Type"); got != "application/javascript" {
				t.Errorf("Content-Type = %q", got)
			}
			if got := response.Header().Get("Cache-Control"); got != "" {
				t.Errorf("Cache-Control = %q, want absent", got)
			}
			if got := response.Header().Get("Content-Length"); got != "7" && !test.development {
				t.Errorf("Content-Length = %q, want 7", got)
			}
		})
	}
}

func TestStaticContentTypesHeadAndRange(t *testing.T) {
	for path, wantType := range map[string]string{
		"/styles.css": "text/css; charset=utf-8", "/assets/en.json": "application/json",
		"/image.svg": "image/svg+xml", "/font.woff2": "font/woff2",
	} {
		response := serveStaticRequest("", false, false, http.MethodGet, path, nil)
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != wantType {
			t.Errorf("GET %s = %d Content-Type %q, want %q", path, response.Code, response.Header().Get("Content-Type"), wantType)
		}
	}

	head := serveStaticRequest("", true, false, http.MethodHead, "/app.js", nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "8" {
		t.Errorf("HEAD response = %d body=%q headers=%v", head.Code, head.Body.String(), head.Header())
	}
	rangeHeader := http.Header{"Range": []string{"bytes=1-3"}}
	ranged := serveStaticRequest("", true, false, http.MethodGet, "/app.js", rangeHeader)
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != "lai" || ranged.Header().Get("Content-Range") != "bytes 1-3/8" {
		t.Errorf("Range response = %d %q headers=%v", ranged.Code, ranged.Body.String(), ranged.Header())
	}
}

func TestStaticSPAFallbackAndNotFound(t *testing.T) {
	htmlHeader := http.Header{"Accept": []string{"text/html,application/xhtml+xml"}}
	for _, test := range []struct {
		name   string
		method string
		path   string
		header http.Header
		status int
		body   string
	}{
		{"deep link", http.MethodGet, "/security/risks", htmlHeader, http.StatusOK, "<html>fixture</html>"},
		{"deep link trailing slash", http.MethodGet, "/security/risks/", htmlHeader, http.StatusOK, "<html>fixture</html>"},
		{"non html", http.MethodGet, "/security/risks", nil, http.StatusNotFound, "404 page not found"},
		{"missing asset", http.MethodGet, "/missing.js", htmlHeader, http.StatusNotFound, "404 page not found"},
		{"favicon", http.MethodGet, "/favicon.ico", htmlHeader, http.StatusNotFound, "404 page not found"},
		{"directory", http.MethodGet, "/assets/", nil, http.StatusNotFound, "404 page not found"},
		{"parent traversal", http.MethodGet, "/../index.html", htmlHeader, http.StatusNotFound, "404 page not found"},
		{"encoded traversal", http.MethodGet, "/%2e%2e/index.html", htmlHeader, http.StatusNotFound, "404 page not found"},
		{"backslash traversal", http.MethodGet, "/..%5cindex.html", htmlHeader, http.StatusNotFound, "404 page not found"},
		{"post", http.MethodPost, "/styles.css", nil, http.StatusNotFound, "404 page not found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := serveStaticRequest("", false, false, test.method, test.path, test.header)
			if response.Code != test.status || response.Body.String() != test.body {
				t.Fatalf("response = %d %q, want %d %q", response.Code, response.Body.String(), test.status, test.body)
			}
		})
	}
}

func TestStaticSecurityHeaders(t *testing.T) {
	response := serveStaticRequest("", false, true, http.MethodGet, "/styles.css", nil)
	for name, want := range map[string]string{
		"X-Frame-Options": "SAMEORIGIN", "X-Content-Type-Options": "nosniff",
		"X-XSS-Protection":          "1; mode=block",
		"Strict-Transport-Security": "max-age=15724800; includeSubDomains; preload",
	} {
		if got := response.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func serveStaticRequest(prefix string, development, tls bool, method, target string, header http.Header) *httptest.ResponseRecorder {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(managerMiddleware.SecurityHeaders(tls))
	engine.NoRoute(newStaticHandler(fs.FS(testWebFiles), prefix, development, "test-version").handle)
	request := httptest.NewRequest(method, target, nil)
	request.Header = header.Clone()
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}
