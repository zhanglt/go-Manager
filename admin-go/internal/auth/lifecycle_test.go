package auth

import (
	"bytes"
	"compress/gzip"
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

func TestSelfConvertsUserAndPropagatesSUSESession(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/selfuser" || r.Header.Get("X-Auth-Token") != "long-enough-test-token" || r.Header.Get("X-R-Sess") != "suse-cookie" {
			t.Errorf("unexpected Controller request: %s headers=%v", r.URL.Path, r.Header)
		}
		var compressed bytes.Buffer
		gzipWriter := gzip.NewWriter(&compressed)
		_, _ = gzipWriter.Write([]byte(`{
			"global_permissions":[],"remote_global_permissions":[],"domain_permissions":{},
			"password_days_until_expire":7,
			"user":{"fullname":"Test User","server":"local","username":"test","password":"",
			"email":"test@example.invalid","role":"reader","locale":"en","timeout":42,
			"default_password":false,"modify_password":false,"password_resettable":true,
			"blocked_for_failed_login":false,"blocked_for_password_expired":false,
			"role_domains":{},"extra_permissions":[],"extra_permissions_domains":[]}
		}`))
		_ = gzipWriter.Close()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressed.Bytes())
	}))
	defer controllerServer.Close()

	store := session.NewStore(10)
	store.Put("long-enough-test-token", "suse-cookie")
	handler := lifecycleTestHandler(t, controllerServer, store)
	request := httptest.NewRequest(http.MethodGet, "/self?isOnNV=false&isRancherSSOUrl=true", nil)
	request.Header.Set("Token", "long-enough-test-token")
	request.AddCookie(&http.Cookie{Name: "R_SESS", Value: "suse-cookie"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var output map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	tokenOutput := output["token"].(map[string]any)
	if tokenOutput["timeout"] != float64(300) || output["is_suse_authenticated"] != true {
		t.Fatalf("unexpected compatibility response: %v", output)
	}
}

func TestSelfRelogsInWhenSUSECookieChanges(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/auth" || r.Header.Get("X-R-Sess") != "new-cookie" {
			t.Errorf("unexpected Controller request: %s %s headers=%v", r.Method, r.URL.Path, r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"password_days_until_expire":-1,"token":{"token":"replacement-token-12345","fullname":"User","server":"local","username":"user","email":null,"role":"reader","locale":"en","timeout":300,"default_password":false,"modify_password":false,"role_domains":{}}}`))
	}))
	defer controllerServer.Close()

	store := session.NewStore(10)
	store.Put("long-enough-test-token", "old-cookie")
	handler := lifecycleTestHandler(t, controllerServer, store)
	request := httptest.NewRequest(http.MethodGet, "/self?isRancherSSOUrl=true", nil)
	request.Header.Set("Token", "long-enough-test-token")
	request.AddCookie(&http.Cookie{Name: "R_SESS", Value: "new-cookie"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "replacement-token-12345") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestHeartbeatAndLogoutSessionLifecycle(t *testing.T) {
	requests := make(chan *http.Request, 2)
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer controllerServer.Close()

	store := session.NewStore(10)
	store.Put("long-enough-test-token", "suse-cookie")
	invalidator := &recordingInvalidator{}
	handler := lifecycleTestHandler(t, controllerServer, store, invalidator)
	for _, methodPath := range []struct{ method, path string }{{http.MethodPatch, "/heartbeat"}, {http.MethodDelete, "/auth"}} {
		request := httptest.NewRequest(methodPath.method, methodPath.path, nil)
		request.Header.Set("Token", "long-enough-test-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d", methodPath.method, methodPath.path, response.Code)
		}
	}
	heartbeat := <-requests
	logout := <-requests
	if heartbeat.Header.Get("X-R-Sess") != "suse-cookie" || logout.Header.Get("X-R-Sess") != "" {
		t.Fatalf("unexpected session propagation: heartbeat=%q logout=%q", heartbeat.Header.Get("X-R-Sess"), logout.Header.Get("X-R-Sess"))
	}
	if _, ok := store.Get("long-enough-test-token"); ok {
		t.Fatal("logout did not remove local session")
	}
	if len(invalidator.tokens) != 1 || invalidator.tokens[0] != "long-enough-test-token" {
		t.Fatalf("invalidated tokens = %v", invalidator.tokens)
	}
}

type recordingInvalidator struct{ tokens []string }

func (r *recordingInvalidator) DeleteToken(token string) { r.tokens = append(r.tokens, token) }

func TestLocalOEMEULA(t *testing.T) {
	store := session.NewStore(1)
	handler := &Handler{sessions: store, now: time.Now}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/eula", func(c *gin.Context) { handler.EULA(c, true) })
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/eula", nil))
	if response.Code != http.StatusOK || response.Body.String() != `{"eula":{"accepted":true}}` {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestControllerEULA(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/eula" || r.Header.Get("X-R-SSO") != "true" {
			t.Errorf("unexpected Controller EULA request: %s headers=%v", r.URL.Path, r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"eula":{"accepted":false}}`))
	}))
	defer controllerServer.Close()
	store := session.NewStore(1)
	baseURL, _ := url.Parse(controllerServer.URL + "/v1")
	handler := NewHandler(controller.NewWithHTTPClient(baseURL, controllerServer.Client()), store)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/eula", func(c *gin.Context) { handler.EULA(c, false) })
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/eula?isSSO=true", nil))
	if response.Code != http.StatusOK || response.Body.String() != `{"eula":{"accepted":false}}` {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestHeartbeatMapsControllerAuthenticationError(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer controllerServer.Close()
	store := session.NewStore(1)
	handler := lifecycleTestHandler(t, controllerServer, store)
	request := httptest.NewRequest(http.MethodPatch, "/heartbeat", nil)
	request.Header.Set("Token", "long-enough-test-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || response.Body.String() != "Authentication failed!" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}

func lifecycleTestHandler(t *testing.T, controllerServer *httptest.Server, store *session.Store, invalidators ...tokenInvalidator) http.Handler {
	t.Helper()
	baseURL, err := url.Parse(controllerServer.URL + "/v1")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(controller.NewWithHTTPClient(baseURL, controllerServer.Client()), store, invalidators...)
	handler.now = func() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) }
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/self", handler.Self)
	engine.PATCH("/heartbeat", handler.Heartbeat)
	engine.DELETE("/auth", handler.Logout)
	return engine
}
