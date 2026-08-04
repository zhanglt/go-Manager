package auth

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type selfResponse struct {
	GlobalPermissions       []permission            `json:"global_permissions"`
	RemoteGlobalPermissions []permission            `json:"remote_global_permissions"`
	DomainPermissions       map[string][]permission `json:"domain_permissions"`
	PasswordDaysUntilExpire *int                    `json:"password_days_until_expire"`
	User                    user                    `json:"user"`
}

type eulaResponse struct {
	EULA struct {
		Accepted bool `json:"accepted"`
	} `json:"eula"`
}

type user struct {
	Fullname         string              `json:"fullname"`
	Server           string              `json:"server"`
	Username         string              `json:"username"`
	Email            *string             `json:"email"`
	Role             string              `json:"role"`
	Locale           string              `json:"locale"`
	Timeout          *int                `json:"timeout"`
	DefaultPassword  bool                `json:"default_password"`
	ModifyPassword   bool                `json:"modify_password"`
	RoleDomains      map[string][]string `json:"role_domains"`
	ExtraPermissions []permission        `json:"extra_permissions"`
}

func (h *Handler) Self(c *gin.Context) {
	tokenID := c.GetHeader("Token")
	rancherSSO := strings.EqualFold(c.Query("isRancherSSOUrl"), "true")
	requestSUSEToken := ""
	if cookie, err := c.Request.Cookie("R_SESS"); err == nil && rancherSSO {
		requestSUSEToken = cookie.Value
		entry, _ := h.sessions.Get(tokenID)
		if cookie.Value != entry.SUSEToken {
			h.login(c, password{IsRancherSSOURL: true})
			return
		}
	}

	response, err := h.controller.Do(c.Request.Context(), http.MethodGet, "/selfuser", nil, h.authenticatedHeaders(tokenID))
	if err != nil {
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeControllerAuthError(c, response.StatusCode)
		return
	}
	var source selfResponse
	if err := decodeControllerJSON(response, &source); err != nil {
		writeInternalError(c)
		return
	}
	timeout := source.User.Timeout
	if c.Query("isOnNV") != "true" {
		compatibilityTimeout := 300
		timeout = &compatibilityTimeout
	}
	entry, _ := h.sessions.Get(tokenID)
	suseAuthenticated := requestSUSEToken != ""
	output := convertResponse(controllerResponse{
		PasswordDaysUntilExpire: source.PasswordDaysUntilExpire,
		Token: &token{
			Token: tokenID, Fullname: source.User.Fullname, Server: source.User.Server,
			Username: source.User.Username, Email: source.User.Email, Role: source.User.Role,
			Locale: source.User.Locale, Timeout: timeout,
			DefaultPassword: source.User.DefaultPassword, ModifyPassword: source.User.ModifyPassword,
			RoleDomains: source.User.RoleDomains, ExtraPermissions: source.User.ExtraPermissions,
			GlobalPermissions:       source.GlobalPermissions,
			RemoteGlobalPermissions: source.RemoteGlobalPermissions,
			DomainPermissions:       source.DomainPermissions,
		},
	}, h.now(), suseAuthenticated)
	if h.invalidator != nil {
		h.invalidator.DeleteToken(tokenID)
	}
	h.sessions.Put(tokenID, entry.SUSEToken)
	if encoded, err := json.Marshal(output); err == nil {
		h.sessions.SetTokenJSON(tokenID, encoded)
	}
	c.Header("Content-Type", "application/json")
	c.JSON(http.StatusOK, output)
}

func (h *Handler) Heartbeat(c *gin.Context) {
	h.proxyAuthenticated(c, http.MethodPatch, "/auth", false)
}

func (h *Handler) Logout(c *gin.Context) {
	tokenID := c.GetHeader("Token")
	if h.invalidator != nil {
		h.invalidator.DeleteToken(tokenID)
	}
	h.sessions.Delete(tokenID)
	// Scala invalidates the session before creating the Controller request, so X-R-Sess is empty.
	h.proxyAuthenticated(c, http.MethodDelete, "/auth", true)
}

func (h *Handler) EULA(c *gin.Context, localOEM bool) {
	if localOEM {
		c.Header("Content-Type", "application/json")
		c.JSON(http.StatusOK, gin.H{"eula": gin.H{"accepted": true}})
		return
	}
	headers := make(http.Header)
	headers.Set("X-R-SSO", c.DefaultQuery("isSSO", "false"))
	response, err := h.controller.Do(c.Request.Context(), http.MethodGet, "/eula", nil, headers)
	if err != nil {
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		copyControllerResponse(c, response)
		return
	}
	var output eulaResponse
	if err := decodeControllerJSON(response, &output); err != nil {
		writeInternalError(c)
		return
	}
	c.Header("Content-Type", "application/json")
	c.JSON(http.StatusOK, output)
}

func (h *Handler) proxyAuthenticated(c *gin.Context, method, path string, sessionAlreadyDeleted bool) {
	tokenID := c.GetHeader("Token")
	headers := h.authenticatedHeaders(tokenID)
	if sessionAlreadyDeleted {
		headers.Set("X-R-Sess", "")
	}
	response, err := h.controller.Do(c.Request.Context(), method, path, nil, headers)
	if err != nil {
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		writeControllerAuthError(c, response.StatusCode)
		return
	}
	copyControllerResponse(c, response)
}

func (h *Handler) authenticatedHeaders(tokenID string) http.Header {
	headers := make(http.Header)
	headers.Set("X-Auth-Token", tokenID)
	headers.Set("Accept-Encoding", "gzip")
	headers.Set("Cache-Control", "no-cache")
	if entry, ok := h.sessions.Get(tokenID); ok {
		headers.Set("X-R-Sess", entry.SUSEToken)
	} else {
		headers.Set("X-R-Sess", "")
	}
	return headers
}

func decodeControllerJSON(response *http.Response, target any) error {
	var reader io.Reader = response.Body
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Encoding")), "gzip") {
		gzipReader, err := gzip.NewReader(response.Body)
		if err != nil {
			return fmt.Errorf("open gzip response: %w", err)
		}
		defer gzipReader.Close()
		reader = gzipReader
	}
	decoder := json.NewDecoder(io.LimitReader(reader, 50*1024*1024+1))
	return decoder.Decode(target)
}

func writeControllerAuthError(c *gin.Context, status int) {
	c.Header("Content-Type", "text/plain; charset=UTF-8")
	switch status {
	case http.StatusUnauthorized:
		c.String(http.StatusUnauthorized, "Authentication failed!")
	case http.StatusRequestTimeout:
		c.String(http.StatusRequestTimeout, "Session expired!")
	case http.StatusServiceUnavailable:
		c.String(http.StatusServiceUnavailable, "Server is not available!")
	default:
		c.String(http.StatusInternalServerError, "Internal server error")
	}
}

func writeInternalError(c *gin.Context) {
	c.Header("Content-Type", "text/plain; charset=UTF-8")
	c.String(http.StatusInternalServerError, "Internal server error")
}
