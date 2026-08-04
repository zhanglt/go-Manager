package transfer

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/session"
)

type Proxy struct {
	controller *controller.Client
	resolver   *controller.TargetResolver
	sessions   *session.Store
}

func New(client *controller.Client, resolver *controller.TargetResolver, sessions *session.Store) *Proxy {
	return &Proxy{controller: client, resolver: resolver, sessions: sessions}
}

func (p *Proxy) Import(scope string, segments ...string) gin.HandlerFunc {
	return p.ImportQuery(url.Values{"scope": {scope}}, segments...)
}

func (p *Proxy) ImportQuery(query url.Values, segments ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body []byte
		headers := make(http.Header)
		if transactionID, present := RequestHeader(c.Request.Header, "X-Transaction-Id"); present {
			headers.Set("X-Transaction-Id", transactionID)
			body = []byte{}
			p.RequestTransaction(c, http.MethodPost, controller.V1, false, query, headers, body, segments...)
			return
		} else {
			value, err := io.ReadAll(c.Request.Body)
			if err != nil {
				WriteBodyError(c, err)
				return
			}
			body = []byte(ExtractLegacyForm(string(value)))
		}
		p.PostQuery(c, query, headers, body, segments...)
	}
}

func (p *Proxy) Post(c *gin.Context, scope string, extraHeaders http.Header, body []byte, segments ...string) {
	p.PostQuery(c, url.Values{"scope": {scope}}, extraHeaders, body, segments...)
}

func (p *Proxy) PostQuery(c *gin.Context, query url.Values, extraHeaders http.Header, body []byte, segments ...string) {
	p.Request(c, http.MethodPost, query, extraHeaders, body, segments...)
}

func (p *Proxy) Request(c *gin.Context, method string, query url.Values, extraHeaders http.Header, body []byte, segments ...string) {
	p.request(c, method, true, query, extraHeaders, body, segments...)
}

func (p *Proxy) RequestLocal(c *gin.Context, method string, query url.Values, extraHeaders http.Header, body []byte, segments ...string) {
	p.request(c, method, false, query, extraHeaders, body, segments...)
}

func (p *Proxy) RequestV2(c *gin.Context, method string, query url.Values, extraHeaders http.Header, body []byte, segments ...string) {
	p.requestVersion(c, method, true, controller.V2, query, extraHeaders, body, segments...)
}

// Fetch sends a cluster-aware request for handlers that need to transform the Controller response.
// The caller owns the returned response body.
func (p *Proxy) Fetch(c *gin.Context, method string, query url.Values, extraHeaders http.Header, body []byte, segments ...string) (*http.Response, error) {
	target, err := p.resolver.Resolve(c.GetHeader("Token"), controller.V1, segments...)
	if err != nil {
		return nil, err
	}
	return p.fetchTarget(c, method, target, query, extraHeaders, body)
}

// FetchLocal is the local-controller counterpart to Fetch.
func (p *Proxy) FetchLocal(c *gin.Context, method string, query url.Values, extraHeaders http.Header, body []byte, segments ...string) (*http.Response, error) {
	target, err := p.resolver.ResolveLocal(controller.V1, segments...)
	if err != nil {
		return nil, err
	}
	return p.fetchTarget(c, method, target, query, extraHeaders, body)
}

func (p *Proxy) request(c *gin.Context, method string, clusterAware bool, query url.Values, extraHeaders http.Header, body []byte, segments ...string) {
	p.requestVersion(c, method, clusterAware, controller.V1, query, extraHeaders, body, segments...)
}

func (p *Proxy) requestVersion(c *gin.Context, method string, clusterAware bool, version controller.APIVersion, query url.Values, extraHeaders http.Header, body []byte, segments ...string) {
	var target *url.URL
	var err error
	if clusterAware {
		target, err = p.resolver.Resolve(c.GetHeader("Token"), version, segments...)
	} else {
		target, err = p.resolver.ResolveLocal(version, segments...)
	}
	if err != nil {
		writeInternalError(c)
		return
	}
	p.requestTarget(c, method, target, query, extraHeaders, body)
}

func (p *Proxy) RequestTransaction(c *gin.Context, method string, fallback controller.APIVersion, closeTransaction bool, query url.Values, extraHeaders http.Header, body []byte, segments ...string) {
	token := c.GetHeader("Token")
	api, clusterID := p.sessions.EnsureTransactionTarget(token, string(fallback))
	if closeTransaction {
		p.sessions.ClearTransactionTarget(token)
	}
	target, err := p.resolver.ResolveCluster(clusterID, controller.APIVersion(api), segments...)
	if err != nil {
		writeInternalError(c)
		return
	}
	p.requestTarget(c, method, target, query, extraHeaders, body)
}

func (p *Proxy) requestTarget(c *gin.Context, method string, target *url.URL, query url.Values, extraHeaders http.Header, body []byte) {
	response, err := p.fetchTarget(c, method, target, query, extraHeaders, body)
	if err != nil {
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	for name, values := range response.Header {
		if strings.EqualFold(name, "Content-Length") {
			continue
		}
		for _, value := range values {
			c.Writer.Header().Add(name, value)
		}
	}
	c.Status(response.StatusCode)
	_, _ = io.Copy(c.Writer, response.Body)
}

func (p *Proxy) fetchTarget(c *gin.Context, method string, target *url.URL, query url.Values, extraHeaders http.Header, body []byte) (*http.Response, error) {
	if query != nil {
		target.RawQuery = query.Encode()
	}
	headers := make(http.Header)
	token := c.GetHeader("Token")
	headers.Set("X-Auth-Token", token)
	headers.Set("Accept-Encoding", "gzip")
	headers.Set("Cache-Control", "no-cache")
	if body != nil {
		headers.Set("Content-Type", "text/plain; charset=UTF-8")
	}
	if entry, ok := p.sessions.Get(token); ok {
		headers.Set("X-R-Sess", entry.SUSEToken)
	} else {
		headers.Set("X-R-Sess", "")
	}
	for name, values := range extraHeaders {
		for _, value := range values {
			headers.Add(name, value)
		}
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	response, err := p.controller.DoTarget(c.Request.Context(), method, target, reader, headers)
	if err != nil {
		return nil, err
	}
	return response, nil
}

func ExtractLegacyForm(value string) string {
	lines := strings.Split(value, "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= 5 {
		return ""
	}
	return strings.Join(lines[4:len(lines)-1], "\n")
}

func RequestHeader(header http.Header, name string) (string, bool) {
	values, present := header[http.CanonicalHeaderKey(name)]
	if !present || len(values) == 0 {
		return "", false
	}
	return values[0], true
}

func WriteBodyError(c *gin.Context, err error) {
	var sizeError *http.MaxBytesError
	if errors.As(err, &sizeError) {
		c.Data(http.StatusRequestEntityTooLarge, "text/plain; charset=UTF-8", []byte("Request entity too large"))
		return
	}
	c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("invalid request body"))
}

func writeInternalError(c *gin.Context) {
	c.Data(http.StatusInternalServerError, "application/json", []byte("Internal server error"))
}
