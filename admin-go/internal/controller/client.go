package controller

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	observer   Observer
}

type Observer interface {
	ObserveController(method string, status int, elapsed time.Duration, err error)
}

func New(baseURL *url.URL, verifyTLS bool, timeout time.Duration) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		// The Scala service accepts the Controller's deployment certificate by default.
		InsecureSkipVerify: !verifyTLS, //nolint:gosec
	}
	return &Client{
		baseURL: cloneURL(baseURL),
		httpClient: &http.Client{
			Transport: transport, Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func NewWithHTTPClient(baseURL *url.URL, client *http.Client) *Client {
	return &Client{baseURL: cloneURL(baseURL), httpClient: client}
}

func (c *Client) SetObserver(observer Observer) { c.observer = observer }

func (c *Client) Do(ctx context.Context, method, path string, body io.Reader, headers http.Header) (*http.Response, error) {
	target, err := c.resolve(path)
	if err != nil {
		return nil, err
	}
	return c.DoTarget(ctx, method, target, body, headers)
}

func (c *Client) DoTarget(ctx context.Context, method string, target *url.URL, body io.Reader, headers http.Header) (*http.Response, error) {
	return c.DoTargetSized(ctx, method, target, body, headers, -1)
}

func (c *Client) DoTargetSized(ctx context.Context, method string, target *url.URL, body io.Reader, headers http.Header, contentLength int64) (*http.Response, error) {
	if target == nil || target.Scheme != c.baseURL.Scheme || target.Host != c.baseURL.Host {
		return nil, errors.New("controller target must use the configured origin")
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, fmt.Errorf("create controller request: %w", err)
	}
	if contentLength >= 0 {
		request.ContentLength = contentLength
	}
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	started := time.Now()
	response, err := c.httpClient.Do(request)
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	if c.observer != nil {
		c.observer.ObserveController(method, status, time.Since(started), err)
	}
	if err != nil {
		return nil, fmt.Errorf("controller request: %w", err)
	}
	return response, nil
}

func (c *Client) resolve(path string) (*url.URL, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, fmt.Errorf("controller path must start with one slash: %q", path)
	}
	target := cloneURL(c.baseURL)
	target.Path = strings.TrimRight(target.Path, "/") + path
	target.RawPath = ""
	target.RawQuery = ""
	target.Fragment = ""
	return target, nil
}

func cloneURL(source *url.URL) *url.URL {
	result := *source
	return &result
}
