package controller

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDoBuildsControllerRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth" || r.Header.Get("X-R-Sess") != "cookie" {
			t.Errorf("unexpected request path=%q headers=%v", r.URL.Path, r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "payload" {
			t.Errorf("body = %q", body)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	base, _ := url.Parse(server.URL + "/v1")
	client := NewWithHTTPClient(base, server.Client())
	response, err := client.Do(context.Background(), http.MethodPost, "/auth", strings.NewReader("payload"), http.Header{"X-R-Sess": {"cookie"}})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestDoRejectsNonRelativePath(t *testing.T) {
	base, _ := url.Parse("https://controller:10443/v1")
	client := NewWithHTTPClient(base, http.DefaultClient)
	if _, err := client.Do(context.Background(), http.MethodGet, "https://attacker.invalid", nil, nil); err == nil {
		t.Fatal("Do() error = nil, want unsafe path rejection")
	}
}

func TestDoTargetRejectsDifferentOrigin(t *testing.T) {
	base, _ := url.Parse("https://controller:10443/v1")
	target, _ := url.Parse("https://attacker.invalid/v1/auth")
	client := NewWithHTTPClient(base, http.DefaultClient)
	if _, err := client.DoTarget(context.Background(), http.MethodGet, target, nil, nil); err == nil {
		t.Fatal("DoTarget() error = nil, want origin rejection")
	}
}
