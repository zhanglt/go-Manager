package policy

import (
	"encoding/json"
	"io"
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

func TestResponsePolicyExport(t *testing.T) {
	for _, test := range []struct{ route, scope string }{{"/responsePolicy/export", "local"}, {"/responsePolicy/export-fed", "fed"}} {
		t.Run(test.scope, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				want := `{"ids":[1,7],"remote_export_options":{"remote_repository_nickname":"repo-one"}}`
				if r.URL.EscapedPath() != "/v1/fed/cluster/member-one/v1/file/response/rule" || r.URL.Query().Get("scope") != test.scope || string(body) != want {
					t.Errorf("target=%s?%s body=%s", r.URL.EscapedPath(), r.URL.RawQuery, body)
				}
				_, _ = w.Write([]byte("exported"))
			}))
			defer controllerServer.Close()
			engine := testEngine(t, controllerServer)
			input := `{"ids":[1,7],"remote_export_options":{"remote_repository_nickname":"repo-one","file_path":null,"comment":null}}`
			request := httptest.NewRequest(http.MethodPost, test.route, strings.NewReader(input))
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "exported" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestResponsePolicyImport(t *testing.T) {
	for _, test := range []struct{ route, scope, transaction, input, wantBody string }{
		{"/responsePolicy/import", "local", "tx-response", "ignored", ""},
		{"/responsePolicy/import-fed", "fed", "", "--b\nDisposition\nType\n\nfed-rule\n--b--\n", "fed-rule"},
	} {
		t.Run(test.scope, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.URL.EscapedPath() != "/v1/fed/cluster/member-one/v1/file/response/rule/config" || r.URL.Query().Get("scope") != test.scope || r.Header.Get("X-Transaction-Id") != test.transaction || string(body) != test.wantBody {
					t.Errorf("target=%s?%s headers=%v body=%q", r.URL.EscapedPath(), r.URL.RawQuery, r.Header, body)
				}
				_, _ = w.Write([]byte("imported"))
			}))
			defer controllerServer.Close()
			engine := testEngine(t, controllerServer)
			request := httptest.NewRequest(http.MethodPost, test.route, strings.NewReader(test.input))
			request.Header.Set("Token", "token")
			if test.transaction != "" {
				request.Header.Set("X-Transaction-Id", test.transaction)
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "imported" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestResponsePolicyExportValidation(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("Controller must not be called") }))
	defer controllerServer.Close()
	engine := testEngine(t, controllerServer)
	for _, input := range []string{`not-json`, `{}`, `{"ids":[],"remote_export_options":{}}`} {
		request := httptest.NewRequest(http.MethodPost, "/responsePolicy/export", strings.NewReader(input))
		request.Header.Set("Token", "token")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("input=%q status=%d", input, response.Code)
		}
	}
}

func TestResponsePolicyReadAndDelete(t *testing.T) {
	tests := []struct {
		name, method, route, wantPath, wantQuery string
	}{
		{"single rule", http.MethodGet, "/responseRule?id=7", "/v1/fed/cluster/member-one/v1/response/rule/7", ""},
		{"local list", http.MethodGet, "/responsePolicy?scope=local", "/v1/fed/cluster/member-one/v1/response/rule", "scope=local"},
		{"federal list", http.MethodGet, "/responsePolicy?scope=fed", "/v1/response/rule", "scope=fed"},
		{"local delete", http.MethodDelete, "/responsePolicy?id=7", "/v1/fed/cluster/member-one/v1/response/rule/7", ""},
		{"federal delete", http.MethodDelete, "/responsePolicy?id=7&scope=fed", "/v1/response/rule/7", "scope=fed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != test.method || r.URL.EscapedPath() != test.wantPath || r.URL.Query().Encode() != test.wantQuery {
					t.Errorf("request=%s %s?%s", r.Method, r.URL.EscapedPath(), r.URL.RawQuery)
				}
				_, _ = w.Write([]byte("ok"))
			}))
			defer controllerServer.Close()
			request := httptest.NewRequest(test.method, test.route, nil)
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			testEngine(t, controllerServer).ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "ok" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestResponsePolicyMutations(t *testing.T) {
	create := `{"insert":{"after":null,"rules":[{"id":null,"event":"event","comment":null,"group":null,"conditions":null,"actions":["webhook"],"webhooks":null,"disable":false,"cfg_type":"federal"}]}}`
	update := `{"config":{"id":7,"event":"event","comment":null,"group":null,"conditions":null,"actions":["webhook"],"webhooks":null,"disable":false,"cfg_type":"user_created"}}`
	tests := []struct{ name, method, route, input, wantPath, wantBody string }{
		{"create federal", http.MethodPost, "/responsePolicy", create, "/v1/response/rule", `{"insert":{"rules":[{"actions":["webhook"],"cfg_type":"federal","disable":false,"event":"event"}]}}`},
		{"update local", http.MethodPatch, "/responsePolicy", update, "/v1/fed/cluster/member-one/v1/response/rule/7", `{"config":{"actions":["webhook"],"cfg_type":"user_created","disable":false,"event":"event","id":7}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.Method != http.MethodPatch || r.URL.EscapedPath() != test.wantPath || string(body) != test.wantBody {
					t.Errorf("request=%s %s body=%s", r.Method, r.URL.EscapedPath(), body)
				}
				_, _ = w.Write([]byte("ok"))
			}))
			defer controllerServer.Close()
			request := httptest.NewRequest(test.method, test.route, strings.NewReader(test.input))
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			testEngine(t, controllerServer).ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "ok" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestPolicyRoutes(t *testing.T) {
	tests := []struct{ name, method, route, input, wantMethod, wantPath, wantQuery, wantBody string }{
		{"deploy", http.MethodPost, "/fed-deploy", `{"force":true,"ids":[1]}`, http.MethodPost, "/v1/fed/deploy", "", `{"force":true,"ids":[1]}`},
		{"condition federal", http.MethodGet, "/conditionOption?scope=fed", "", http.MethodGet, "/v1/response/options", "scope=fed", ""},
		{"unquarantine", http.MethodPost, "/unquarantine", `{"request":{"unquarantine":{"response_rule":7}}}`, http.MethodPost, "/v1/fed/cluster/member-one/v1/system/request", "", `{"request":{"unquarantine":{"response_rule":7}}}`},
		{"applications", http.MethodGet, "/policy/application", "", http.MethodGet, "/v1/fed/cluster/member-one/v1/list/application", "", ""},
		{"get rule", http.MethodGet, "/policy/rule?id=7", "", http.MethodGet, "/v1/fed/cluster/member-one/v1/policy/rule/7", "", ""},
		{"create rule", http.MethodPost, "/policy/rule", `{"id":null,"from":"a","to":"b","action":"allow","learned":false,"disable":false}`, http.MethodPatch, "/v1/fed/cluster/member-one/v1/policy/rule", "", `{"insert":{"after":0,"rules":[{"action":"allow","disable":false,"from":"a","learned":false,"to":"b"}]}}`},
		{"update rule", http.MethodPatch, "/policy/rule", `{"id":7,"comment":null,"action":"deny"}`, http.MethodPatch, "/v1/fed/cluster/member-one/v1/policy/rule/7", "", `{"config":{"action":"deny","id":7},"replicate":true}`},
		{"delete rule", http.MethodDelete, "/policy?id=7", "", http.MethodDelete, "/v1/fed/cluster/member-one/v1/policy/rule/7", "", ""},
		{"update federal", http.MethodPatch, "/policy?scope=fed", `{"rules":null,"delete":[7]}`, http.MethodPatch, "/v1/policy/rule", "scope=fed", `{"delete":[7]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.Method != test.wantMethod || r.URL.EscapedPath() != test.wantPath || r.URL.Query().Encode() != test.wantQuery || string(body) != test.wantBody {
					t.Errorf("request=%s %s?%s body=%s", r.Method, r.URL.EscapedPath(), r.URL.RawQuery, body)
				}
				_, _ = w.Write([]byte("ok"))
			}))
			defer controllerServer.Close()
			request := httptest.NewRequest(test.method, test.route, strings.NewReader(test.input))
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			testEngine(t, controllerServer).ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "ok" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestGetPolicyPaginationAndLocalScope(t *testing.T) {
	requests := 0
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			if r.URL.EscapedPath() != "/v1/fed/cluster/member-one/v1/policy/rule" {
				t.Errorf("cluster path=%s", r.URL.EscapedPath())
			}
		} else if r.URL.EscapedPath() != "/v1/policy/rule" || r.URL.Query().Get("scope") != "fed" {
			t.Errorf("local target=%s?%s", r.URL.EscapedPath(), r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"rules":[{"id":1},{"id":2},{"id":3}]}`))
	}))
	defer controllerServer.Close()
	engine := testEngine(t, controllerServer)
	for _, test := range []struct{ path, want string }{
		{"/policy?start=0&limit=2", `[{"id":1},{"id":2}]`},
		{"/policy?start=2&limit=2", `[{"id":3}]`},
		{"/policy?scope=fed", `{"rules":[{"id":1},{"id":2},{"id":3}]}`},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.Header.Set("Token", "token")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != test.want {
			t.Fatalf("%s response=%d %s", test.path, response.Code, response.Body.String())
		}
	}
	if requests != 2 {
		t.Fatalf("controller requests=%d", requests)
	}
}

func TestGetPolicyGraph(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/v1/fed/cluster/member-one/v1/group":
			_, _ = w.Write([]byte(`{"groups":[{"name":"nv.external","reserved":true,"learned":false},{"name":"learned","reserved":false,"learned":true},{"name":"custom","reserved":false,"learned":false}]}`))
		case "/v1/fed/cluster/member-one/v1/policy/rule":
			_, _ = w.Write([]byte(`{"rules":[{"id":7,"from":"custom","to":"nv.external","applications":["TLS"],"action":"Deny"},{"id":null,"from":"learned","to":"custom","applications":null,"action":"allow"}]}`))
		default:
			t.Errorf("path=%s", r.URL.EscapedPath())
		}
	}))
	defer controllerServer.Close()
	request := httptest.NewRequest(http.MethodGet, "/policy/graph", nil)
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	testEngine(t, controllerServer).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
	var graph policyGraph
	if err := json.Unmarshal(response.Body.Bytes(), &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 3 || graph.Nodes[0].Group != "external" || graph.Nodes[1].Group != "learned" || graph.Nodes[2].Group != "custom" {
		t.Fatalf("nodes=%+v", graph.Nodes)
	}
	if len(graph.Edges) != 2 || graph.Edges[0].ID == nil || *graph.Edges[0].ID != "Some(7)" || graph.Edges[0].Label == nil || *graph.Edges[0].Label != "X" || graph.Edges[1].ID == nil || *graph.Edges[1].ID != "None" {
		t.Fatalf("edges=%+v", graph.Edges)
	}
}

func TestGetScanWorkloadSummaryAndDetail(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/v1/fed/cluster/member-one/v1/scan/workload/workload%2Fone" {
			if r.URL.Query().Get("show") != "accepted" {
				t.Errorf("detail query=%s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"report":"detail"}`))
			return
		}
		if r.URL.EscapedPath() != "/v1/fed/cluster/member-one/v1/scan/workload" || r.URL.Query().Encode() != "limit=0&start=0&view=pod" {
			t.Errorf("summary target=%s?%s", r.URL.EscapedPath(), r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"workloads":[{"id":"parent","name":"parent","base_os":"linux","display_name":"Parent","domain":"prod","critical":1,"high":2,"medium":3,"host":"host","image":"image","platform_role":"","policy_mode":null,"result":"finished","service":"svc","service_group":"group","state":"running","status":"scanned","scanner_version":"v1","children":[{"id":"child","name":"child","base_os":"linux","display_name":"Child","domain":"prod","critical":4,"high":5,"medium":6,"host":"host","image":"image","platform_role":"","policy_mode":null,"result":"finished","service":"svc","service_group":"group","state":"running","status":"scanned","scanner_version":"v1","scanned_timestamp":2,"scanned_at":"time"}],"scanned_timestamp":1,"scanned_at":"time"}],"status":{"scanned":1,"scheduled":0,"scanning":0,"failed":0,"cvedb_version":"v1","cvedb_create_time":"time"}}`))
	}))
	defer controllerServer.Close()
	engine := testEngine(t, controllerServer)
	request := httptest.NewRequest(http.MethodGet, "/scan/workload", nil)
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"hidden_vulnerability":{"hidden_critical":5,"hidden_high":7,"hidden_medium":9}`) {
		t.Fatalf("summary=%d %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/scan/workload?id=workload%2Fone&show=accepted", nil)
	request.Header.Set("Token", "token")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != `{"report":"detail"}` {
		t.Fatalf("detail=%d %s", response.Code, response.Body.String())
	}
}

func testEngine(t *testing.T, controllerServer *httptest.Server) *gin.Engine {
	t.Helper()
	baseURL, _ := url.Parse(controllerServer.URL + "/v1")
	store := session.NewStore(10)
	store.Put("token", "cookie")
	store.SetCluster("token", "member-one")
	handler := NewHandler(controller.NewWithHTTPClient(baseURL, controllerServer.Client()), controller.NewTargetResolver(baseURL, store), store, NewCache(10, 1<<20, time.Minute))
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/responsePolicy/export", handler.Export("local"))
	engine.POST("/responsePolicy/export-fed", handler.Export("fed"))
	engine.POST("/responsePolicy/import", handler.Import("local"))
	engine.POST("/responsePolicy/import-fed", handler.Import("fed"))
	engine.GET("/responseRule", handler.GetResponseRule)
	engine.GET("/responsePolicy", handler.GetResponsePolicy)
	engine.POST("/responsePolicy", handler.CreateResponsePolicy)
	engine.PATCH("/responsePolicy", handler.UpdateResponsePolicy)
	engine.DELETE("/responsePolicy", handler.DeleteResponsePolicy)
	engine.POST("/fed-deploy", handler.DeployFederal)
	engine.GET("/conditionOption", handler.GetConditionOptions)
	engine.POST("/unquarantine", handler.Unquarantine)
	engine.PATCH("/policy", handler.UpdatePolicy)
	engine.DELETE("/policy", handler.DeletePolicy)
	engine.GET("/policy", handler.GetPolicy)
	engine.GET("/policy/application", handler.GetPolicyApplications)
	engine.GET("/policy/rule", handler.GetPolicyRule)
	engine.POST("/policy/rule", handler.CreatePolicyRule)
	engine.PATCH("/policy/rule", handler.UpdatePolicyRule)
	engine.GET("/policy/graph", handler.GetPolicyGraph)
	engine.GET("/scan/workload", handler.GetScanWorkload)
	return engine
}
