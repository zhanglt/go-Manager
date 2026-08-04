package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/hashutil"
	managerMiddleware "github.com/neuvector/manager/admin-go/internal/middleware"
	"github.com/neuvector/manager/admin-go/internal/session"
)

type Handler struct {
	controller  *controller.Client
	sessions    *session.Store
	now         func() time.Time
	invalidator tokenInvalidator
	ssoResults  *transientStore[loginResponse]
	oidcStates  *transientStore[string]
	ssoOptions  SSOOptions
}

type tokenInvalidator interface {
	DeleteToken(string)
}

type password struct {
	Username        string  `json:"username"`
	Password        string  `json:"password"`
	IsRancherSSOURL bool    `json:"isRancherSSOUrl"`
	NewPassword     *string `json:"new_password"`
}

func (p *password) UnmarshalJSON(data []byte) error {
	var input struct {
		Username        *string         `json:"username"`
		Password        *string         `json:"password"`
		IsRancherSSOURL *bool           `json:"isRancherSSOUrl"`
		NewPassword     json.RawMessage `json:"new_password"`
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return err
	}
	if input.Username == nil || input.Password == nil || input.IsRancherSSOURL == nil {
		return errors.New("username, password, and isRancherSSOUrl are required")
	}
	p.Username = *input.Username
	p.Password = *input.Password
	p.IsRancherSSOURL = *input.IsRancherSSOURL
	p.NewPassword = nil
	if len(input.NewPassword) != 0 && string(input.NewPassword) != "null" {
		if err := json.Unmarshal(input.NewPassword, &p.NewPassword); err != nil {
			return fmt.Errorf("new_password: %w", err)
		}
	}
	return nil
}

type authRequest struct {
	Password password `json:"password"`
	ClientIP string   `json:"client_ip"`
}

type permission struct {
	ID    string `json:"id"`
	Read  bool   `json:"read"`
	Write bool   `json:"write"`
}

type token struct {
	Token                   string                  `json:"token"`
	Fullname                string                  `json:"fullname"`
	Server                  string                  `json:"server"`
	Username                string                  `json:"username"`
	Email                   *string                 `json:"email"`
	Role                    string                  `json:"role"`
	Locale                  string                  `json:"locale"`
	Timeout                 *int                    `json:"timeout"`
	DefaultPassword         bool                    `json:"default_password"`
	ModifyPassword          bool                    `json:"modify_password"`
	RoleDomains             map[string][]string     `json:"role_domains"`
	ExtraPermissions        []permission            `json:"extra_permissions"`
	GlobalPermissions       []permission            `json:"global_permissions"`
	RemoteGlobalPermissions []permission            `json:"remote_global_permissions"`
	DomainPermissions       map[string][]permission `json:"domain_permissions"`
}

type controllerResponse struct {
	PasswordDaysUntilExpire *int   `json:"password_days_until_expire"`
	NeedToResetPassword     *bool  `json:"need_to_reset_password"`
	Token                   *token `json:"token"`
}

type tokenNew struct {
	Token                   string                  `json:"token"`
	Fullname                string                  `json:"fullname"`
	Server                  string                  `json:"server"`
	Username                string                  `json:"username"`
	Email                   *string                 `json:"email"`
	Role                    string                  `json:"role"`
	Locale                  string                  `json:"locale"`
	Timeout                 *int                    `json:"timeout"`
	DefaultPassword         bool                    `json:"default_password"`
	ModifyPassword          bool                    `json:"modify_password"`
	ExtraPermissions        []permission            `json:"extra_permissions"`
	GlobalPermissions       []permission            `json:"global_permissions"`
	RemoteGlobalPermissions []permission            `json:"remote_global_permissions"`
	DomainPermissions       map[string][]permission `json:"domain_permissions"`
	PasswordDaysUntilExpire int                     `json:"password_days_until_expire"`
}

type loginResponse struct {
	Token               *tokenNew         `json:"token"`
	EmailHash           string            `json:"emailHash"`
	Roles               map[string]string `json:"roles"`
	LoginTimestamp      *string           `json:"login_timestamp"`
	NeedToResetPassword *bool             `json:"need_to_reset_password"`
	IsSUSEAuthenticated bool              `json:"is_suse_authenticated"`
}

func NewHandler(client *controller.Client, sessions *session.Store, invalidators ...tokenInvalidator) *Handler {
	return NewHandlerWithOptions(client, sessions, SSOOptions{SecureCookies: true}, invalidators...)
}

type SSOOptions struct {
	PublicURL     *url.URL
	PathPrefix    string
	TTL           time.Duration
	MaxEntries    int
	SecureCookies bool
}

func NewHandlerWithOptions(client *controller.Client, sessions *session.Store, options SSOOptions, invalidators ...tokenInvalidator) *Handler {
	var invalidator tokenInvalidator
	if len(invalidators) > 0 {
		invalidator = invalidators[0]
	}
	if options.TTL <= 0 {
		options.TTL = 5 * time.Minute
	}
	if options.MaxEntries <= 0 {
		options.MaxEntries = 1024
	}
	return &Handler{
		controller: client, sessions: sessions, now: time.Now, invalidator: invalidator,
		ssoResults: newTransientStore[loginResponse](options.MaxEntries, options.TTL),
		oidcStates: newTransientStore[string](options.MaxEntries, options.TTL),
		ssoOptions: options,
	}
}

func (h *Handler) Login(c *gin.Context) {
	var credentials password
	decoder := json.NewDecoder(c.Request.Body)
	if err := decoder.Decode(&credentials); err != nil {
		writeBadRequest(c, err)
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeBadRequest(c, err)
		return
	}
	h.login(c, credentials)
}

// SAMLLogoutResponse mirrors the legacy IdP logout callback, which always returns to the UI root.
func (h *Handler) SAMLLogoutResponse(c *gin.Context) {
	managerMiddleware.RemoveSecurityHeaders(c.Writer.Header())
	root := h.ssoOptions.PathPrefix + "/"
	c.Header("Location", root)
	c.Data(http.StatusFound, "text/html; charset=UTF-8", []byte(`The requested resource temporarily resides under <a href="`+root+`">this URI</a>.`))
}

func (h *Handler) GetSAMLAuthServer(c *gin.Context) {
	path := "/token_auth_server"
	var body io.Reader
	if serverName, present := c.GetQuery("serverName"); present {
		path = "/token_auth_server/saml1"
		redirectURL, err := h.ssoCallbackURL(c, "/token_auth_server")
		if err != nil {
			c.String(http.StatusBadRequest, "Invalid public callback URL")
			return
		}
		payload, _ := json.Marshal(struct {
			RedirectEndpoint string `json:"redirect_endpoint"`
			Issuer           string `json:"issuer"`
		}{redirectURL, redirectURL})
		body = bytes.NewReader(payload)
		_ = serverName // Scala uses only presence, not its value.
	}
	headers := make(http.Header)
	headers.Set("X-R-SSO", "false")
	response, err := h.controller.Do(c.Request.Context(), http.MethodGet, path, body, headers)
	if err != nil {
		c.String(http.StatusInternalServerError, "Internal server error")
		return
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil || !json.Valid(payload) {
		c.String(http.StatusInternalServerError, "Internal server error")
		return
	}
	c.Data(http.StatusOK, "application/json", payload)
}

func (h *Handler) GetOpenIDAuth(c *gin.Context) {
	if _, hasState := c.GetQuery("state"); hasState {
		c.String(http.StatusNotImplemented, "OpenID callback is not migrated")
		return
	}
	path, method := "/token_auth_server", http.MethodGet
	var body io.Reader
	if _, hasServerName := c.GetQuery("serverName"); hasServerName {
		path = "/token_auth_server/openId1"
		redirectURL, err := h.ssoCallbackURL(c, "/openId_auth")
		if err != nil {
			c.String(http.StatusBadRequest, "Invalid public callback URL")
			return
		}
		method = http.MethodPost
		payload, _ := json.Marshal(struct {
			RedirectEndpoint string `json:"redirect_endpoint"`
		}{redirectURL})
		body = bytes.NewReader(payload)
	}
	headers := make(http.Header)
	headers.Set("X-R-SSO", "false")
	response, err := h.controller.Do(c.Request.Context(), method, path, body, headers)
	if err != nil {
		c.String(http.StatusInternalServerError, "Internal server error")
		return
	}
	defer response.Body.Close()
	if method == http.MethodPost && response.StatusCode != http.StatusOK {
		clearCookie(c, oidcFlowCookie, h.ssoOptions)
		c.String(http.StatusBadGateway, "OpenID provider configuration failed")
		return
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil || !json.Valid(payload) {
		c.String(http.StatusInternalServerError, "Internal server error")
		return
	}
	if method == http.MethodPost {
		if err := h.bindOIDCState(c, payload); err != nil {
			clearCookie(c, oidcFlowCookie, h.ssoOptions)
			c.String(http.StatusBadGateway, "Invalid OpenID provider response")
			return
		}
	}
	c.Data(http.StatusOK, "application/json", payload)
}

func (h *Handler) GetSAMLAuthServerLogout(c *gin.Context) {
	logoutURL, err := h.ssoCallbackURL(c, "/samlslo")
	if err != nil {
		c.String(http.StatusBadRequest, "Invalid public callback URL")
		return
	}
	redirectURL, err := h.ssoCallbackURL(c, "/token_auth_server")
	if err != nil {
		c.String(http.StatusBadRequest, "Invalid public callback URL")
		return
	}
	payload, _ := json.Marshal(struct {
		RedirectEndpoint string `json:"redirect_endpoint"`
		Issuer           string `json:"issuer"`
	}{logoutURL, redirectURL})
	headers := make(http.Header)
	headers.Set("X-Auth-Token", c.GetHeader("Token"))
	headers.Set("X-R-Sess", "")
	headers.Set("Accept-Encoding", "gzip")
	headers.Set("Cache-Control", "no-cache")
	response, err := h.controller.Do(c.Request.Context(), http.MethodGet, "/token_auth_server/saml1/slo", bytes.NewReader(payload), headers)
	if err != nil {
		c.String(http.StatusInternalServerError, "Internal server error")
		return
	}
	defer response.Body.Close()
	copyControllerResponse(c, response)
}

func (h *Handler) login(c *gin.Context, credentials password) {
	requestBody, err := json.Marshal(authRequest{Password: credentials, ClientIP: clientIP(c.Request.RemoteAddr)})
	if err != nil {
		c.Header("Content-Type", "application/json")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Server internal error"})
		return
	}
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	if cookie, err := c.Request.Cookie("R_SESS"); err == nil && credentials.IsRancherSSOURL {
		headers.Set("X-R-Sess", cookie.Value)
	} else {
		headers.Set("X-R-Sess", "")
	}
	response, err := h.controller.Do(c.Request.Context(), http.MethodPost, "/auth", bytes.NewReader(requestBody), headers)
	if err != nil {
		c.Header("Content-Type", "text/plain; charset=UTF-8")
		c.String(http.StatusInternalServerError, "Controller unavailable!")
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		copyControllerResponse(c, response)
		return
	}

	var result controllerResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		c.Header("Content-Type", "application/json")
		c.JSON(http.StatusBadGateway, gin.H{"error": "Invalid Controller response"})
		return
	}
	c.Header("Content-Type", "application/json")
	output := convertResponse(result, h.now(), headers.Get("X-R-Sess") != "")
	if output.Token != nil {
		if h.invalidator != nil {
			h.invalidator.DeleteToken(output.Token.Token)
		}
		h.sessions.Put(output.Token.Token, headers.Get("X-R-Sess"))
		if encoded, encodeErr := json.Marshal(output); encodeErr == nil {
			h.sessions.SetTokenJSON(output.Token.Token, encoded)
		}
	}
	c.JSON(http.StatusOK, output)
}

func convertResponse(source controllerResponse, now time.Time, suseAuthenticated bool) loginResponse {
	if source.Token == nil {
		return loginResponse{
			Token: nil, EmailHash: "", Roles: nil, LoginTimestamp: nil,
			NeedToResetPassword: source.NeedToResetPassword, IsSUSEAuthenticated: suseAuthenticated,
		}
	}
	inputToken := source.Token
	if inputToken.ExtraPermissions == nil {
		inputToken.ExtraPermissions = []permission{}
	}
	if inputToken.GlobalPermissions == nil {
		inputToken.GlobalPermissions = []permission{}
	}
	if inputToken.RemoteGlobalPermissions == nil {
		inputToken.RemoteGlobalPermissions = []permission{}
	}
	if inputToken.DomainPermissions == nil {
		inputToken.DomainPermissions = map[string][]permission{}
	}
	expires := -1
	if source.PasswordDaysUntilExpire != nil {
		expires = *source.PasswordDaysUntilExpire
	}
	outputToken := &tokenNew{
		Token: inputToken.Token, Fullname: inputToken.Fullname, Server: inputToken.Server,
		Username: inputToken.Username, Email: inputToken.Email, Role: inputToken.Role,
		Locale: inputToken.Locale, Timeout: inputToken.Timeout,
		DefaultPassword: inputToken.DefaultPassword, ModifyPassword: inputToken.ModifyPassword,
		ExtraPermissions: inputToken.ExtraPermissions, GlobalPermissions: inputToken.GlobalPermissions,
		RemoteGlobalPermissions: inputToken.RemoteGlobalPermissions,
		DomainPermissions:       inputToken.DomainPermissions, PasswordDaysUntilExpire: expires,
	}
	stamp := now.Format("2006-01-02T15:04:05.000-0700")
	reset := false
	return loginResponse{
		Token:     outputToken,
		EmailHash: emailHash(inputToken.Email), Roles: roles(inputToken.Role, inputToken.RoleDomains),
		LoginTimestamp: &stamp, NeedToResetPassword: &reset, IsSUSEAuthenticated: suseAuthenticated,
	}
}

func roles(globalRole string, domains map[string][]string) map[string]string {
	digits := map[string]string{"fedAdmin": "4", "fedReader": "3", "admin": "2", "reader": "1"}
	digit := func(role string) string {
		if value, ok := digits[role]; ok {
			return value
		}
		return "0"
	}
	result := map[string]string{"global": digit(globalRole)}
	for role, names := range domains {
		for _, name := range names {
			result[name] = digit(role)
		}
	}
	return result
}

func emailHash(email *string) string {
	value := ""
	if email != nil {
		value = *email
	}
	return hashutil.CompatibilityHash(value)
}

func clientIP(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil {
		return host
	}
	return remoteAddress
}

func writeBadRequest(c *gin.Context, err error) {
	c.Header("Content-Type", "text/plain; charset=UTF-8")
	var sizeError *http.MaxBytesError
	if errors.As(err, &sizeError) {
		c.String(http.StatusRequestEntityTooLarge, "Request entity too large")
		return
	}
	c.String(http.StatusBadRequest, "Invalid request body: %s", err.Error())
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("multiple JSON values")
}

func copyControllerResponse(c *gin.Context, response *http.Response) {
	for name, values := range response.Header {
		if name == "Content-Length" || name == "Connection" || name == "Transfer-Encoding" {
			continue
		}
		for _, value := range values {
			c.Header(name, value)
		}
	}
	c.Status(response.StatusCode)
	_, _ = io.Copy(c.Writer, response.Body)
}
