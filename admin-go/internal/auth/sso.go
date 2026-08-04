package auth

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	managerMiddleware "github.com/neuvector/manager/admin-go/internal/middleware"
)

const (
	ssoMarkerCookie  = "temp"
	ssoHandoffCookie = "nv_sso_handoff"
	oidcFlowCookie   = "nv_oidc_flow"
	ssoMarkerValue   = "samlSso"
	maxOIDCParameter = 4096
)

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
	redirect, err := h.ssoCallbackURL(c, "/token_auth_server")
	if err != nil {
		clearSSOCookies(c, h.ssoOptions)
		c.String(http.StatusBadRequest, "Invalid public callback URL")
		return
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeBadRequest(c, err)
		return
	}
	payload, err := json.Marshal(ssoRequest{ClientIP: clientIP(c.Request.RemoteAddr), Token: &ssoToken{Token: string(body), RedirectEndpoint: &redirect}})
	if err != nil {
		writeInternalError(c)
		return
	}
	response, err := h.controller.Do(c.Request.Context(), http.MethodPost, "/auth/saml1", bytes.NewReader(payload), http.Header{"Content-Type": {"application/json"}})
	if err != nil {
		clearSSOCookies(c, h.ssoOptions)
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		clearSSOCookies(c, h.ssoOptions)
		redirectSSORoot(c, http.StatusMovedPermanently, true, h.ssoOptions.PathPrefix)
		return
	}
	var source controllerResponse
	if err := json.NewDecoder(response.Body).Decode(&source); err != nil {
		clearSSOCookies(c, h.ssoOptions)
		writeInternalError(c)
		return
	}
	if source.Token == nil || source.Token.Token == "" {
		clearSSOCookies(c, h.ssoOptions)
		writeInternalError(c)
		return
	}
	if !h.storeSSOResult(c, convertResponse(source, h.now(), false)) {
		writeInternalError(c)
		return
	}
	redirectSSORoot(c, http.StatusFound, true, h.ssoOptions.PathPrefix)
}

func (h *Handler) PatchSAMLAuthServer(c *gin.Context) { h.consumeSSOToken(c) }
func (h *Handler) PatchOpenIDAuth(c *gin.Context)     { h.consumeSSOToken(c) }

func (h *Handler) consumeSSOToken(c *gin.Context) {
	cookie, err := c.Request.Cookie(ssoHandoffCookie)
	clearSSOCookies(c, h.ssoOptions)
	if err != nil || cookie.Value == "" {
		c.String(http.StatusUnauthorized, "Authentication failed!")
		return
	}
	value, found := h.ssoResults.take(cookie.Value)
	if !found {
		c.String(http.StatusUnauthorized, "Authentication failed!")
		return
	}
	payload, err := json.Marshal(value)
	if err != nil {
		writeInternalError(c)
		return
	}
	c.Data(http.StatusOK, "application/json", payload)
}

func (h *Handler) CompleteOpenIDAuth(c *gin.Context) {
	state, hasState := c.GetQuery("state")
	if !hasState {
		h.GetOpenIDAuth(c)
		return
	}
	code, hasCode := c.GetQuery("code")
	flowCookie, cookieErr := c.Request.Cookie(oidcFlowCookie)
	clearCookie(c, oidcFlowCookie, h.ssoOptions)
	if !hasCode || code == "" || state == "" || len(code) > maxOIDCParameter || len(state) > maxOIDCParameter || cookieErr != nil {
		c.String(http.StatusBadRequest, "Invalid OpenID callback")
		return
	}
	expectedState, found := h.oidcStates.take(flowCookie.Value)
	if !found || subtle.ConstantTimeCompare([]byte(state), []byte(expectedState)) != 1 {
		c.String(http.StatusBadRequest, "Invalid OpenID callback")
		return
	}
	redirect, err := h.ssoCallbackURL(c, "/openId_auth")
	if err != nil {
		c.String(http.StatusBadRequest, "Invalid public callback URL")
		return
	}
	payload, err := json.Marshal(ssoRequest{ClientIP: clientIP(c.Request.RemoteAddr), Token: &ssoToken{Token: code, State: &state, RedirectEndpoint: &redirect}})
	if err != nil {
		writeInternalError(c)
		return
	}
	response, err := h.controller.Do(c.Request.Context(), http.MethodPost, "/auth/openId1", bytes.NewReader(payload), http.Header{"Content-Type": {"application/json"}})
	if err != nil {
		clearSSOCookies(c, h.ssoOptions)
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		clearSSOCookies(c, h.ssoOptions)
		redirectSSORoot(c, http.StatusMovedPermanently, false, h.ssoOptions.PathPrefix)
		return
	}
	var source controllerResponse
	if err := json.NewDecoder(response.Body).Decode(&source); err != nil {
		clearSSOCookies(c, h.ssoOptions)
		writeInternalError(c)
		return
	}
	if source.Token == nil || source.Token.Token == "" {
		clearSSOCookies(c, h.ssoOptions)
		writeInternalError(c)
		return
	}
	if !h.storeSSOResult(c, convertResponse(source, h.now(), false)) {
		writeInternalError(c)
		return
	}
	redirectSSORoot(c, http.StatusFound, false, h.ssoOptions.PathPrefix)
}

func (h *Handler) storeSSOResult(c *gin.Context, value loginResponse) bool {
	id, err := randomID()
	if err != nil || !h.ssoResults.put(id, value) {
		clearSSOCookies(c, h.ssoOptions)
		return false
	}
	setCookie(c, ssoMarkerCookie, base64.StdEncoding.EncodeToString([]byte(ssoMarkerValue)), false, h.ssoOptions)
	setCookie(c, ssoHandoffCookie, id, true, h.ssoOptions)
	return true
}

func (h *Handler) bindOIDCState(c *gin.Context, payload []byte) error {
	var response struct {
		Redirect struct {
			RedirectURL string `json:"redirect_url"`
		} `json:"redirect"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return err
	}
	redirect, err := url.Parse(response.Redirect.RedirectURL)
	if err != nil || !redirect.IsAbs() {
		return errors.New("controller returned an invalid OpenID redirect URL")
	}
	state := redirect.Query().Get("state")
	if state == "" || len(state) > maxOIDCParameter {
		return errors.New("controller OpenID redirect URL has no state")
	}
	flowID, err := randomID()
	if err != nil || !h.oidcStates.put(flowID, state) {
		return errors.New("cannot store OpenID state")
	}
	setCookie(c, oidcFlowCookie, flowID, true, h.ssoOptions)
	return nil
}

func randomID() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func (h *Handler) ssoCallbackURL(c *gin.Context, endpoint string) (string, error) {
	path := strings.TrimSuffix(h.ssoOptions.PathPrefix, "/") + endpoint
	if h.ssoOptions.PublicURL != nil {
		result := *h.ssoOptions.PublicURL
		result.Path = strings.TrimSuffix(result.Path, "/") + path
		result.RawPath, result.RawQuery, result.Fragment = "", "", ""
		return result.String(), nil
	}
	host := c.Request.Host
	if host == "" || strings.ContainsAny(host, "\\/?#@\r\n\t ") {
		return "", errors.New("invalid Host header")
	}
	parsed, err := url.Parse("//" + host)
	if err != nil || parsed.Host != host || parsed.Hostname() == "" {
		return "", errors.New("invalid Host header")
	}
	scheme := "http"
	if h.ssoOptions.SecureCookies {
		scheme = "https"
	}
	return (&url.URL{Scheme: scheme, Host: host, Path: path}).String(), nil
}

func setCookie(c *gin.Context, name, value string, httpOnly bool, options SSOOptions) {
	path := strings.TrimSuffix(options.PathPrefix, "/") + "/"
	http.SetCookie(c.Writer, &http.Cookie{
		Name: name, Value: value, Path: path, MaxAge: int(options.TTL.Seconds()),
		Secure: options.SecureCookies, HttpOnly: httpOnly, SameSite: http.SameSiteLaxMode,
	})
}

func clearSSOCookies(c *gin.Context, options SSOOptions) {
	clearCookie(c, ssoMarkerCookie, options)
	clearCookie(c, ssoHandoffCookie, options)
}

func clearCookie(c *gin.Context, name string, options SSOOptions) {
	path := strings.TrimSuffix(options.PathPrefix, "/") + "/"
	http.SetCookie(c.Writer, &http.Cookie{
		Name: name, Path: path, MaxAge: -1, Expires: time.Unix(1, 0),
		Secure: options.SecureCookies, HttpOnly: name != ssoMarkerCookie, SameSite: http.SameSiteLaxMode,
	})
}

func redirectSSORoot(c *gin.Context, status int, removeSecurityHeaders bool, prefix string) {
	if removeSecurityHeaders {
		managerMiddleware.RemoveSecurityHeaders(c.Writer.Header())
	}
	root := strings.TrimSuffix(prefix, "/") + "/"
	c.Header("Location", root)
	if status == http.StatusFound {
		c.Data(status, "text/html; charset=UTF-8", []byte(`The requested resource temporarily resides under <a href="`+root+`">this URI</a>.`))
		return
	}
	c.Data(status, "text/html; charset=UTF-8", []byte(`The requested resource has moved permanently to <a href="`+root+`">this URI</a>.`))
}
