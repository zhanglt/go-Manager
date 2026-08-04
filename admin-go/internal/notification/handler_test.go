package notification

import (
	"encoding/json"
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

func newNotificationTestHandler(t *testing.T, controllerHandler http.HandlerFunc) *Handler {
	t.Helper()
	server := httptest.NewServer(controllerHandler)
	t.Cleanup(server.Close)
	baseURL, _ := url.Parse(server.URL + "/v1")
	store := session.NewStore(10)
	store.Put("token", "cookie")
	store.SetCluster("token", "member-one")
	return NewHandler(controller.NewWithHTTPClient(baseURL, server.Client()), controller.NewTargetResolver(baseURL, store), store)
}

func TestPatchIPGeo(t *testing.T) {
	from, _ := decimalIP("16909056")
	to, _ := decimalIP("16909311")
	database := newIPGeoDatabase()
	database.once.Do(func() {})
	database.countries = []ipGeoCountry{{"-", "-"}, {"ZZ", "Fixture Country"}}
	database.v4 = []ipGeoRange{{from, to, 1}}
	handler := &Handler{ipGeo: database}
	engine := gin.New()
	engine.PATCH("/ip-geo", handler.PatchIPGeo)
	request := httptest.NewRequest(http.MethodPatch, "/ip-geo", strings.NewReader(`["1.2.3.4","invalid"]`))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response=%d type=%q body=%s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	want := `{"ip_map":{"1.2.3.4":{"from":16909056,"to":16909311,"country_code":"ZZ","country_name":"Fixture Country"},"invalid":{"from":0,"to":0,"country_code":"-","country_name":"-"}}}`
	if response.Body.String() != want {
		t.Fatalf("body=%s", response.Body.String())
	}
}

func TestLogRoutesUseCurrentCluster(t *testing.T) {
	for _, resource := range []string{"event", "incident", "audit"} {
		t.Run(resource, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/fed/cluster/member-one/v1/log/"+resource {
					t.Errorf("request=%s %s", r.Method, r.URL.EscapedPath())
				}
				_, _ = w.Write([]byte("ok"))
			}))
			defer server.Close()
			baseURL, _ := url.Parse(server.URL + "/v1")
			store := session.NewStore(10)
			store.Put("token", "cookie")
			store.SetCluster("token", "member-one")
			handler := NewHandler(controller.NewWithHTTPClient(baseURL, server.Client()), controller.NewTargetResolver(baseURL, store), store)
			gin.SetMode(gin.TestMode)
			engine := gin.New()
			switch resource {
			case "event":
				engine.GET("/event", handler.GetEvent)
			case "incident":
				engine.GET("/incident", handler.GetIncident)
			case "audit":
				engine.GET("/audit", handler.GetAudit)
			}
			request := httptest.NewRequest(http.MethodGet, "/"+resource, nil)
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "ok" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestViolationTopAndNetworkSession(t *testing.T) {
	tests := []struct{ route, path, query string }{
		{"/violation/top?category=client", "/v1/fed/cluster/member-one/v1/log/violation/workload", "limit=5&s_client=desc&start=0"},
		{"/violation/top?category=server", "/v1/fed/cluster/member-one/v1/log/violation/workload", "limit=5&s_server=desc&start=0"},
		{"/network/session?id=endpoint%2Fone", "/v1/fed/cluster/member-one/v1/session", "f_workload=endpoint%2Fone&limit=256"},
	}
	for _, test := range tests {
		t.Run(test.route, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() != test.path || r.URL.Query().Encode() != test.query {
					t.Errorf("target=%s?%s", r.URL.EscapedPath(), r.URL.RawQuery)
				}
				_, _ = w.Write([]byte("ok"))
			}))
			defer server.Close()
			baseURL, _ := url.Parse(server.URL + "/v1")
			store := session.NewStore(10)
			store.SetCluster("token", "member-one")
			handler := NewHandler(controller.NewWithHTTPClient(baseURL, server.Client()), controller.NewTargetResolver(baseURL, store), store)
			gin.SetMode(gin.TestMode)
			engine := gin.New()
			engine.GET("/violation/top", handler.GetViolationTop)
			engine.GET("/network/session", handler.GetNetworkSession)
			request := httptest.NewRequest(http.MethodGet, test.route, nil)
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("response=%d", response.Code)
			}
		})
	}
}

func TestNotificationRequiredQueries(t *testing.T) {
	engine := gin.New()
	handler := &Handler{}
	engine.GET("/violation/top", handler.GetViolationTop)
	engine.GET("/network/session", handler.GetNetworkSession)
	for _, path := range []string{"/violation/top", "/network/session"} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s=%d", path, response.Code)
		}
	}
}

func TestConversationRoutes(t *testing.T) {
	tests := []struct{ method, route, path string }{
		{http.MethodDelete, "/network/conversation?from=a%2Fb&to=c%2Fd", "/v1/fed/cluster/member-one/v1/conversation/a%2Fb/c%2Fd"},
		{http.MethodGet, "/network/history?from=a%2Fb&to=c%2Fd", "/v1/fed/cluster/member-one/v1/conversation/a%2Fb/c%2Fd"},
		{http.MethodDelete, "/network/endpoint?id=a%2Fb", "/v1/fed/cluster/member-one/v1/conversation_endpoint/a%2Fb"},
	}
	for _, test := range tests {
		t.Run(test.route, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != test.method || r.URL.EscapedPath() != test.path {
					t.Errorf("%s %s", r.Method, r.URL.EscapedPath())
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			base, _ := url.Parse(server.URL + "/v1")
			store := session.NewStore(2)
			store.SetCluster("token", "member-one")
			h := NewHandler(controller.NewWithHTTPClient(base, server.Client()), controller.NewTargetResolver(base, store), store)
			e := gin.New()
			e.DELETE("/network/conversation", h.DeleteConversation)
			e.GET("/network/history", h.GetConversationHistory)
			e.DELETE("/network/endpoint", h.DeleteConversationEndpoint)
			req := httptest.NewRequest(test.method, test.route, nil)
			req.Header.Set("Token", "token")
			out := httptest.NewRecorder()
			e.ServeHTTP(out, req)
			if out.Code != http.StatusOK {
				t.Fatalf("%d", out.Code)
			}
		})
	}
}

func TestNetworkGraphTransformsControllerDataAndKeepsUserState(t *testing.T) {
	controllerHandler := func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.EscapedPath() {
		case "GET /v1/fed/cluster/member-one/v1/conversation":
			_, _ = w.Write([]byte(`{"endpoints":[{"id":"workload-a","name":"backend","display_name":"","state":"protect","kind":"container","platform_role":"controller","service_group":"ns.team.app","domain":"team","share_ns_with":null,"policy_mode":"enforce","scan_summary":{"status":"finished","critical":1,"high":2,"medium":3},"cap_quarantine":true,"cap_change_mode":true,"cap_sniff":false,"service_mesh":false,"service_mesh_sidecar":null,"children":null},{"id":"workload-b","name":"db","display_name":"Database","state":"monitor","kind":"container","platform_role":null,"service_group":"ns.team.app","domain":"team","share_ns_with":null,"policy_mode":"monitor","scan_summary":null,"cap_quarantine":false,"cap_change_mode":false,"cap_sniff":false,"service_mesh":null,"service_mesh_sidecar":null,"children":null},{"id":"hidden","name":"hidden","display_name":"","state":"exit","kind":"container","platform_role":null,"service_group":null,"domain":"","share_ns_with":null,"policy_mode":null,"scan_summary":null,"cap_quarantine":false,"cap_change_mode":false,"cap_sniff":false}],"conversations":[{"from":"workload-a","to":"workload-b","bytes":9,"sessions":1,"severity":"","policy_action":"allow","event_type":["dlp"],"protocols":["tcp"],"applications":["https"],"ports":["443"],"sidecar_proxy":true}]}`))
		case "PATCH /v1/fed/cluster/member-one/v1/auth":
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected controller request %s %s", r.Method, r.URL.EscapedPath())
			w.WriteHeader(http.StatusNotFound)
		}
	}
	handler := newNotificationTestHandler(t, controllerHandler)
	engine := gin.New()
	engine.GET("/network/graph", handler.GetNetworkGraph)
	engine.POST("/network/graph", handler.CreateNetworkGraph)
	engine.GET("/network/graph/layout", handler.GetNetworkGraphLayout)
	engine.POST("/network/graph/blacklist", handler.CreateNetworkGraphBlacklist)
	engine.GET("/network/graph/blacklist", handler.GetNetworkGraphBlacklist)

	graph := httptest.NewRecorder()
	graphRequest := httptest.NewRequest(http.MethodGet, "/network/graph?user=alice", nil)
	graphRequest.Header.Set("Token", "token")
	engine.ServeHTTP(graph, graphRequest)
	if graph.Code != http.StatusOK {
		t.Fatalf("graph status=%d body=%s", graph.Code, graph.Body.String())
	}
	var output networkGraph
	if err := json.Unmarshal(graph.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Nodes) != 2 || output.Nodes[0].Label != "backend" || output.Nodes[0].PlatformRole != "System" {
		t.Fatalf("nodes=%+v", output.Nodes)
	}
	if len(output.Edges) != 1 || output.Edges[0].Status != "intraGroup" || output.Edges[0].Label == nil || *output.Edges[0].Label != "$ https,443" {
		t.Fatalf("edges=%+v", output.Edges)
	}

	for _, test := range []struct{ method, path, body string }{
		{http.MethodPost, "/network/graph", `{"user":"alice","nodePositions":{"workload-a":{"x":1.5,"y":2.5}}}`},
		{http.MethodPost, "/network/graph/blacklist", `{"user":"alice","blacklist":{"domains":[{"name":"team"}],"groups":[],"endpoints":[]}}`},
		{http.MethodGet, "/network/graph/layout?user=alice", ""},
		{http.MethodGet, "/network/graph/blacklist?user=alice", ""},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		request.Header.Set("Token", "token")
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s: status=%d body=%s", test.method, test.path, response.Code, response.Body.String())
		}
		if strings.Contains(test.path, "layout") && !strings.Contains(response.Body.String(), `"nodePositions":{"workload-a":{"x":1.5,"y":2.5}}`) {
			t.Fatalf("layout=%s", response.Body.String())
		}
		if strings.Contains(test.path, "blacklist?") && response.Body.String() != `{"domains":[{"name":"team"}],"groups":[],"endpoints":[]}` {
			t.Fatalf("blacklist=%s", response.Body.String())
		}
	}
}

func TestNotificationMutations(t *testing.T) {
	tests := []struct {
		name, route, wantPath, input, wantBody string
	}{
		{"endpoint", "/network/endpoint", "/v1/fed/cluster/member-one/v1/conversation_endpoint/endpoint%2Fone", `{"config":{"id":"endpoint/one","display_name":null}}`, `{"config":{"id":"endpoint/one"}}`},
		{"accept", "/notification/accept", "/v1/internal/alert", `{"manager_alerts":["alert-one"],"controller_alerts":null,"user_alerts":[]}`, `{"manager_alerts":["alert-one"],"user_alerts":[]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.Method != http.MethodPatch && test.name == "endpoint" || r.Method != http.MethodPost && test.name == "accept" {
					t.Errorf("method=%s", r.Method)
				}
				if r.URL.EscapedPath() != test.wantPath || string(body) != test.wantBody {
					t.Errorf("path=%s body=%s", r.URL.EscapedPath(), body)
				}
				_, _ = w.Write([]byte("ok"))
			}))
			defer server.Close()
			base, _ := url.Parse(server.URL + "/v1")
			store := session.NewStore(2)
			store.SetCluster("token", "member-one")
			handler := NewHandler(controller.NewWithHTTPClient(base, server.Client()), controller.NewTargetResolver(base, store), store)
			engine := gin.New()
			engine.PATCH("/network/endpoint", handler.UpdateConversationEndpoint)
			engine.POST("/notification/accept", handler.AcceptNotification)
			method := http.MethodPatch
			if test.name == "accept" {
				method = http.MethodPost
			}
			request := httptest.NewRequest(method, test.route, strings.NewReader(test.input))
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "ok" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestGetThreatConvertsDirectionAndFallbackNames(t *testing.T) {
	handler := newNotificationTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v1/fed/cluster/member-one/v1/log/threat" || r.Header.Get("X-Auth-Token") != "token" || r.Header.Get("X-R-Sess") != "cookie" {
			t.Errorf("request=%s token=%q session=%q", r.URL.EscapedPath(), r.Header.Get("X-Auth-Token"), r.Header.Get("X-R-Sess"))
		}
		_, _ = w.Write([]byte(`{"threats":[{"id":"one","name":"attack","reported_timestamp":1,"reported_at":"2026-08-02T10:00:00+08:00","count":2,"client_workload_id":"client-id","client_workload_name":"","client_workload_domain":"client-domain","server_workload_id":"server-id","server_workload_name":"","server_workload_domain":null,"severity":"high","action":"allow","client_port":1000,"server_port":443,"server_conn_port":8443,"client_ip":"10.0.0.1","server_ip":"10.0.0.2","application":"TLS","target":"client","cap_len":64,"message":"sample"}]}`))
	})
	engine := gin.New()
	engine.GET("/threat", handler.GetThreat)
	request := httptest.NewRequest(http.MethodGet, "/threat", nil)
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Threats []convertedThreat `json:"threats"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result.Threats) != 1 {
		t.Fatalf("decode=%v result=%+v", err, result)
	}
	item := result.Threats[0]
	if item.SourceWorkloadID != "server-id" || item.SourceWorkloadName != "10.0.0.2" || item.DestinationWorkloadID != "client-id" || item.DestinationWorkloadName != "10.0.0.1" {
		t.Fatalf("direction=%+v", item)
	}
	if item.SourceConnectionPort == nil || *item.SourceConnectionPort != 8443 || item.DestinationConnectionPort != nil || item.Domain.Source != nil || item.Domain.Destination == nil || *item.Domain.Destination != "client-domain" {
		t.Fatalf("connection/domain=%+v", item)
	}
}

func TestGetThreatByIDIsTransparent(t *testing.T) {
	handler := newNotificationTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v1/fed/cluster/member-one/v1/log/threat/id%2Fone" {
			t.Errorf("path=%s", r.URL.EscapedPath())
		}
		w.Header().Set("X-Fixture", "preserved")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("raw"))
	})
	engine := gin.New()
	engine.GET("/threat", handler.GetThreat)
	request := httptest.NewRequest(http.MethodGet, "/threat?id=id%2Fone", nil)
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Body.String() != "raw" || response.Header().Get("X-Fixture") != "preserved" {
		t.Fatalf("response=%d header=%q body=%s", response.Code, response.Header().Get("X-Fixture"), response.Body.String())
	}
}

func TestGetThreatTopDistinctSortAndPadding(t *testing.T) {
	handler := newNotificationTestHandler(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"threats":[{"name":"low","severity":"low","application":"DNS"},{"name":"critical","severity":"Critical","application":"HTTP"},{"name":"low","severity":"low","application":"DNS"},{"name":"unknown","severity":"unknown","application":"TCP"}]}`))
	})
	engine := gin.New()
	engine.GET("/threat/top", handler.GetThreatTop)
	request := httptest.NewRequest(http.MethodGet, "/threat/top", nil)
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	var result []threatBriefDTO
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result) != 5 {
		t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
	}
	if result[0].Name != "critical" || result[0].SeverityID != 5 || result[1].Name != "low" || result[1].SeverityID != 2 || result[2].Name != "unknown" || result[2].SeverityID != 1 || result[4].Name != " " {
		t.Fatalf("result=%+v", result)
	}
}

func TestTrackThreatAndViolationUseStrictTimeWindow(t *testing.T) {
	controllerHandler := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/v1/fed/cluster/member-one/v1/log/threat":
			_, _ = w.Write([]byte(`{"threats":[{"name":"inside","reported_at":"2026-08-02T11:59:59+08:00","workload_id":"server","workload_name":"server","count":1,"severity":"high","action":"allow","src_ip":"1","dst_ip":"2","src_port":1,"dst_port":2,"application":"TCP","sess_ingress":true},{"name":"boundary","reported_at":"2026-08-02T12:00:00+08:00","workload_id":"server","workload_name":"server","count":1,"severity":"low","action":"allow","src_ip":"1","dst_ip":"2","src_port":1,"dst_port":2,"application":"TCP","sess_ingress":true},{"name":"egress-boundary","reported_at":"2026-08-02T08:00:00+08:00","workload_id":"client","workload_name":"client","count":1,"severity":"low","action":"allow","src_ip":"1","dst_ip":"2","src_port":1,"dst_port":2,"application":"TCP","sess_ingress":false}]}`))
		case "/v1/fed/cluster/member-one/v1/log/violation":
			_, _ = w.Write([]byte(`{"violations":[{"client_id":"client","client_name":"display-client","server_id":"server","server_name":"display-server","server_port":443,"applications":["TLS"],"reported_at":"2026-08-02T08:00:01+08:00","policy_id":1,"client_ip":"1","server_ip":"2"},{"client_id":"client","client_name":"display-client","server_id":"server","server_name":"display-server","server_port":443,"applications":["TLS"],"reported_at":"2026-08-02T08:00:00+08:00","policy_id":2,"client_ip":"1","server_ip":"2"}]}`))
		default:
			t.Errorf("path=%s", r.URL.EscapedPath())
		}
	}
	for _, test := range []struct {
		path string
		bind func(*gin.Engine, *Handler)
		want string
	}{
		{"/threat/track", func(e *gin.Engine, h *Handler) { e.POST("/threat/track", h.TrackThreat) }, "inside"},
		{"/violation/track", func(e *gin.Engine, h *Handler) { e.POST("/violation/track", h.TrackViolation) }, "display-client"},
	} {
		t.Run(test.path, func(t *testing.T) {
			handler := newNotificationTestHandler(t, controllerHandler)
			engine := gin.New()
			test.bind(engine, handler)
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(`{"client_name":"client","server_name":"server","reported_at":"2026-08-02T10:00:00+08:00"}`))
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.want) {
				t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
			}
			var result []json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result) != 1 {
				t.Fatalf("result count=%d err=%v", len(result), err)
			}
		})
	}
}

func TestGetAudit2PaginationValidatesEveryPage(t *testing.T) {
	auditRequests, validationRequests := 0, 0
	handler := newNotificationTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.EscapedPath() == "/v1/fed/cluster/member-one/v1/log/audit":
			auditRequests++
			_, _ = w.Write([]byte(`{"audits":[{"id":1},{"id":2}]}`))
		case r.Method == http.MethodPatch && r.URL.EscapedPath() == "/v1/fed/cluster/member-one/v1/auth":
			validationRequests++
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			t.Errorf("request=%s %s", r.Method, r.URL.EscapedPath())
		}
	})
	engine := gin.New()
	engine.GET("/audit2", handler.GetAudit2)
	for _, test := range []struct{ path, body string }{
		{"/audit2?start=0&limit=1", `[{"id":1}]`},
		{"/audit2?start=1&limit=1", `[{"id":2}]`},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.Header.Set("Token", "token")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/plain; charset=UTF-8" || response.Body.String() != test.body {
			t.Fatalf("%s response=%d type=%q body=%s", test.path, response.Code, response.Header().Get("Content-Type"), response.Body.String())
		}
	}
	if auditRequests != 1 || validationRequests != 2 {
		t.Fatalf("audit requests=%d validations=%d", auditRequests, validationRequests)
	}
}

func TestGetSecurityEvents2ReturnsRawNotificationPayloads(t *testing.T) {
	handler := newNotificationTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		resource := strings.TrimPrefix(r.URL.EscapedPath(), "/v1/fed/cluster/member-one/v1/log/")
		switch resource {
		case "threat":
			_, _ = w.Write([]byte(`{"threats":[{"id":"one"}]}`))
		case "violation":
			_, _ = w.Write([]byte(`{"violations":[{"id":"two"}]}`))
		case "incident":
			_, _ = w.Write([]byte(`{"incidents":[{"id":"three"}]}`))
		default:
			t.Errorf("resource=%s", resource)
		}
	})
	engine := gin.New()
	engine.GET("/security-events2", handler.GetSecurityEvents2)
	request := httptest.NewRequest(http.MethodGet, "/security-events2", nil)
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	var output []string
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &output) != nil || len(output) != 3 {
		t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
	}
	if output[0] != `{"threats":[{"id":"one"}]}` || output[1] != `{"violations":[{"id":"two"}]}` || output[2] != `{"incidents":[{"id":"three"}]}` {
		t.Fatalf("output=%q", output)
	}
}

func TestGetSecurityEventsMapsAndSortsControllerNotifications(t *testing.T) {
	handler := newNotificationTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		resource := strings.TrimPrefix(r.URL.EscapedPath(), "/v1/fed/cluster/member-one/v1/log/")
		switch resource {
		case "threat":
			_, _ = w.Write([]byte(`{"threats":[{"name":"threat-one","reported_timestamp":10,"reported_at":"2026-08-02T10:00:00+08:00","level":"high","client_workload_id":"client-id","client_workload_name":"client-name","client_workload_domain":"client-domain","server_workload_id":"server-id","server_workload_name":"server-name","server_workload_domain":"server-domain","client_port":1234,"server_port":443,"server_conn_port":8443,"client_ip":"10.0.0.1","server_ip":"10.0.0.2","target":"client","application":"TLS","id":"threat-id","host_name":"host-one","cluster_name":"cluster-one","count":2,"severity":"high","action":"allow","cap_len":64,"message":"threat detail"}]}`))
		case "violation":
			_, _ = w.Write([]byte(`{"violations":[{"policy_id":7,"reported_timestamp":30,"reported_at":"2026-08-02T10:30:00+08:00","level":"medium","client_id":"client-id","client_name":"client-name","client_domain":"client-domain","server_id":"server-id","server_name":"server-name","server_domain":"server-domain","client_ip":"10.0.0.1","server_ip":"10.0.0.2","server_port":443,"applications":["HTTPS"],"cluster_name":"cluster-one","client_image":"client:1","server_image":"server:1","ip_proto":6,"servers":null,"sessions":3,"policy_action":"deny"}]}`))
		case "incident":
			_, _ = w.Write([]byte(`{"incidents":[{"name":"incident-one","level":"critical","host_name":null,"workload_id":null,"workload_name":null,"workload_domain":null,"remote_workload_id":"remote-id","remote_workload_name":"remote-name","remote_workload_domain":"remote-domain","client_ip":null,"server_ip":"10.0.0.3","client_port":null,"server_port":8080,"server_conn_port":18080,"conn_ingress":true,"proc_path":"/bin/tool","reported_timestamp":20,"reported_at":"2026-08-02T10:20:00+08:00","cluster_name":"cluster-one","ether_type":2048,"ip_proto":6,"proc_name":"tool","proc_cmd":null,"proc_real_uid":1000,"proc_effective_uid":null,"proc_real_user":"fixture","proc_effective_user":null,"file_path":null,"file_name":null,"message":"incident detail"}]}`))
		default:
			t.Errorf("resource=%s", resource)
		}
	})
	engine := gin.New()
	engine.GET("/security-events", handler.GetSecurityEvents)
	request := httptest.NewRequest(http.MethodGet, "/security-events", nil)
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	var output securityEventDTO
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &output) != nil || len(output.SecurityEvents) != 3 {
		t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
	}
	if output.SecurityEvents[0].Name != "7" || output.SecurityEvents[1].Name != "incident-one" || output.SecurityEvents[2].Name != "threat-one" {
		t.Fatalf("events are not sorted: %+v", output.SecurityEvents)
	}
	threat := output.SecurityEvents[2]
	if threat.SecurityEventType != "Threat" || *threat.Source.WorkloadID != "server-id" || *threat.Source.ServerConnectionPort != 8443 || *threat.Destination.WorkloadID != "client-id" || threat.Destination.ServerConnectionPort != nil {
		t.Fatalf("threat=%+v", threat)
	}
	incident := output.SecurityEvents[1]
	if incident.SecurityEventType != "Incident" || *incident.HostName != "" || *incident.Source.WorkloadID != "remote-id" || len(incident.Applications) != 1 || incident.Applications[0] != "/bin/tool" {
		t.Fatalf("incident=%+v", incident)
	}
	var details incidentSecurityDetails
	if json.Unmarshal([]byte(incident.Details), &details) != nil || details.ProcessName == nil || *details.ProcessName != "tool" || details.ProcessCommand != nil {
		t.Fatalf("details=%s", incident.Details)
	}
}
