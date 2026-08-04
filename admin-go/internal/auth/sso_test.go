package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/session"
)

func TestSAMLConcurrentBrowsersAreIsolated(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request ssoRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		writeSSOControllerToken(w, request.Token.Token)
	}))
	defer controllerServer.Close()
	handler := testSSOHandler(t, controllerServer, SSOOptions{SecureCookies: true})
	engine := gin.New()
	engine.POST("/token_auth_server", handler.PostSAMLAuthServer)
	engine.PATCH("/token_auth_server", handler.PatchSAMLAuthServer)

	first := performSAMLCallback(t, engine, "first")
	second := performSAMLCallback(t, engine, "second")
	if first.Value == second.Value {
		t.Fatal("concurrent callbacks received the same handoff capability")
	}
	assertConsumedUsername(t, engine, second, "second")
	assertConsumedUsername(t, engine, first, "first")
}

func TestSSOHandoffExpiresAndCannotCrossInstances(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSSOControllerToken(w, "fixture")
	}))
	defer controllerServer.Close()
	first := testSSOHandler(t, controllerServer, SSOOptions{SecureCookies: true, TTL: time.Minute})
	second := testSSOHandler(t, controllerServer, SSOOptions{SecureCookies: true, TTL: time.Minute})
	engine := gin.New()
	engine.POST("/token_auth_server", first.PostSAMLAuthServer)
	cookie := performSAMLCallback(t, engine, "fixture")

	otherEngine := gin.New()
	otherEngine.PATCH("/token_auth_server", second.PatchSAMLAuthServer)
	response := performCookieRequest(otherEngine, http.MethodPatch, "/token_auth_server", cookie)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("cross-instance consume status=%d, want 401", response.Code)
	}

	clock := time.Now()
	first.ssoResults.now = func() time.Time { return clock }
	cookie = performSAMLCallback(t, engine, "fixture")
	clock = clock.Add(2 * time.Minute)
	engine.PATCH("/consume", first.PatchSAMLAuthServer)
	response = performCookieRequest(engine, http.MethodPatch, "/consume", cookie)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expired consume status=%d, want 401", response.Code)
	}
}

func TestOpenIDStateIsBoundOneTimeAndResultIsOneTime(t *testing.T) {
	const state = "opaque-controller-state"
	var callbackCalls atomic.Int32
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/token_auth_server/openId1":
			_, _ = w.Write([]byte(`{"redirect":{"redirect_url":"https://idp.example/authorize?state=` + state + `"}}`))
		case "/v1/auth/openId1":
			callbackCalls.Add(1)
			var request ssoRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			if request.Token == nil || request.Token.Token != "authorization-code" || request.Token.State == nil || *request.Token.State != state {
				t.Errorf("unexpected callback payload: %+v", request)
			}
			writeSSOControllerToken(w, "oidc-user")
		default:
			http.NotFound(w, r)
		}
	}))
	defer controllerServer.Close()
	handler := testSSOHandler(t, controllerServer, SSOOptions{SecureCookies: true})
	engine := gin.New()
	engine.GET("/openId_auth", handler.CompleteOpenIDAuth)
	engine.PATCH("/openId_auth", handler.PatchOpenIDAuth)

	flow := initiateOIDC(t, engine)
	bad := performCookieRequest(engine, http.MethodGet, "/openId_auth?code=stolen&state=wrong", flow)
	if bad.Code != http.StatusBadRequest || callbackCalls.Load() != 0 {
		t.Fatalf("mismatched state status=%d callback_calls=%d", bad.Code, callbackCalls.Load())
	}
	flow = initiateOIDC(t, engine)
	callback := performCookieRequest(engine, http.MethodGet, "/openId_auth?code=authorization-code&state="+state, flow)
	if callback.Code != http.StatusFound || callbackCalls.Load() != 1 {
		t.Fatalf("valid callback status=%d callback_calls=%d", callback.Code, callbackCalls.Load())
	}
	handoff := findCookie(t, callback.Result(), ssoHandoffCookie)
	assertConsumedUsername(t, engine, handoff, "oidc-user")
	replay := performCookieRequest(engine, http.MethodGet, "/openId_auth?code=authorization-code&state="+state, flow)
	if replay.Code != http.StatusBadRequest || callbackCalls.Load() != 1 {
		t.Fatalf("replayed state status=%d callback_calls=%d", replay.Code, callbackCalls.Load())
	}
}

func TestOpenIDStateExpires(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"redirect":{"redirect_url":"https://idp.example/authorize?state=soon-expired"}}`))
	}))
	defer controllerServer.Close()
	handler := testSSOHandler(t, controllerServer, SSOOptions{SecureCookies: true, TTL: time.Minute})
	clock := time.Now()
	handler.oidcStates.now = func() time.Time { return clock }
	engine := gin.New()
	engine.GET("/openId_auth", handler.CompleteOpenIDAuth)
	flow := initiateOIDC(t, engine)
	clock = clock.Add(2 * time.Minute)
	response := performCookieRequest(engine, http.MethodGet, "/openId_auth?code=code&state=soon-expired", flow)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expired state status=%d, want 400", response.Code)
	}
}

func TestConfiguredPublicURLIgnoresHostHeader(t *testing.T) {
	var callbackURL string
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request ssoRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		callbackURL = *request.Token.RedirectEndpoint
		writeSSOControllerToken(w, "fixture")
	}))
	defer controllerServer.Close()
	publicURL, _ := url.Parse("https://manager.example:8443")
	handler := testSSOHandler(t, controllerServer, SSOOptions{PublicURL: publicURL, PathPrefix: "/console", SecureCookies: true})
	engine := gin.New()
	engine.POST("/console/token_auth_server", handler.PostSAMLAuthServer)
	request := httptest.NewRequest(http.MethodPost, "/console/token_auth_server", strings.NewReader("assertion"))
	request.Host = "attacker.example"
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if callbackURL != "https://manager.example:8443/console/token_auth_server" || response.Header().Get("Location") != "/console/" {
		t.Fatalf("callback=%q location=%q", callbackURL, response.Header().Get("Location"))
	}

	invalid := httptest.NewRequest(http.MethodPost, "/console/token_auth_server", strings.NewReader("assertion"))
	invalid.Host = "bad host"
	withoutPublicURL := testSSOHandler(t, controllerServer, SSOOptions{SecureCookies: true})
	invalidEngine := gin.New()
	invalidEngine.POST("/console/token_auth_server", withoutPublicURL.PostSAMLAuthServer)
	invalidResponse := httptest.NewRecorder()
	invalidEngine.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid Host status=%d, want 400", invalidResponse.Code)
	}
}

func testSSOHandler(t *testing.T, server *httptest.Server, options SSOOptions) *Handler {
	t.Helper()
	baseURL, err := url.Parse(server.URL + "/v1")
	if err != nil {
		t.Fatal(err)
	}
	return NewHandlerWithOptions(controller.NewWithHTTPClient(baseURL, server.Client()), session.NewStore(8), options)
}

func performSAMLCallback(t *testing.T, engine http.Handler, assertion string) *http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "https://manager.example/token_auth_server", strings.NewReader(assertion))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("SAML callback status=%d body=%q", response.Code, response.Body.String())
	}
	return findCookie(t, response.Result(), ssoHandoffCookie)
}

func initiateOIDC(t *testing.T, engine http.Handler) *http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "https://manager.example/openId_auth?serverName=openId1", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("OpenID initiation status=%d body=%q", response.Code, response.Body.String())
	}
	return findCookie(t, response.Result(), oidcFlowCookie)
}

func findCookie(t *testing.T, response *http.Response, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name == name && cookie.MaxAge >= 0 {
			return cookie
		}
	}
	t.Fatalf("cookie %q not found in %v", name, response.Header.Values("Set-Cookie"))
	return nil
}

func performCookieRequest(engine http.Handler, method, target string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func assertConsumedUsername(t *testing.T, engine http.Handler, cookie *http.Cookie, want string) {
	t.Helper()
	response := performCookieRequest(engine, http.MethodPatch, "/token_auth_server", cookie)
	if response.Code == http.StatusNotFound {
		response = performCookieRequest(engine, http.MethodPatch, "/openId_auth", cookie)
	}
	if response.Code != http.StatusOK {
		t.Fatalf("consume status=%d body=%q", response.Code, response.Body.String())
	}
	var result loginResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Token == nil || result.Token.Username != want {
		t.Fatalf("consumed username=%v, want %q", result.Token, want)
	}
	second := performCookieRequest(engine, http.MethodPatch, "/token_auth_server", cookie)
	if second.Code == http.StatusNotFound {
		second = performCookieRequest(engine, http.MethodPatch, "/openId_auth", cookie)
	}
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("second consume status=%d, want 401", second.Code)
	}
}

func writeSSOControllerToken(w http.ResponseWriter, username string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"password_days_until_expire":-1,"need_to_reset_password":false,"token":{"token":"token-%s","fullname":"Fixture","server":"local","username":%q,"role":"admin","locale":"en","default_password":false,"modify_password":false,"role_domains":{}}}`, username, username)
}
