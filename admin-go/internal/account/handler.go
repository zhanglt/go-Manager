package account

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/hashutil"
	"github.com/neuvector/manager/admin-go/internal/session"
)

type Handler struct {
	controller *controller.Client
	resolver   *controller.TargetResolver
	sessions   *session.Store
	now        func() time.Time
}

type permission struct {
	ID    string `json:"id"`
	Read  bool   `json:"read"`
	Write bool   `json:"write"`
}

type extraPermission struct {
	Permissions []permission `json:"permissions"`
	Domains     []string     `json:"domains"`
}

type user struct {
	Fullname                  string              `json:"fullname"`
	Server                    string              `json:"server"`
	Username                  string              `json:"username"`
	Password                  string              `json:"password"`
	Email                     *string             `json:"email"`
	Role                      string              `json:"role"`
	Locale                    string              `json:"locale"`
	Timeout                   *int                `json:"timeout"`
	DefaultPassword           bool                `json:"default_password"`
	ModifyPassword            bool                `json:"modify_password"`
	PasswordResettable        *bool               `json:"password_resettable"`
	BlockedForFailedLogin     *bool               `json:"blocked_for_failed_login"`
	BlockedForPasswordExpired *bool               `json:"blocked_for_password_expired"`
	RoleDomains               map[string][]string `json:"role_domains"`
	ExtraPermissions          []permission        `json:"extra_permissions"`
	ExtraPermissionsDomains   []extraPermission   `json:"extra_permissions_domains"`
}

type userImage struct {
	Fullname                  string              `json:"fullname"`
	Server                    string              `json:"server"`
	Username                  string              `json:"username"`
	Password                  string              `json:"password"`
	Email                     *string             `json:"email"`
	Role                      string              `json:"role"`
	Locale                    string              `json:"locale"`
	DefaultPassword           bool                `json:"default_password"`
	ModifyPassword            bool                `json:"modify_password"`
	PasswordResettable        *bool               `json:"password_resettable"`
	EmailHash                 string              `json:"emailHash"`
	BlockedForFailedLogin     *bool               `json:"blocked_for_failed_login"`
	BlockedForPasswordExpired *bool               `json:"blocked_for_password_expired"`
	RoleDomains               map[string][]string `json:"role_domains"`
	ExtraPermissions          []permission        `json:"extra_permissions"`
	ExtraPermissionsDomains   []extraPermission   `json:"extra_permissions_domains"`
}

type usersResponse struct {
	DomainRoles       []string `json:"domain_roles"`
	GlobalRoles       []string `json:"global_roles"`
	RolesNotForDomain []string `json:"roles_not_for_domain"`
	Users             []user   `json:"users"`
}

type usersOutput struct {
	DomainRoles       []string    `json:"domain_roles"`
	GlobalRoles       []string    `json:"global_roles"`
	RolesNotForDomain []string    `json:"roles_not_for_domain"`
	Users             []userImage `json:"users"`
}

type userProfile struct {
	Fullname        string              `json:"fullname"`
	Username        string              `json:"username"`
	Email           *string             `json:"email"`
	Role            *string             `json:"role"`
	Password        *string             `json:"password"`
	NewPassword     *string             `json:"new_password"`
	Timeout         *int                `json:"timeout"`
	Locale          string              `json:"locale"`
	DefaultPassword bool                `json:"default_password"`
	ModifyPassword  bool                `json:"modify_password"`
	RoleDomains     map[string][]string `json:"role_domains"`
}

func NewHandler(client *controller.Client, resolver *controller.TargetResolver, sessions *session.Store) *Handler {
	return &Handler{controller: client, resolver: resolver, sessions: sessions, now: time.Now}
}

func (h *Handler) ValidateToken(c *gin.Context) {
	value, ok := h.sessions.TokenJSON(c.GetHeader("Token"))
	if !ok {
		value = []byte("null")
	}
	c.Data(http.StatusOK, "application/json", value)
}

func (h *Handler) GetUsers(c *gin.Context) {
	if name, present := c.GetQuery("name"); present {
		h.getUser(c, name)
		return
	}
	target, err := h.resolver.Resolve(c.GetHeader("Token"), controller.V1, "user")
	if err != nil {
		writeInternalError(c)
		return
	}
	response, err := h.do(c, http.MethodGet, target, nil)
	if err != nil {
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		copyResponse(c, response)
		return
	}
	var source usersResponse
	if err := decodeJSON(response, &source); err != nil {
		writeInternalError(c)
		return
	}
	output := usersOutput{
		DomainRoles: source.DomainRoles, GlobalRoles: source.GlobalRoles,
		RolesNotForDomain: source.RolesNotForDomain, Users: make([]userImage, len(source.Users)),
	}
	for index, current := range source.Users {
		output.Users[index] = userImage{
			Fullname: current.Fullname, Server: current.Server, Username: current.Username,
			Password: current.Password, Email: current.Email, Role: current.Role, Locale: current.Locale,
			DefaultPassword: current.DefaultPassword, ModifyPassword: current.ModifyPassword,
			PasswordResettable: current.PasswordResettable, EmailHash: emailHash(current.Email),
			BlockedForFailedLogin:     current.BlockedForFailedLogin,
			BlockedForPasswordExpired: current.BlockedForPasswordExpired,
			RoleDomains:               current.RoleDomains, ExtraPermissions: current.ExtraPermissions,
			ExtraPermissionsDomains: current.ExtraPermissionsDomains,
		}
	}
	writeJSON(c, http.StatusOK, output)
}

func (h *Handler) getUser(c *gin.Context, name string) {
	target, err := h.resolver.Resolve("", controller.V1, "user", name)
	if err != nil {
		writeInternalError(c)
		return
	}
	response, err := h.do(c, http.MethodGet, target, nil)
	if err != nil {
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		copyResponse(c, response)
		return
	}
	var wrapped struct {
		User user `json:"user"`
	}
	if err := decodeJSON(response, &wrapped); err != nil {
		writeInternalError(c)
		return
	}
	output := newTokenResponse(c.GetHeader("Token"), wrapped.User, h.now())
	encoded, _ := json.Marshal(output)
	h.sessions.SetTokenJSON(c.GetHeader("Token"), encoded)
	writeJSON(c, http.StatusOK, output)
}

func (h *Handler) AddUser(c *gin.Context) {
	var input user
	if !decodeBody(c, &input) {
		return
	}
	body, _ := json.Marshal(struct {
		User user `json:"user"`
	}{User: input})
	h.proxy(c, http.MethodPost, body, true, "user")
}

func (h *Handler) UpdateUser(c *gin.Context) {
	var input userProfile
	if !decodeBody(c, &input) {
		return
	}
	body, _ := json.Marshal(struct {
		Config userProfile `json:"config"`
	}{Config: input})
	target, err := h.resolver.Resolve("", controller.V1, "user", input.Fullname)
	if err != nil {
		writeInternalError(c)
		return
	}
	response, err := h.do(c, http.MethodPatch, target, body)
	if err != nil {
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		copyResponse(c, response)
		return
	}
	role := "none"
	if input.Role != nil {
		role = *input.Role
	}
	output := gin.H{
		"token": gin.H{
			"token": c.GetHeader("Token"), "fullname": input.Fullname, "server": "",
			"username": input.Username, "email": input.Email, "role": role, "locale": input.Locale,
			"timeout": input.Timeout, "default_password": input.DefaultPassword,
			"modify_password": input.ModifyPassword, "role_domains": input.RoleDomains,
		},
		"emailHash": emailHash(input.Email),
	}
	writeJSON(c, http.StatusOK, output)
}

func (h *Handler) DeleteUser(c *gin.Context) {
	userID, present := c.GetQuery("userId")
	if !present {
		writeBadRequest(c, "query parameter 'userId' is required")
		return
	}
	h.proxy(c, http.MethodDelete, nil, true, "user", userID)
}

func (h *Handler) PasswordPublic(c *gin.Context) {
	h.proxy(c, http.MethodGet, nil, true, "password_profile", "nvsyspwdprofile")
}

func (h *Handler) PasswordProfile(c *gin.Context) {
	h.proxy(c, http.MethodGet, nil, true, "password_profile")
}

func (h *Handler) UpdateUserBlock(c *gin.Context) {
	body, raw, ok := decodeGenericBody(c)
	if !ok {
		return
	}
	config, _ := body["config"].(map[string]any)
	fullname, _ := config["fullname"].(string)
	if fullname == "" {
		writeBadRequest(c, "user block config.fullname is required")
		return
	}
	h.proxy(c, http.MethodPost, raw, true, "user", fullname, "password")
}

func (h *Handler) UpdatePasswordProfile(c *gin.Context) {
	body, raw, ok := decodeGenericBody(c)
	if !ok {
		return
	}
	config, _ := body["config"].(map[string]any)
	name, _ := config["name"].(string)
	if name == "" {
		writeBadRequest(c, "password profile config.name is required")
		return
	}
	h.proxy(c, http.MethodPatch, raw, true, "password_profile", name)
}

func (h *Handler) SetEULA(c *gin.Context) {
	var input struct {
		Accepted bool `json:"accepted"`
	}
	if !decodeBody(c, &input) {
		return
	}
	body, _ := json.Marshal(struct {
		EULA any `json:"eula"`
	}{EULA: input})
	h.proxy(c, http.MethodPost, body, false, "eula")
}

func (h *Handler) GetLicense(c *gin.Context) {
	h.proxy(c, http.MethodGet, nil, true, "system", "license")
}

func (h *Handler) RequestLicense(c *gin.Context) {
	_, body, ok := decodeGenericBody(c)
	if ok {
		h.proxy(c, http.MethodPost, body, true, "system", "license", "request")
	}
}

func (h *Handler) UpdateLicense(c *gin.Context) {
	_, body, ok := decodeGenericBody(c)
	if ok {
		h.proxy(c, http.MethodPost, body, true, "system", "license", "update")
	}
}

func (h *Handler) GetServers(c *gin.Context) {
	h.proxy(c, http.MethodGet, nil, true, "server")
}

func (h *Handler) AddServer(c *gin.Context) {
	_, body, ok := decodeGenericBody(c)
	if ok {
		h.proxy(c, http.MethodPost, body, true, "server")
	}
}

func (h *Handler) UpdateServer(c *gin.Context) {
	h.serverMutation(c, http.MethodPatch)
}

func (h *Handler) DeleteServer(c *gin.Context) {
	h.serverMutation(c, http.MethodDelete)
}

func (h *Handler) serverMutation(c *gin.Context, method string) {
	value, body, ok := decodeGenericBody(c)
	if !ok {
		return
	}
	config, _ := value["config"].(map[string]any)
	name, _ := config["name"].(string)
	if name == "" {
		writeBadRequest(c, "server config.name is required")
		return
	}
	h.proxy(c, method, body, true, "server", name)
}

func (h *Handler) TestServer(c *gin.Context) {
	_, body, ok := decodeGenericBody(c)
	if ok {
		h.proxy(c, http.MethodPost, body, true, "debug", "server", "test")
	}
}

func (h *Handler) proxy(c *gin.Context, method string, body []byte, clusterAware bool, segments ...string) {
	token := ""
	if clusterAware {
		token = c.GetHeader("Token")
	}
	target, err := h.resolver.Resolve(token, controller.V1, segments...)
	if err != nil {
		writeInternalError(c)
		return
	}
	response, err := h.do(c, method, target, body)
	if err != nil {
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	copyResponse(c, response)
}

func (h *Handler) do(c *gin.Context, method string, target *url.URL, body []byte) (*http.Response, error) {
	headers := make(http.Header)
	headers.Set("X-Auth-Token", c.GetHeader("Token"))
	headers.Set("Accept-Encoding", "gzip")
	headers.Set("Cache-Control", "no-cache")
	if entry, ok := h.sessions.Get(c.GetHeader("Token")); ok {
		headers.Set("X-R-Sess", entry.SUSEToken)
	} else {
		headers.Set("X-R-Sess", "")
	}
	if body != nil {
		headers.Set("Content-Type", "text/plain; charset=UTF-8")
	}
	return h.controller.DoTarget(c.Request.Context(), method, target, bytes.NewReader(body), headers)
}

func decodeBody(c *gin.Context, target any) bool {
	decoder := json.NewDecoder(c.Request.Body)
	if err := decoder.Decode(target); err != nil {
		writeReadError(c, err)
		return false
	}
	return true
}

func decodeGenericBody(c *gin.Context) (map[string]any, []byte, bool) {
	var value map[string]any
	if !decodeBody(c, &value) {
		return nil, nil, false
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		writeInternalError(c)
		return nil, nil, false
	}
	return value, encoded, true
}

func decodeJSON(response *http.Response, target any) error {
	var reader io.Reader = response.Body
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Encoding")), "gzip") {
		gzipReader, err := gzip.NewReader(response.Body)
		if err != nil {
			return err
		}
		defer gzipReader.Close()
		reader = gzipReader
	}
	return json.NewDecoder(io.LimitReader(reader, 50*1024*1024+1)).Decode(target)
}

func copyResponse(c *gin.Context, response *http.Response) {
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

func newTokenResponse(tokenID string, current user, now time.Time) any {
	return gin.H{
		"token": gin.H{
			"token": tokenID, "fullname": current.Fullname, "server": current.Server,
			"username": current.Username, "email": current.Email, "role": current.Role,
			"locale": current.Locale, "timeout": 300, "default_password": current.DefaultPassword,
			"modify_password": current.ModifyPassword, "extra_permissions": []permission{},
			"global_permissions": []permission{}, "remote_global_permissions": []permission{},
			"domain_permissions": map[string][]permission{}, "password_days_until_expire": -1,
		},
		"emailHash": emailHash(current.Email), "roles": roleDigits(current.Role, current.RoleDomains),
		"login_timestamp":        now.Format("2006-01-02T15:04:05.000-0700"),
		"need_to_reset_password": false, "is_suse_authenticated": false,
	}
}

func roleDigits(globalRole string, domains map[string][]string) map[string]string {
	digits := map[string]string{"fedAdmin": "4", "fedReader": "3", "admin": "2", "reader": "1"}
	result := map[string]string{"global": digits[globalRole]}
	if result["global"] == "" {
		result["global"] = "0"
	}
	for role, names := range domains {
		for _, name := range names {
			result[name] = digits[role]
			if result[name] == "" {
				result[name] = "0"
			}
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

func writeJSON(c *gin.Context, status int, value any) {
	c.Header("Content-Type", "application/json")
	c.JSON(status, value)
}

func writeBadRequest(c *gin.Context, message string) {
	c.Header("Content-Type", "text/plain; charset=UTF-8")
	c.String(http.StatusBadRequest, "%s", message)
}

func writeReadError(c *gin.Context, err error) {
	var sizeError *http.MaxBytesError
	if errors.As(err, &sizeError) {
		c.String(http.StatusRequestEntityTooLarge, "Request entity too large")
		return
	}
	writeBadRequest(c, "invalid JSON request body")
}

func writeInternalError(c *gin.Context) {
	c.Header("Content-Type", "application/json")
	c.String(http.StatusInternalServerError, "Internal server error")
}
