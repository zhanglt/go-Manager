package account

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/session"
)

func TestValidateTokenReturnsStoredValueOrNull(t *testing.T) {
	store := session.NewStore(2)
	store.SetTokenJSON("known", []byte(`{"token":{"token":"known"}}`))
	handler := &Handler{sessions: store}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/token", handler.ValidateToken)
	for _, test := range []struct{ token, want string }{
		{"known", `{"token":{"token":"known"}}`},
		{"missing", "null"},
	} {
		request := httptest.NewRequest(http.MethodPost, "/token", nil)
		request.Header.Set("Token", test.token)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != test.want {
			t.Errorf("token %q response = %d %s", test.token, response.Code, response.Body.String())
		}
	}
}

func TestUserListAddsEmailHashAndOmitsTimeout(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/fed/cluster/member-one/v1/user" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"domain_roles":null,"global_roles":[],"roles_not_for_domain":[],
			"users":[{"fullname":"User","server":"local","username":"user","password":"",
			"email":"user@example.invalid","role":"reader","locale":"en","timeout":42,
			"default_password":false,"modify_password":false,"password_resettable":true,
			"blocked_for_failed_login":false,"blocked_for_password_expired":false,
			"role_domains":{},"extra_permissions":[],"extra_permissions_domains":[]}]
		}`))
	}))
	defer controllerServer.Close()
	engine, store := accountTestEngine(t, controllerServer)
	store.SetCluster("token", "member-one")
	request := httptest.NewRequest(http.MethodGet, "/user", nil)
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var output map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	first := output["users"].([]any)[0].(map[string]any)
	if _, exists := first["timeout"]; exists {
		t.Errorf("user image unexpectedly contains timeout: %v", first)
	}
	if first["emailHash"] != "50e6044e3138e72bb23fdafaa213cd68" {
		t.Errorf("emailHash = %v", first["emailHash"])
	}
}

func TestUpdateUserBlockUsesEscapedPathSegment(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.EscapedPath(), "/user/name%2Fpart/password") {
			t.Errorf("escaped path = %q", r.URL.EscapedPath())
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer controllerServer.Close()
	engine, _ := accountTestEngine(t, controllerServer)
	request := httptest.NewRequest(http.MethodPost, "/password-profile/user", strings.NewReader(`{"config":{"fullname":"name/part"}}`))
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestSetEULAWrapsBodyAndUsesLocalController(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/eula" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["eula"].(map[string]any)["accepted"] != true {
			t.Errorf("body = %v", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer controllerServer.Close()
	engine, store := accountTestEngine(t, controllerServer)
	store.SetCluster("token", "member-one")
	request := httptest.NewRequest(http.MethodPost, "/eula", strings.NewReader(`{"accepted":true}`))
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestServerMutationEscapesName(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.EscapedPath(), "/server/ldap%2Fone") {
			t.Errorf("escaped path = %q", r.URL.EscapedPath())
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer controllerServer.Close()
	engine, _ := accountTestEngine(t, controllerServer)
	request := httptest.NewRequest(http.MethodPatch, "/server", strings.NewReader(`{"config":{"name":"ldap/one","ldap":null,"saml":null,"oidc":null}}`))
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func accountTestEngine(t *testing.T, controllerServer *httptest.Server) (*gin.Engine, *session.Store) {
	t.Helper()
	baseURL, err := url.Parse(controllerServer.URL + "/v1")
	if err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(10)
	client := controller.NewWithHTTPClient(baseURL, controllerServer.Client())
	handler := NewHandler(client, controller.NewTargetResolver(baseURL, store), store)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/user", handler.GetUsers)
	engine.POST("/password-profile/user", handler.UpdateUserBlock)
	engine.POST("/eula", handler.SetEULA)
	engine.PATCH("/server", handler.UpdateServer)
	return engine, store
}
