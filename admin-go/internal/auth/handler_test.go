package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/session"
)

func TestLoginConvertsControllerToken(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth" || r.Header.Get("X-R-Sess") != "rancher-cookie" {
			t.Errorf("controller request path=%q X-R-Sess=%q", r.URL.Path, r.Header.Get("X-R-Sess"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode controller request: %v", err)
		}
		if body["client_ip"] != "192.0.2.10" {
			t.Errorf("client_ip = %#v", body["client_ip"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"password_days_until_expire":12,
			"need_to_reset_password":true,
			"token":{
				"token":"fixture-token","fullname":"Fixture User","server":"local",
				"username":"fixture-user","email":"fixture@example.invalid","role":"admin",
				"locale":"en","timeout":300,"default_password":false,"modify_password":false,
				"role_domains":{"reader":["dev"]}
			}
		}`))
	}))
	defer controllerServer.Close()

	baseURL, _ := url.Parse(controllerServer.URL + "/v1")
	store := session.NewStore(10)
	invalidator := &recordingInvalidator{}
	handler := NewHandler(controller.NewWithHTTPClient(baseURL, controllerServer.Client()), store, invalidator)
	handler.now = func() time.Time { return time.Date(2026, 8, 1, 9, 10, 11, 120_000_000, time.FixedZone("CST", 8*60*60)) }
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/auth", handler.Login)
	request := httptest.NewRequest(http.MethodPost, "/auth", strings.NewReader(`{
		"username":"fixture-user","password":"fixture-password",
		"isRancherSSOUrl":true
	}`))
	request.RemoteAddr = "192.0.2.10:54321"
	request.AddCookie(&http.Cookie{Name: "R_SESS", Value: "rancher-cookie"})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var output map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if output["emailHash"] != "a403c50e83dfc222f341e532d0f4a6e2" || output["login_timestamp"] != "2026-08-01T09:10:11.120+0800" {
		t.Errorf("unexpected derived fields: %v", output)
	}
	if output["is_suse_authenticated"] != true || output["need_to_reset_password"] != false {
		t.Errorf("unexpected login flags: %v", output)
	}
	tokenOutput := output["token"].(map[string]any)
	if _, exists := tokenOutput["role_domains"]; exists {
		t.Errorf("compatibility token unexpectedly contains role_domains: %v", tokenOutput)
	}
	if tokenOutput["password_days_until_expire"] != float64(12) {
		t.Errorf("password_days_until_expire = %#v", tokenOutput["password_days_until_expire"])
	}
	roles := output["roles"].(map[string]any)
	if roles["global"] != "2" || roles["dev"] != "1" {
		t.Errorf("roles = %v", roles)
	}
	if len(invalidator.tokens) != 1 || invalidator.tokens[0] != "fixture-token" {
		t.Fatalf("invalidated tokens = %v", invalidator.tokens)
	}
	if entry, ok := store.Get("fixture-token"); !ok || entry.SUSEToken != "rancher-cookie" {
		t.Errorf("stored session = %+v, %v", entry, ok)
	}
}

func TestLoginRejectsMissingCompatibilityField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	handler := &Handler{}
	engine.POST("/auth", handler.Login)
	request := httptest.NewRequest(http.MethodPost, "/auth", strings.NewReader(`{"username":"user","password":"secret","new_password":null}`))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestPasswordAcceptsOptionalNewPassword(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
		want    *string
	}{
		{"omitted", `{"username":"user","password":"secret","isRancherSSOUrl":false}`, nil},
		{"null", `{"username":"user","password":"secret","isRancherSSOUrl":false,"new_password":null}`, nil},
		{"reset", `{"username":"user","password":"secret","isRancherSSOUrl":false,"new_password":"replacement"}`, stringPointer("replacement")},
	} {
		t.Run(test.name, func(t *testing.T) {
			var credentials password
			if err := json.Unmarshal([]byte(test.payload), &credentials); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if (credentials.NewPassword == nil) != (test.want == nil) {
				t.Fatalf("NewPassword = %#v, want %#v", credentials.NewPassword, test.want)
			}
			if test.want != nil && *credentials.NewPassword != *test.want {
				t.Errorf("NewPassword = %q, want %q", *credentials.NewPassword, *test.want)
			}
		})
	}
}

func stringPointer(value string) *string {
	return &value
}

func TestSAMLLogoutResponseRedirectsToRoot(t *testing.T) {
	engine := gin.New()
	handler := &Handler{}
	engine.GET("/samlslo", handler.SAMLLogoutResponse)
	engine.POST("/samlslo", handler.SAMLLogoutResponse)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(method, "/samlslo", nil))
		if response.Code != http.StatusFound || response.Header().Get("Location") != "/" || response.Header().Get("Content-Type") != "text/html; charset=UTF-8" || response.Body.String() != `The requested resource temporarily resides under <a href="/">this URI</a>.` {
			t.Fatalf("method=%s status=%d headers=%v body=%q", method, response.Code, response.Header(), response.Body.String())
		}
	}
}

func TestSAMLCallbackStoresOneTimeToken(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/auth/saml1" {
			t.Errorf("request=%s %s", r.Method, r.URL.Path)
		}
		var body ssoRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.ClientIP != "192.0.2.10" || body.Token == nil || body.Token.Token != "assertion" || body.Token.RedirectEndpoint == nil || *body.Token.RedirectEndpoint != "https://manager.example/token_auth_server" {
			t.Errorf("body=%+v", body)
		}
		_, _ = w.Write([]byte(`{"password_days_until_expire":-1,"need_to_reset_password":false,"token":{"token":"sso-token","fullname":"Fixture","server":"local","username":"fixture","email":"fixture@example.invalid","role":"admin","locale":"en","timeout":300,"default_password":false,"modify_password":false,"role_domains":{}}}`))
	}))
	defer controllerServer.Close()
	baseURL, _ := url.Parse(controllerServer.URL + "/v1")
	handler := NewHandler(controller.NewWithHTTPClient(baseURL, controllerServer.Client()), session.NewStore(4))
	engine := gin.New()
	engine.POST("/token_auth_server", handler.PostSAMLAuthServer)
	engine.PATCH("/token_auth_server", handler.PatchSAMLAuthServer)

	callback := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "http://manager.example/token_auth_server", strings.NewReader("assertion"))
	request.RemoteAddr = "192.0.2.10:4567"
	engine.ServeHTTP(callback, request)
	if callback.Code != http.StatusFound || callback.Header().Get("Location") != "/" || callback.Header().Get("Set-Cookie") != "temp=c2FtbFNzbw==" {
		t.Fatalf("callback=%d headers=%v body=%q", callback.Code, callback.Header(), callback.Body.String())
	}
	for index, want := range []int{http.StatusOK, http.StatusUnauthorized} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/token_auth_server", nil))
		if response.Code != want {
			t.Fatalf("consume %d: status=%d body=%s", index, response.Code, response.Body.String())
		}
	}
}
