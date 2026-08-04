package dashboard

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

func TestDashboardQueriesUseCurrentCluster(t *testing.T) {
	tests := []struct {
		name, route, target, query, page string
	}{
		{"alerts", "/dashboard/alerts", "/v1/fed/cluster/member-one/v1/system/alerts", "", "globalAlerts"},
		{"scores-default", "/dashboard/scores", "/v1/fed/cluster/member-one/v1/system/score/metrics", "isGlobalUser=true", ""},
		{"scores-domain", "/dashboard/scores?isGlobalUser=false&domain=team%2Fone", "/v1/fed/cluster/member-one/v1/system/score/metrics", "f_domain=team%2Fone&isGlobalUser=false", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.EscapedPath() != test.target || r.URL.Query().Encode() != test.query || r.Header.Get("X-Nv-Page") != test.page {
					t.Errorf("request=%s %s?%s page=%q", r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("X-Nv-Page"))
				}
				_, _ = w.Write([]byte(`{"status":"ok"}`))
			}))
			defer server.Close()
			baseURL, _ := url.Parse(server.URL + "/v1")
			sessions := session.NewStore(10)
			sessions.SetCluster("token", "member-one")
			handler := NewHandler(controller.NewWithHTTPClient(baseURL, server.Client()), controller.NewTargetResolver(baseURL, sessions), sessions)
			engine := gin.New()
			engine.GET("/dashboard/alerts", handler.GetAlerts)
			engine.GET("/dashboard/scores", handler.GetScores)
			request := httptest.NewRequest(http.MethodGet, test.route, nil)
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != `{"status":"ok"}` {
				t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestPostScoresReencodesMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/v1/fed/cluster/member-one/v1/system/score/metrics" || r.Header.Get("Content-Type") != "text/plain; charset=UTF-8" {
			t.Errorf("request=%s %s type=%q", r.Method, r.URL.EscapedPath(), r.Header.Get("Content-Type"))
		}
		var input scoreMetricsWrap
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Metrics.Workloads.RunningPods != 4 || input.Metrics.AdmissionMode != nil {
			t.Errorf("input=%+v err=%v", input, err)
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()
	baseURL, _ := url.Parse(server.URL + "/v1")
	sessions := session.NewStore(10)
	sessions.SetCluster("token", "member-one")
	handler := NewHandler(controller.NewWithHTTPClient(baseURL, server.Client()), controller.NewTargetResolver(baseURL, sessions), sessions)
	engine := gin.New()
	engine.POST("/dashboard/scores", handler.PostScores)
	body := `{"metrics":{"platform":"kubernetes","kube_version":"1.30","openshift_version":"","new_service_policy_mode":"discover","new_service_profile_mode":"learn","adm_mode":null,"enabled_deny_adm_ctrl_rules":null,"deny_adm_ctrl_rules":1,"hosts":2,"workloads":{"running_pods":4,"privileged_wls":0,"root_wls":1,"discover_ext_eps":2,"monitor_ext_eps":3,"protect_ext_eps":4,"threat_ext_eps":5,"violate_ext_eps":6},"groups":{"groups":7,"discover_groups":1,"monitor_groups":2,"protect_groups":3,"profile_discover_groups":4,"profile_monitor_groups":5,"profile_protect_groups":6,"discover_groups_zero_drift":7,"monitor_groups_zero_drift":8,"protect_groups_zero_drift":9},"cves":{"discover_cves":1,"monitor_cves":2,"protect_cves":3,"platform_cves":4,"host_cves":5}}}`
	request := httptest.NewRequest(http.MethodPost, "/dashboard/scores", strings.NewReader(body))
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != `{"status":"ok"}` {
		t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGetNotificationsConvertsAndGroupsEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Nv-Page") != "dashboard" {
			t.Errorf("page=%q", r.Header.Get("X-Nv-Page"))
		}
		switch r.URL.EscapedPath() {
		case "/v1/fed/cluster/member-one/v1/log/threat":
			_, _ = w.Write([]byte(`{"threats":[{"name":"threat","host_name":"host","level":"critical","client_workload_id":"client","client_workload_name":"destination","client_workload_domain":"client-domain","client_ip":"1","client_port":1,"server_workload_id":"server","server_workload_name":"source","server_workload_domain":"server-domain","server_ip":"2","server_port":2,"server_conn_port":3,"application":"TLS","target":"client","reported_at":"2026-08-02T10:00:00+08:00"}]}`))
		case "/v1/fed/cluster/member-one/v1/log/violation":
			_, _ = w.Write([]byte(`{"violations":[{"policy_id":7,"host_name":"host","level":"warning","client_id":"client","client_ip":"1","client_name":"source","client_domain":"client-domain","server_id":"server","server_ip":"2","server_name":"destination","server_domain":"server-domain","server_port":2,"application":"TLS","reported_at":"2026-08-02T11:00:00+08:00"}]}`))
		case "/v1/fed/cluster/member-one/v1/log/incident":
			_, _ = w.Write([]byte(`{"incidents":[]}`))
		default:
			t.Errorf("path=%s", r.URL.EscapedPath())
		}
	}))
	defer server.Close()
	baseURL, _ := url.Parse(server.URL + "/v1")
	sessions := session.NewStore(10)
	sessions.SetCluster("token", "member-one")
	handler := NewHandler(controller.NewWithHTTPClient(baseURL, server.Client()), controller.NewTargetResolver(baseURL, sessions), sessions)
	engine := gin.New()
	engine.GET("/dashboard/notifications", handler.GetNotifications)
	request := httptest.NewRequest(http.MethodGet, "/dashboard/notifications", nil)
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	var output struct {
		Critical criticalDashboardEvents `json:"criticalSecurityEvents"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &output) != nil {
		t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
	}
	if len(output.Critical.TopSecurityEvents.Source) != 1 || len(output.Critical.TopSecurityEvents.Source[0]) != 2 || output.Critical.TopSecurityEvents.Source[0][0].SourceWorkloadName == nil || *output.Critical.TopSecurityEvents.Source[0][0].SourceWorkloadName != "source" {
		t.Fatalf("top sources=%+v", output.Critical.TopSecurityEvents.Source)
	}
	if len(output.Critical.Summary["critical"]) != 1 || output.Critical.Summary["critical"][0][1].(float64) != 1 || output.Critical.Summary["warning"][0][1].(float64) != 1 {
		t.Fatalf("summary=%+v", output.Critical.Summary)
	}
}

func TestGetMultiClusterSummaryCachesSummaryAndDefaultsFailedScore(t *testing.T) {
	summaryRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/v1/fed/cluster/member-one/v1/fed/cluster/child-one/v1/system/summary":
			summaryRequests++
			if summaryRequests > 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"summary":{"cluster_id":"child-one"}}`))
		case "/v1/fed/cluster/member-one/v1/fed/cluster/child-one/v1/system/score/metrics":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			t.Errorf("path=%s", r.URL.EscapedPath())
		}
	}))
	defer server.Close()
	baseURL, _ := url.Parse(server.URL + "/v1")
	sessions := session.NewStore(10)
	sessions.SetCluster("token", "member-one")
	handler := NewHandler(controller.NewWithHTTPClient(baseURL, server.Client()), controller.NewTargetResolver(baseURL, sessions), sessions)
	engine := gin.New()
	engine.GET("/multi-cluster-summary", handler.GetMultiClusterSummary)
	for attempt := 0; attempt < 2; attempt++ {
		request := httptest.NewRequest(http.MethodGet, "/multi-cluster-summary?clusterId=child-one", nil)
		request.Header.Set("Token", "token")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		var output multiClusterSummary
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &output) != nil {
			t.Fatalf("attempt=%d response=%d body=%s", attempt, response.Code, response.Body.String())
		}
		if output.SummaryJSON != `{"summary":{"cluster_id":"child-one"}}` || output.Score.SecurityRiskScore != 0 {
			t.Fatalf("attempt=%d output=%+v", attempt, output)
		}
	}
}

func TestDashboardDetailsAggregatesResources(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Nv-Page") != "dashboard" {
			t.Errorf("page=%q", r.Header.Get("X-Nv-Page"))
		}
		responses := map[string]string{
			"/v1/fed/cluster/member-one/v1/host":         `{"hosts":[{"id":"host","name":"node","os":"linux","scan_summary":{"status":"finished","critical":0,"high":2,"medium":1}}]}`,
			"/v1/fed/cluster/member-one/v1/group":        `{"groups":[{"domain":"team","name":"nv.web","policy_mode":"protect","platform_role":"","members":[{"id":"w1"}],"not_scored":false,"kind":"container"}]}`,
			"/v1/fed/cluster/member-one/v1/policy/rule":  `{"rules":[{"id":1,"from":"nv.web","to":"external","applications":["https"]}]}`,
			"/v1/fed/cluster/member-one/v1/workload":     `{"workloads":[{"id":"w1","display_name":"web","state":"protect","service":"web","platform_role":"","domain":"team","scan_summary":{"status":"finished","critical":1,"high":2,"medium":0},"children":null}]}`,
			"/v1/fed/cluster/member-one/v1/conversation": `{"endpoints":[],"conversations":[{"from":"w1","to":"external","bytes":42,"applications":["https"]}]}`,
			"/v1/fed/cluster/member-one/v1/scan/config":  `{"config":{"auto_scan":true}}`,
		}
		body, ok := responses[r.URL.EscapedPath()]
		if !ok {
			t.Errorf("path=%s", r.URL.EscapedPath())
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL + "/v1")
	sessions := session.NewStore(4)
	sessions.SetCluster("token", "member-one")
	handler := NewHandler(controller.NewWithHTTPClient(base, server.Client()), controller.NewTargetResolver(base, sessions), sessions)
	engine := gin.New()
	engine.GET("/dashboard/details", handler.GetDetails)
	request := httptest.NewRequest(http.MethodGet, "/dashboard/details?domain=team", nil)
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var output map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output["autoScanConfig"] != true || len(output["containers"].([]any)) != 1 || output["highPriorityVulnerabilities"].(map[string]any)["containers"].(map[string]any)["vulnerabilitiesTotal"] != float64(1) {
		t.Fatalf("output=%v", output)
	}
}

func TestDashboardDetailsKeepsPartialResultsWhenOneResourceFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/v1/fed/cluster/member-one/v1/host" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		responses := map[string]string{
			"/v1/fed/cluster/member-one/v1/group":        `{"groups":[]}`,
			"/v1/fed/cluster/member-one/v1/policy/rule":  `{"rules":[]}`,
			"/v1/fed/cluster/member-one/v1/workload":     `{"workloads":[]}`,
			"/v1/fed/cluster/member-one/v1/conversation": `{"endpoints":[],"conversations":[]}`,
			"/v1/fed/cluster/member-one/v1/scan/config":  `{"config":{"auto_scan":false}}`,
		}
		body, ok := responses[r.URL.EscapedPath()]
		if !ok {
			t.Errorf("path=%s", r.URL.EscapedPath())
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL + "/v1")
	sessions := session.NewStore(4)
	sessions.SetCluster("token", "member-one")
	handler := NewHandler(controller.NewWithHTTPClient(base, server.Client()), controller.NewTargetResolver(base, sessions), sessions)
	engine := gin.New()
	engine.GET("/dashboard/details", handler.GetDetails)
	request := httptest.NewRequest(http.MethodGet, "/dashboard/details", nil)
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	var output map[string]any
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &output) != nil {
		t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
	}
	high := output["highPriorityVulnerabilities"].(map[string]any)
	nodes := high["nodes"].(map[string]any)
	if !strings.Contains(nodes["message"].(string), "Status: 500") || output["containers"] == nil || output["autoScanConfig"] != false {
		t.Fatalf("output=%v", output)
	}
}

func TestDashboardDetailsPropagatesControllerAvailabilityFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL + "/v1")
	sessions := session.NewStore(4)
	sessions.SetCluster("token", "member-one")
	handler := NewHandler(controller.NewWithHTTPClient(base, server.Client()), controller.NewTargetResolver(base, sessions), sessions)
	engine := gin.New()
	engine.GET("/dashboard/details", handler.GetDetails)
	request := httptest.NewRequest(http.MethodGet, "/dashboard/details", nil)
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || response.Body.String() != "Server is not available!" {
		t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
	}
}
