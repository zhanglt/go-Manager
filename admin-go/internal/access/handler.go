package access

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
	controller  *controller.Client
	resolver    *controller.TargetResolver
	sessions    *session.Store
	invalidator tokenInvalidator
}

type tokenInvalidator interface {
	DeleteToken(string)
}

func NewHandler(client *controller.Client, resolver *controller.TargetResolver, sessions *session.Store, invalidators ...tokenInvalidator) *Handler {
	var invalidator tokenInvalidator
	if len(invalidators) > 0 {
		invalidator = invalidators[0]
	}
	return &Handler{controller: client, resolver: resolver, sessions: sessions, invalidator: invalidator}
}

func (h *Handler) SwitchCluster(c *gin.Context) {
	tokenID := c.GetHeader("Token")
	if h.invalidator != nil {
		h.invalidator.DeleteToken(tokenID)
	}
	clusterID, present := c.GetQuery("id")
	if present {
		h.sessions.SetCluster(tokenID, clusterID)
		writeJSON(c, http.StatusOK, gin.H{"id": clusterID})
		return
	}
	h.sessions.SetCluster(tokenID, "")
	writeJSON(c, http.StatusOK, gin.H{"id": nil})
}

func (h *Handler) PermissionOptions(c *gin.Context) {
	h.proxy(c, http.MethodGet, nil, "user_role_permission", "options")
}

func (h *Handler) GetRoles(c *gin.Context) {
	segments := []string{"user_role"}
	if name, present := c.GetQuery("name"); present {
		segments = append(segments, name)
	}
	h.proxy(c, http.MethodGet, nil, segments...)
}

func (h *Handler) AddRole(c *gin.Context) {
	body, ok := compactJSONBody(c)
	if ok {
		h.proxy(c, http.MethodPost, body, "user_role")
	}
}

func (h *Handler) UpdateRole(c *gin.Context) {
	body, ok := compactJSONBody(c)
	if !ok {
		return
	}
	var request struct {
		Config struct {
			Name string `json:"name"`
		} `json:"config"`
	}
	if err := json.Unmarshal(body, &request); err != nil || request.Config.Name == "" {
		writeBadRequest(c, "role config.name is required")
		return
	}
	h.proxy(c, http.MethodPatch, body, "user_role", request.Config.Name)
}

func (h *Handler) DeleteRole(c *gin.Context) {
	name, present := c.GetQuery("name")
	if !present {
		writeBadRequest(c, "query parameter 'name' is required")
		return
	}
	h.proxy(c, http.MethodDelete, nil, "user_role", name)
}

func (h *Handler) GetAPIKeys(c *gin.Context) {
	segments := []string{"api_key"}
	if name, present := c.GetQuery("name"); present {
		segments = append(segments, name)
	}
	h.proxy(c, http.MethodGet, nil, segments...)
}

func (h *Handler) AddOrCreateAPIKey(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeReadError(c, err)
		return
	}
	if len(bytes.TrimSpace(body)) == 0 {
		h.proxy(c, http.MethodPost, nil, "api_key")
		return
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, body); err != nil {
		writeBadRequest(c, "invalid JSON request body")
		return
	}
	h.proxy(c, http.MethodPost, compact.Bytes(), "api_key")
}

func (h *Handler) DeleteAPIKey(c *gin.Context) {
	name, present := c.GetQuery("name")
	if !present {
		writeBadRequest(c, "query parameter 'name' is required")
		return
	}
	h.proxy(c, http.MethodDelete, nil, "api_key", name)
}

func (h *Handler) proxy(c *gin.Context, method string, body []byte, resourceSegments ...string) {
	tokenID := c.GetHeader("Token")
	target, err := h.resolver.Resolve(tokenID, controller.V1, resourceSegments...)
	if err != nil {
		writeInternalError(c)
		return
	}
	headers := make(http.Header)
	headers.Set("X-Auth-Token", tokenID)
	headers.Set("Accept-Encoding", "gzip")
	headers.Set("Cache-Control", "no-cache")
	if entry, ok := h.sessions.Get(tokenID); ok {
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

func compactJSONBody(c *gin.Context) ([]byte, bool) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeReadError(c, err)
		return nil, false
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, body); err != nil {
		writeBadRequest(c, "invalid JSON request body")
		return nil, false
	}
	return compact.Bytes(), true
}

func copyResponse(c *gin.Context, response *http.Response) {
	for name, values := range response.Header {
		if isHopByHop(name) {
			continue
		}
		for _, value := range values {
			c.Writer.Header().Add(name, value)
		}
	}
	c.Status(response.StatusCode)
	_, _ = io.Copy(c.Writer, response.Body)
}

func isHopByHop(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}

func writeReadError(c *gin.Context, err error) {
	var sizeError *http.MaxBytesError
	if errors.As(err, &sizeError) {
		c.Header("Content-Type", "text/plain; charset=UTF-8")
		c.String(http.StatusRequestEntityTooLarge, "Request entity too large")
		return
	}
	writeInternalError(c)
}

func writeBadRequest(c *gin.Context, message string) {
	c.Header("Content-Type", "text/plain; charset=UTF-8")
	c.String(http.StatusBadRequest, "%s", message)
}

func writeInternalError(c *gin.Context) {
	c.Header("Content-Type", "application/json")
	c.String(http.StatusInternalServerError, "Internal server error")
}

func writeJSON(c *gin.Context, status int, value any) {
	c.Header("Content-Type", "application/json")
	c.JSON(status, value)
}
