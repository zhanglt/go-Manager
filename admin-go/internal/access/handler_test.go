package access

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/session"
)

func TestUpdateRoleEscapesClusterAndRoleSegments(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/v1/fed/cluster/member%2Fone/v1/user_role/admin%2Fname"
		if r.URL.EscapedPath() != wantPath {
			t.Errorf("escaped path = %q, want %q", r.URL.EscapedPath(), wantPath)
		}
		if r.Header.Get("X-Auth-Token") != "token" || r.Header.Get("X-R-Sess") != "cookie" {
			t.Errorf("unexpected auth headers: %v", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"config":{"name":"admin/name","comment":null,"permissions":[]}}` {
			t.Errorf("body = %q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer controllerServer.Close()

	engine, store := testEngine(t, controllerServer)
	store.Put("token", "cookie")
	store.SetCluster("token", "member/one")
	request := httptest.NewRequest(http.MethodPatch, "/role2", strings.NewReader(` { "config": { "name": "admin/name", "comment": null, "permissions": [] } } `))
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != `{"status":"ok"}` {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestCreateAPIKeyWithEmptyBody(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/api_key" || len(body) != 0 {
			t.Errorf("request path=%q body=%q", r.URL.Path, body)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer controllerServer.Close()
	engine, _ := testEngine(t, controllerServer)
	request := httptest.NewRequest(http.MethodPost, "/api_key", nil)
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestSwitchClusterAndReset(t *testing.T) {
	controllerServer := httptest.NewServer(http.NotFoundHandler())
	defer controllerServer.Close()
	invalidator := &recordingInvalidator{}
	engine, store := testEngine(t, controllerServer, invalidator)
	for _, test := range []struct {
		path string
		want string
		set  bool
	}{
		{"/fed/switch?id=member-one", `{"id":"member-one"}`, true},
		{"/fed/switch", `{"id":null}`, false},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.Header.Set("Token", "token")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != test.want {
			t.Fatalf("GET %s = %d %s", test.path, response.Code, response.Body.String())
		}
		_, set := store.Cluster("token")
		if set != test.set {
			t.Fatalf("cluster set = %v, want %v", set, test.set)
		}
	}
	if len(invalidator.tokens) != 2 || invalidator.tokens[0] != "token" || invalidator.tokens[1] != "token" {
		t.Fatalf("invalidated tokens = %v", invalidator.tokens)
	}
}

type recordingInvalidator struct{ tokens []string }

func (r *recordingInvalidator) DeleteToken(token string) { r.tokens = append(r.tokens, token) }

func TestDeleteRoleRequiresName(t *testing.T) {
	controllerServer := httptest.NewServer(http.NotFoundHandler())
	defer controllerServer.Close()
	engine, _ := testEngine(t, controllerServer)
	request := httptest.NewRequest(http.MethodDelete, "/role2", nil)
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
}

func testEngine(t *testing.T, controllerServer *httptest.Server, invalidators ...tokenInvalidator) (*gin.Engine, *session.Store) {
	t.Helper()
	baseURL, err := url.Parse(controllerServer.URL + "/v1")
	if err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(10)
	client := controller.NewWithHTTPClient(baseURL, controllerServer.Client())
	handler := NewHandler(client, controller.NewTargetResolver(baseURL, store), store, invalidators...)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.PATCH("/role2", handler.UpdateRole)
	engine.DELETE("/role2", handler.DeleteRole)
	engine.POST("/api_key", handler.AddOrCreateAPIKey)
	engine.GET("/fed/switch", handler.SwitchCluster)
	return engine, store
}
