package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	managerCache "github.com/neuvector/manager/admin-go/internal/cache"
	managerMiddleware "github.com/neuvector/manager/admin-go/internal/middleware"
)

const ssoTransientKey = "samlSso"

type ssoToken struct {
	Token            string  `json:"token"`
	State            *string `json:"state"`
	RedirectEndpoint *string `json:"redirect_endpoint"`
}

type ssoRequest struct {
	ClientIP string    `json:"client_ip"`
	Password *password `json:"password"`
	Token    *ssoToken `json:"Token"`
}

func (h *Handler) PostSAMLAuthServer(c *gin.Context) {
	host := ssoHost(c)
	if host == "" {
		c.String(http.StatusBadRequest, "Host header is missing")
		return
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeBadRequest(c, err)
		return
	}
	redirect := "https://" + host + "/token_auth_server"
	payload, err := json.Marshal(ssoRequest{ClientIP: clientIP(c.Request.RemoteAddr), Token: &ssoToken{Token: string(body), RedirectEndpoint: &redirect}})
	if err != nil {
		writeInternalError(c)
		return
	}
	response, err := h.controller.Do(c.Request.Context(), http.MethodPost, "/auth/saml1", bytes.NewReader(payload), http.Header{"Content-Type": {"application/json"}})
	if err != nil {
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		redirectSSORoot(c, http.StatusMovedPermanently, true)
		return
	}
	var source controllerResponse
	if err := json.NewDecoder(response.Body).Decode(&source); err != nil {
		writeInternalError(c)
		return
	}
	output := convertResponse(source, h.now(), false)
	encoded, err := json.Marshal(output)
	if err != nil || !h.sso.Set(ssoCacheKey(), []loginResponse{output}, int64(len(encoded))) {
		writeInternalError(c)
		return
	}
	setSSOCookie(c)
	redirectSSORoot(c, http.StatusFound, true)
}

func (h *Handler) PatchSAMLAuthServer(c *gin.Context) { h.consumeSSOToken(c) }
func (h *Handler) PatchOpenIDAuth(c *gin.Context)     { h.consumeSSOToken(c) }

func (h *Handler) consumeSSOToken(c *gin.Context) {
	values, found := h.sso.Take(ssoCacheKey())
	if !found || len(values) != 1 {
		c.String(http.StatusUnauthorized, "Authentication failed!")
		return
	}
	payload, err := json.Marshal(values[0])
	if err != nil {
		writeInternalError(c)
		return
	}
	c.Data(http.StatusOK, "application/json", payload)
}

func (h *Handler) CompleteOpenIDAuth(c *gin.Context) {
	_, hasState := c.GetQuery("state")
	if !hasState {
		h.GetOpenIDAuth(c)
		return
	}
	host := ssoHost(c)
	code, _ := c.GetQuery("code")
	state, _ := c.GetQuery("state")
	redirect := ""
	if host != "" {
		redirect = "https://" + host + "/openId_auth"
	}
	payload, err := json.Marshal(ssoRequest{ClientIP: clientIP(c.Request.RemoteAddr), Token: &ssoToken{Token: code, State: &state, RedirectEndpoint: &redirect}})
	if err != nil {
		writeInternalError(c)
		return
	}
	response, err := h.controller.Do(c.Request.Context(), http.MethodPost, "/auth/openId1", bytes.NewReader(payload), http.Header{"Content-Type": {"application/json"}})
	if err != nil {
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		redirectSSORoot(c, http.StatusMovedPermanently, false)
		return
	}
	var source controllerResponse
	if err := json.NewDecoder(response.Body).Decode(&source); err != nil {
		writeInternalError(c)
		return
	}
	output := convertResponse(source, h.now(), false)
	encoded, err := json.Marshal(output)
	if err != nil || !h.sso.Set(ssoCacheKey(), []loginResponse{output}, int64(len(encoded))) {
		writeInternalError(c)
		return
	}
	setSSOCookie(c)
	redirectSSORoot(c, http.StatusFound, false)
}

func ssoHost(c *gin.Context) string {
	if host := c.GetHeader("Host"); host != "" {
		return host
	}
	return c.Request.Host
}

func ssoCacheKey() managerCache.Key { return managerCache.Key{Domain: ssoTransientKey} }

func setSSOCookie(c *gin.Context) {
	c.Header("Set-Cookie", (&http.Cookie{Name: "temp", Value: base64.StdEncoding.EncodeToString([]byte(ssoTransientKey))}).String())
}

func redirectSSORoot(c *gin.Context, status int, removeSecurityHeaders bool) {
	if removeSecurityHeaders {
		managerMiddleware.RemoveSecurityHeaders(c.Writer.Header())
	}
	c.Header("Location", "/")
	if status == http.StatusFound {
		c.Data(status, "text/html; charset=UTF-8", []byte(`The requested resource temporarily resides under <a href="/">this URI</a>.`))
		return
	}
	c.Data(status, "text/html; charset=UTF-8", []byte(`The requested resource has moved permanently to <a href="/">this URI</a>.`))
}
