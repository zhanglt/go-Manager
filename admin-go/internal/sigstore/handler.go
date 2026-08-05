package sigstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/session"
)

type Handler struct {
	controller *controller.Client
	resolver   *controller.TargetResolver
	sessions   *session.Store
}

type verifier struct {
	Name            *string `json:"name,omitempty"`
	RootOfTrustName *string `json:"root_of_trust_name,omitempty"`
	Comment         *string `json:"comment,omitempty"`
	IsPrivate       *bool   `json:"is_private,omitempty"`
	VerifierType    *string `json:"verifier_type,omitempty"`
	IgnoreTlog      *bool   `json:"ignore_tlog,omitempty"`
	IgnoreSCT       *bool   `json:"ignore_sct,omitempty"`
	PublicKey       *string `json:"public_key,omitempty"`
	CertIssuer      *string `json:"cert_issuer,omitempty"`
	CertSubject     *string `json:"cert_subject,omitempty"`
}

type rootOfTrust struct {
	Name                 *string     `json:"name,omitempty"`
	Comment              *string     `json:"comment,omitempty"`
	IsPrivate            *bool       `json:"is_private,omitempty"`
	RootlessKeypairsOnly *bool       `json:"rootless_keypairs_only,omitempty"`
	RekorPublicKey       *string     `json:"rekor_public_key,omitempty"`
	RootCert             *string     `json:"root_cert,omitempty"`
	SCTPublicKey         *string     `json:"sct_public_key,omitempty"`
	Verifiers            *[]verifier `json:"verifiers,omitempty"`
	CfgType              *string     `json:"cfg_type,omitempty"`
}

func NewHandler(client *controller.Client, resolver *controller.TargetResolver, sessions *session.Store) *Handler {
	return &Handler{controller: client, resolver: resolver, sessions: sessions}
}

func (h *Handler) GetRoots(c *gin.Context) {
	h.proxy(c, http.MethodGet, nil, "scan", "sigstore", "root_of_trust")
}

func (h *Handler) CreateRoot(c *gin.Context) {
	var input rootOfTrust
	if h.decode(c, &input) {
		h.proxyJSON(c, http.MethodPost, input, "scan", "sigstore", "root_of_trust")
	}
}

func (h *Handler) UpdateRoot(c *gin.Context) {
	var input rootOfTrust
	if !h.decode(c, &input) {
		return
	}
	name, ok := requiredPointer(c, input.Name, "root of trust name")
	if ok {
		h.proxyJSON(c, http.MethodPatch, input, "scan", "sigstore", "root_of_trust", name)
	}
}

func (h *Handler) DeleteRoot(c *gin.Context) {
	name, ok := requiredQuery(c, "rootOfTrustName")
	if ok {
		h.proxy(c, http.MethodDelete, nil, "scan", "sigstore", "root_of_trust", name)
	}
}

func (h *Handler) GetVerifiers(c *gin.Context) {
	rootName, ok := requiredQuery(c, "rootOfTrustName")
	if ok {
		h.proxy(c, http.MethodGet, nil, "scan", "sigstore", "root_of_trust", rootName, "verifier")
	}
}

func (h *Handler) CreateVerifier(c *gin.Context) {
	var input verifier
	if !h.decode(c, &input) {
		return
	}
	rootName, ok := requiredPointer(c, input.RootOfTrustName, "verifier root_of_trust_name")
	if ok {
		h.proxyJSON(c, http.MethodPost, input, "scan", "sigstore", "root_of_trust", rootName, "verifier")
	}
}

func (h *Handler) UpdateVerifier(c *gin.Context) {
	var input verifier
	if !h.decode(c, &input) {
		return
	}
	rootName, ok := requiredPointer(c, input.RootOfTrustName, "verifier root_of_trust_name")
	if !ok {
		return
	}
	name, ok := requiredPointer(c, input.Name, "verifier name")
	if ok {
		h.proxyJSON(c, http.MethodPatch, input, "scan", "sigstore", "root_of_trust", rootName, "verifier", name)
	}
}

func (h *Handler) DeleteVerifier(c *gin.Context) {
	rootName, ok := requiredQuery(c, "rootOfTrustName")
	if !ok {
		return
	}
	name, ok := requiredQuery(c, "verifierName")
	if ok {
		h.proxy(c, http.MethodDelete, nil, "scan", "sigstore", "root_of_trust", rootName, "verifier", name)
	}
}

func (h *Handler) proxyJSON(c *gin.Context, method string, value any, segments ...string) {
	body, err := json.Marshal(value)
	if err != nil {
		writeInternalError(c)
		return
	}
	h.proxy(c, method, body, segments...)
}

func (h *Handler) proxy(c *gin.Context, method string, body []byte, segments ...string) {
	target, err := h.resolver.Resolve(c.GetHeader("Token"), controller.V1, segments...)
	if err != nil {
		writeInternalError(c)
		return
	}
	token := c.GetHeader("Token")
	headers := make(http.Header)
	headers.Set("X-Auth-Token", token)
	headers.Set("Accept-Encoding", "gzip")
	headers.Set("Cache-Control", "no-cache")
	if entry, ok := h.sessions.Get(token); ok {
		headers.Set("X-R-Sess", entry.SUSEToken)
	} else {
		headers.Set("X-R-Sess", "")
	}
	if body != nil {
		headers.Set("Content-Type", "text/plain; charset=UTF-8")
	}
	response, err := h.controller.DoTarget(c.Request.Context(), method, target, bytes.NewReader(body), headers)
	if err != nil {
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	copyResponse(c, response)
}

func (h *Handler) decode(c *gin.Context, target any) bool {
	if err := json.NewDecoder(c.Request.Body).Decode(target); err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			c.Data(http.StatusRequestEntityTooLarge, "text/plain; charset=UTF-8", []byte("Request entity too large"))
		} else {
			writeBadRequest(c, "invalid JSON request body")
		}
		return false
	}
	return true
}

func requiredPointer(c *gin.Context, value *string, label string) (string, bool) {
	if value == nil || *value == "" {
		writeBadRequest(c, label+" is required")
		return "", false
	}
	return *value, true
}

func requiredQuery(c *gin.Context, name string) (string, bool) {
	value, present := c.GetQuery(name)
	if !present {
		writeBadRequest(c, "query parameter '"+name+"' is required")
	}
	return value, present
}

func copyResponse(c *gin.Context, response *http.Response) {
	for name, values := range response.Header {
		for _, value := range values {
			c.Writer.Header().Add(name, value)
		}
	}
	c.Status(response.StatusCode)
	_, _ = io.Copy(c.Writer, response.Body)
}

func writeBadRequest(c *gin.Context, message string) {
	c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte(message))
}

func writeInternalError(c *gin.Context) {
	c.Data(http.StatusInternalServerError, "application/json", []byte("Internal server error"))
}
