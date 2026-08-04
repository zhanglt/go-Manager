package workload

import (
	"bytes"
	"compress/gzip"
	"context"
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

func TestScannedWorkloadPaginationAndConversion(t *testing.T) {
	workloads := []WorkloadV2{
		scannedFixture("one", 1, 2, 3, 4, 5, 6),
		scannedFixture("two", 10, 20, 30, 1, 2, 3),
	}
	controllerCalls := 0
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		controllerCalls++
		if r.URL.EscapedPath() != "/v1/fed/cluster/member-one/v2/workload" || r.URL.Query().Get("view") != "pod" {
			t.Errorf("target = %s?%s", r.URL.EscapedPath(), r.URL.RawQuery)
		}
		w.Header().Set("Content-Encoding", "gzip")
		compressed := gzip.NewWriter(w)
		_ = json.NewEncoder(compressed).Encode(workloadsWrapV2{Workloads: workloads})
		_ = compressed.Close()
	}))
	defer controllerServer.Close()
	engine, store := testEngine(t, controllerServer)
	store.SetCluster("token", "member-one")

	first := performWorkloadRequest(engine, "/workload/scanned?start=0&limit=1")
	var firstPage []WorkloadV2
	if first.Code != http.StatusOK || json.Unmarshal(first.Body.Bytes(), &firstPage) != nil || len(firstPage) != 1 {
		t.Fatalf("first page = %d %s", first.Code, first.Body.String())
	}
	if got := *firstPage[0].Security.ScanSummary.HiddenCritical; got != 5 {
		t.Errorf("hidden critical = %d, want 5", got)
	}
	if got := *firstPage[0].Security.ScanSummary.HiddenHigh; got != 7 {
		t.Errorf("hidden high = %d, want 7", got)
	}
	if got := *firstPage[0].Security.ScanSummary.HiddenMedium; got != 9 {
		t.Errorf("hidden medium = %d, want 9", got)
	}

	last := performWorkloadRequest(engine, "/workload/scanned?start=1&limit=2")
	var lastPage []WorkloadV2
	if last.Code != http.StatusOK || json.Unmarshal(last.Body.Bytes(), &lastPage) != nil || len(lastPage) != 1 {
		t.Fatalf("last page = %d %s", last.Code, last.Body.String())
	}
	empty := performWorkloadRequest(engine, "/workload/scanned?start=1&limit=2")
	if empty.Code != http.StatusOK || empty.Body.String() != "[]" {
		t.Fatalf("post-eviction page = %d %s", empty.Code, empty.Body.String())
	}
	if controllerCalls != 1 {
		t.Fatalf("Controller calls = %d, want 1", controllerCalls)
	}
}

func TestScannedWorkloadCacheIsClusterIsolated(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(workloadsWrapV2{Workloads: []WorkloadV2{
			scannedFixture("one", 1, 1, 1, 0, 0, 0),
			scannedFixture("two", 1, 1, 1, 0, 0, 0),
		}})
	}))
	defer controllerServer.Close()
	engine, store := testEngine(t, controllerServer)
	store.SetCluster("token", "member-one")
	if response := performWorkloadRequest(engine, "/workload/scanned?start=0&limit=1"); response.Code != http.StatusOK {
		t.Fatalf("initial response = %d", response.Code)
	}
	store.SetCluster("token", "member-two")
	response := performWorkloadRequest(engine, "/workload/scanned?start=1&limit=1")
	if response.Code != http.StatusOK || response.Body.String() != "[]" {
		t.Fatalf("cross-cluster response = %d %s", response.Code, response.Body.String())
	}
}

func TestScannedWorkloadInvalidPagination(t *testing.T) {
	controllerServer := httptest.NewServer(http.NotFoundHandler())
	defer controllerServer.Close()
	engine, _ := testEngine(t, controllerServer)
	response := performWorkloadRequest(engine, "/workload/scanned?start=invalid&limit=10")
	if response.Code != http.StatusInternalServerError || response.Body.String() != "Internal server error" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}

func scannedFixture(id string, critical, high, medium, childCritical, childHigh, childMedium int32) WorkloadV2 {
	child := WorkloadV2Child{Security: WorkloadSecurityV2{ScanSummary: ScanSummary{
		Status: "scanned", Critical: childCritical, High: childHigh, Medium: childMedium,
	}}}
	return WorkloadV2{
		Brief: WorkloadBriefV2{ID: id, Name: id, DisplayName: id},
		Security: WorkloadSecurityV2{ScanSummary: ScanSummary{
			Status: "scanned", Critical: critical, High: high, Medium: medium,
		}},
		Children: []WorkloadV2Child{child},
	}
}

func performWorkloadRequest(engine *gin.Engine, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func TestWorkloadRoutes(t *testing.T) {
	tests := []struct {
		name, method, manager, body, wantPath, wantBody string
		wantQuery                                       url.Values
	}{
		{"workloads", http.MethodGet, "/workload", "", "/v1/fed/cluster/member-one/v1/workload", "", url.Values{"view": {"pod"}}},
		{"workload stats", http.MethodGet, "/workload?id=workload%2Fone", "", "/v1/fed/cluster/member-one/v1/workload/workload%2Fone/stats", "", nil},
		{"quarantine", http.MethodPost, "/workload", `{"id":"workload/one","quarantine":true}`, "/v1/fed/cluster/member-one/v1/workload/workload%2Fone", `{"config":{"quarantine":true}}`, nil},
		{"workload by id", http.MethodGet, "/workload/workload-by-id?id=workload%2Fone", "", "/v1/fed/cluster/member-one/v1/workload/workload%2Fone", "", nil},
		{"monitor", http.MethodGet, "/workload/monitor?id=workload%2Fone&monitor=true", "", "/v1/fed/cluster/member-one/v1/workload/workload%2Fone", `{"config": {"monitor": true}}`, nil},
		{"compliance", http.MethodGet, "/workload/compliance?id=workload%2Fone", "", "/v1/fed/cluster/member-one/v1/workload/workload%2Fone/compliance", "", nil},
		{"scan report", http.MethodPost, "/workload/scan-report", `{"show_accepted":false,"max_cve_records":0,"cursor":{"name":"workload-one","host_name":null},"filters":[],"severity_filter":"high"}`, "/v1/fed/cluster/member-one/v1/scan/workloads/scan_report", `{"cursor":{"name":"workload-one"},"filters":[],"max_cve_records":0,"severity_filter":"high","show_accepted":false}`, nil},
		{"containers", http.MethodGet, "/container", "", "/v1/fed/cluster/member-one/v1/workload", "", url.Values{"view": {"pod"}}},
		{"container", http.MethodGet, "/container?id=workload%2Fone", "", "/v1/fed/cluster/member-one/v1/workload/workload%2Fone", "", url.Values{"view": {"pod"}}},
		{"processes", http.MethodGet, "/container/process?id=workload%2Fone", "", "/v1/fed/cluster/member-one/v1/workload/workload%2Fone/process", "", nil},
		{"process history", http.MethodGet, "/container/processHistory?id=workload%2Fone", "", "/v1/fed/cluster/member-one/v1/workload/workload%2Fone/process_history", "", nil},
		{"domains", http.MethodGet, "/domain", "", "/v1/fed/cluster/member-one/v1/domain", "", nil},
		{"update domain", http.MethodPatch, "/domain", `{"name":"team/one","tags":null}`, "/v1/fed/cluster/member-one/v1/domain/team%2Fone", `{"config":{"name":"team/one"}}`, nil},
		{"update domain settings", http.MethodPost, "/domain", `{"tag_per_domain":false}`, "/v1/fed/cluster/member-one/v1/domain", `{"config":{"tag_per_domain":false}}`, nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() != test.wantPath || r.URL.Query().Encode() != test.wantQuery.Encode() {
					t.Errorf("target = %q?%s, want %q?%s", r.URL.EscapedPath(), r.URL.Query().Encode(), test.wantPath, test.wantQuery.Encode())
				}
				body, _ := io.ReadAll(r.Body)
				if string(body) != test.wantBody {
					t.Errorf("body = %q, want %q", body, test.wantBody)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"ok"}`))
			}))
			defer controllerServer.Close()
			engine, store := testEngine(t, controllerServer)
			store.SetCluster("token", "member-one")
			request := httptest.NewRequest(test.method, test.manager, strings.NewReader(test.body))
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != `{"status":"ok"}` {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestSnifferRoutes(t *testing.T) {
	snifferInput := `{"workloadId":"workload/one","snifferParamWarp":{"sniffer":{"file_number":2,"duration":60,"filter":null}}}`
	tests := []struct {
		name, method, manager, body, wantPath, wantBody string
		wantQuery                                       url.Values
	}{
		{"list", http.MethodGet, "/sniffer?id=workload%2Fone", "", "/v1/fed/cluster/member-one/v1/sniffer", "", url.Values{"f_workload": {"workload/one"}}},
		{"create", http.MethodPost, "/sniffer", snifferInput, "/v1/fed/cluster/member-one/v1/sniffer", `{"sniffer":{"file_number":2,"duration":60}}`, url.Values{"f_workload": {"workload/one"}}},
		{"stop", http.MethodPatch, "/sniffer", "sniffer/one", "/v1/fed/cluster/member-one/v1/sniffer/stop/sniffer%2Fone", "", nil},
		{"delete", http.MethodDelete, "/sniffer?id=sniffer%2Fone", "", "/v1/fed/cluster/member-one/v1/sniffer/sniffer%2Fone", "", nil},
		{"pcap", http.MethodGet, "/sniffer/pcap?id=sniffer%2Fone", "", "/v1/fed/cluster/member-one/v1/sniffer/sniffer%2Fone/pcap", "", url.Values{"limit": {"104857600"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() != test.wantPath || r.URL.Query().Encode() != test.wantQuery.Encode() {
					t.Errorf("target = %q?%s, want %q?%s", r.URL.EscapedPath(), r.URL.Query().Encode(), test.wantPath, test.wantQuery.Encode())
				}
				body, _ := io.ReadAll(r.Body)
				if string(body) != test.wantBody {
					t.Errorf("body = %q, want %q", body, test.wantBody)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"ok"}`))
			}))
			defer controllerServer.Close()
			engine, store := testEngine(t, controllerServer)
			store.SetCluster("token", "member-one")
			request := httptest.NewRequest(test.method, test.manager, strings.NewReader(test.body))
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != `{"status":"ok"}` {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestPCAPStreamsBinaryResponse(t *testing.T) {
	payload := bytes.Repeat([]byte{0x00, 0x7f, 0xff, 0x42}, 512*1024)
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.tcpdump.pcap")
		w.Header().Set("Content-Disposition", `attachment; filename=fixture.pcap`)
		_, _ = w.Write(payload)
	}))
	defer controllerServer.Close()
	engine, _ := testEngine(t, controllerServer)
	request := httptest.NewRequest(http.MethodGet, "/sniffer/pcap?id=sniffer-one", nil)
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), payload) {
		t.Fatalf("response status=%d bytes=%d", response.Code, response.Body.Len())
	}
	if response.Header().Get("Content-Type") != "application/vnd.tcpdump.pcap" {
		t.Errorf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
	if response.Header().Get("Content-Disposition") != `attachment; filename="fixture.pcap"` {
		t.Errorf("Content-Disposition = %q", response.Header().Get("Content-Disposition"))
	}
}

func TestPCAPCancellationPropagatesToController(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	controllerServer := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	defer controllerServer.Close()
	engine, _ := testEngine(t, controllerServer)
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/sniffer/pcap?id=sniffer-one", nil).WithContext(ctx)
	request.Header.Set("Token", "token")
	done := make(chan struct{})
	go func() {
		engine.ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("Controller request did not start")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("Controller request was not canceled")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Manager handler did not return after cancellation")
	}
}

func TestWorkloadValidation(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("Controller must not be called")
	}))
	defer controllerServer.Close()
	engine, _ := testEngine(t, controllerServer)
	for _, test := range []struct{ method, path, body string }{
		{http.MethodPost, "/workload", `{}`},
		{http.MethodGet, "/workload/workload-by-id", ""},
		{http.MethodGet, "/workload/monitor?id=one", ""},
		{http.MethodGet, "/workload/compliance", ""},
		{http.MethodGet, "/container/process", ""},
		{http.MethodPatch, "/domain", `{}`},
	} {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		request.Header.Set("Token", "token")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s %s = %d", test.method, test.path, response.Code)
		}
	}
}

func testEngine(t *testing.T, controllerServer *httptest.Server) (*gin.Engine, *session.Store) {
	t.Helper()
	baseURL, err := url.Parse(controllerServer.URL + "/v1")
	if err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(10)
	handler := NewHandler(controller.NewWithHTTPClient(baseURL, controllerServer.Client()), controller.NewTargetResolver(baseURL, store), store)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/workload", handler.GetWorkloads)
	engine.POST("/workload", handler.UpdateWorkload)
	engine.GET("/workload/workload-by-id", handler.GetWorkloadByID)
	engine.GET("/workload/monitor", handler.UpdateMonitor)
	engine.GET("/workload/compliance", handler.GetCompliance)
	engine.GET("/workload/scanned", handler.GetScannedWorkloads)
	engine.POST("/workload/scan-report", handler.GetScanReport)
	engine.GET("/container", handler.GetContainers)
	engine.GET("/container/process", handler.GetProcesses)
	engine.GET("/container/processHistory", handler.GetProcessHistory)
	engine.GET("/domain", handler.GetDomains)
	engine.PATCH("/domain", handler.UpdateDomain)
	engine.POST("/domain", handler.UpdateDomainSettings)
	engine.GET("/sniffer", handler.GetSniffers)
	engine.POST("/sniffer", handler.CreateSniffer)
	engine.PATCH("/sniffer", handler.StopSniffer)
	engine.DELETE("/sniffer", handler.DeleteSniffer)
	engine.GET("/sniffer/pcap", handler.GetPCAP)
	return engine, store
}
