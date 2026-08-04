package cluster

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

func TestGetMemberTransformsMasterAndJoints(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/fed/member" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("X-Auth-Token") != "token" || r.Header.Get("X-R-Sess") != "cookie" {
			t.Errorf("unexpected auth headers: %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"fed_role":"master","local_rest_info":{"server":"local","port":10443},
			"master_cluster":{"disabled":false,"name":"primary","id":"master-one","secret":"master-secret",
				"user":"admin","status":"connected","rest_version":"v1","rest_info":{"server":"master","port":10443}},
			"joint_clusters":[{"disabled":null,"name":"member","id":"member-one","secret":"joint-secret",
				"user":null,"status":"connected","rest_version":null,"rest_info":{"server":"joint","port":10444},"proxy_required":true}],
			"use_proxy":null,"deploy_repo_scan_data":true}`))
	}))
	defer controllerServer.Close()

	engine, store := testEngine(t, controllerServer)
	store.Put("token", "cookie")
	store.SetCluster("token", "switched-member")
	response := serve(engine, http.MethodGet, "/fed/member", "", "token")
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	want := `{"fed_role":"master","local_rest_info":{"server":"local","port":10443},"clusters":[` +
		`{"disabled":false,"name":"primary","id":"master-one","secret":"master-secret","api_server":"master","api_port":10443,"status":"connected","username":"admin","rest_version":"v1","clusterType":"master","proxy_required":false},` +
		`{"name":"member","id":"member-one","secret":"joint-secret","api_server":"joint","api_port":10444,"status":"connected","clusterType":"joint","proxy_required":true}],` +
		`"use_proxy":"","deploy_repo_scan_data":true}`
	if response.Body.String() != want {
		t.Fatalf("body = %s\nwant = %s", response.Body.String(), want)
	}
}

func TestGetMemberEmptyRoleUsesScalaDefaults(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"fed_role":"","local_rest_info":null,"master_cluster":null,"joint_clusters":null,"use_proxy":null,"deploy_repo_scan_data":null}`))
	}))
	defer controllerServer.Close()
	engine, _ := testEngine(t, controllerServer)
	response := serve(engine, http.MethodGet, "/fed/member", "", "token")
	want := `{"fed_role":"","deploy_repo_scan_data":false}`
	if response.Code != http.StatusOK || response.Body.String() != want {
		t.Fatalf("response = %d %s, want %s", response.Code, response.Body.String(), want)
	}
}

func TestGetMemberWithoutClusterArraysReturnsEmptyClusters(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"fed_role":"joint","local_rest_info":null,"master_cluster":null,"joint_clusters":null,"use_proxy":"https","deploy_repo_scan_data":null}`))
	}))
	defer controllerServer.Close()
	engine, _ := testEngine(t, controllerServer)
	response := serve(engine, http.MethodGet, "/fed/member", "", "token")
	want := `{"fed_role":"joint","clusters":[],"use_proxy":"https"}`
	if response.Code != http.StatusOK || response.Body.String() != want {
		t.Fatalf("response = %d %s, want %s", response.Code, response.Body.String(), want)
	}
}

func TestFederationRoutesUseLocalEscapedPathsAndCompactBodies(t *testing.T) {
	tests := []struct {
		method   string
		path     string
		body     string
		wantPath string
		wantBody string
	}{
		{http.MethodGet, "/fed/summary?id=member%2Fone", "", "/v1/fed/cluster/member%2Fone/v1/system/summary", ""},
		{http.MethodPost, "/fed/promote", ` {"name":"primary","master_rest_info":null,"use_proxy":null,"deploy_repo_scan_data":false} `, "/v1/fed/promote", `{"name":"primary","deploy_repo_scan_data":false}`},
		{http.MethodPost, "/fed/demote", "", "/v1/fed/demote", ""},
		{http.MethodGet, "/fed/join_token", "", "/v1/fed/join_token", ""},
		{http.MethodPost, "/fed/join", `{"name":"member","server":"controller","port":10443,"join_token":"synthetic-token","joint_rest_info":null,"use_proxy":null}`, "/v1/fed/join", `{"name":"member","server":"controller","port":10443,"join_token":"synthetic-token"}`},
		{http.MethodPost, "/fed/leave", `{"force":true}`, "/v1/fed/leave", `{"force":true}`},
		{http.MethodPatch, "/fed/config", `{"poll_interval":10,"name":null,"use_proxy":null,"rest_info":null,"deploy_repo_scan_data":true}`, "/v1/fed/config", `{"poll_interval":10,"deploy_repo_scan_data":true}`},
		{http.MethodDelete, "/fed?id=member%2Fone", "", "/v1/fed/cluster/member%2Fone", ""},
		{http.MethodPost, "/fed/deploy", `{"ids":["rule-1"]}`, "/v1/fed/deploy", `{"ids":["rule-1"]}`},
	}

	for _, test := range tests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() != test.wantPath {
					t.Errorf("escaped path = %q, want %q", r.URL.EscapedPath(), test.wantPath)
				}
				body, _ := io.ReadAll(r.Body)
				if string(body) != test.wantBody {
					t.Errorf("body = %q, want %q", body, test.wantBody)
				}
				w.Header().Set("X-Fixture", "forwarded")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"status":"ok"}`))
			}))
			defer controllerServer.Close()
			engine, store := testEngine(t, controllerServer)
			store.SetCluster("token", "must-not-be-used")
			response := serve(engine, test.method, test.path, test.body, "token")
			if response.Code != http.StatusAccepted || response.Header().Get("X-Fixture") != "forwarded" || response.Body.String() != `{"status":"ok"}` {
				t.Fatalf("response = %d %v %s", response.Code, response.Header(), response.Body.String())
			}
		})
	}
}

func TestFederationValidation(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("Controller must not be called")
	}))
	defer controllerServer.Close()
	engine, _ := testEngine(t, controllerServer)
	for _, test := range []struct{ method, path, body string }{
		{http.MethodGet, "/fed/summary", ""},
		{http.MethodDelete, "/fed", ""},
		{http.MethodPost, "/fed/join", "not-json"},
	} {
		response := serve(engine, test.method, test.path, test.body, "token")
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s %s = %d", test.method, test.path, response.Code)
		}
	}
}

func testEngine(t *testing.T, controllerServer *httptest.Server) (*gin.Engine, *session.Store) {
	t.Helper()
	baseURL, err := url.Parse(controllerServer.URL + "/configured-prefix")
	if err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(10)
	handler := NewHandler(
		controller.NewWithHTTPClient(baseURL, controllerServer.Client()),
		controller.NewTargetResolver(baseURL, store), store,
	)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/fed/member", handler.GetMember)
	engine.GET("/fed/summary", handler.GetSummary)
	engine.POST("/fed/promote", handler.Promote)
	engine.POST("/fed/demote", handler.Demote)
	engine.GET("/fed/join_token", handler.GetJoinToken)
	engine.POST("/fed/join", handler.Join)
	engine.POST("/fed/leave", handler.Leave)
	engine.PATCH("/fed/config", handler.Config)
	engine.DELETE("/fed", handler.Delete)
	engine.POST("/fed/deploy", handler.Deploy)
	return engine, store
}

func serve(engine *gin.Engine, method, path, body, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Token", token)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}
