package risk

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

func TestProfileExports(t *testing.T) {
	for _, test := range []struct{ route, resource string }{
		{"/risk/cve/profile/export", "vulnerability"},
		{"/risk/compliance/profile/export", "compliance"},
	} {
		t.Run(test.resource, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				want := `{"names":["profile-one"],"remote_export_options":{"remote_repository_nickname":"repo-one"}}`
				wantPath := "/v1/fed/cluster/member-one/v1/file/" + test.resource + "/profile"
				if r.URL.EscapedPath() != wantPath || r.URL.RawQuery != "" || string(body) != want {
					t.Errorf("target=%s?%s body=%s", r.URL.EscapedPath(), r.URL.RawQuery, body)
				}
				_, _ = w.Write([]byte("exported"))
			}))
			defer controllerServer.Close()
			engine := testEngine(t, controllerServer)
			input := `{"names":["profile-one"],"remote_export_options":{"remote_repository_nickname":"repo-one","file_path":null,"comment":null}}`
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

func TestProfileImports(t *testing.T) {
	tests := []struct {
		name, route, wantPath, wantOption, transaction, input, wantBody string
	}{
		{"vulnerability transaction", "/risk/cve/profile/import?option=replace%2Fall", "/v1/fed/cluster/member-one/v1/file/vulnerability/profile/config", "replace/all", "tx-cve", "ignored", ""},
		{"vulnerability form", "/risk/cve/profile/import?option=merge", "/v1/fed/cluster/member-one/v1/file/vulnerability/profile/config", "merge", "", "--b\nDisposition\nType\n\ncve-profile\n--b--\n", "cve-profile"},
		{"compliance transaction", "/risk/compliance/profile/import", "/v1/fed/cluster/member-one/v1/file/compliance/profile/config", "", "tx-compliance", "ignored", ""},
		{"compliance form", "/risk/compliance/profile/import", "/v1/fed/cluster/member-one/v1/file/compliance/profile/config", "", "", "--b\nDisposition\nType\n\ncompliance-profile\n--b--\n", "compliance-profile"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.URL.EscapedPath() != test.wantPath || r.URL.Query().Get("option") != test.wantOption || r.Header.Get("X-Transaction-Id") != test.transaction || string(body) != test.wantBody {
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

func TestProfileValidation(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("Controller must not be called") }))
	defer controllerServer.Close()
	engine := testEngine(t, controllerServer)
	for _, test := range []struct{ route, body string }{
		{"/risk/cve/profile/export", `not-json`},
		{"/risk/cve/profile/export", `{}`},
		{"/risk/compliance/profile/export", `{"names":[],"remote_export_options":{}}`},
		{"/risk/cve/profile/import", ``},
	} {
		request := httptest.NewRequest(http.MethodPost, test.route, strings.NewReader(test.body))
		request.Header.Set("Token", "token")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s body=%q status=%d", test.route, test.body, response.Code)
		}
	}
}

func TestProfileCRUD(t *testing.T) {
	tests := []struct {
		name, method, route, input, wantPath, wantBody string
	}{
		{"list vulnerability", http.MethodGet, "/risk/cve/profile", "", "/v1/fed/cluster/member-one/v1/vulnerability/profile", ""},
		{"update vulnerability", http.MethodPatch, "/risk/cve/profile", `{"config":{"name":"profile/one","entries":null}}`, "/v1/fed/cluster/member-one/v1/vulnerability/profile/profile%2Fone", `{"config":{"name":"profile/one"}}`},
		{"add vulnerability entry", http.MethodPost, "/risk/cve/profile/entry", `{"config":{"name":"profile/one","entries":[{"id":null,"name":"entry-one","comment":"fixture","days":7,"domains":[],"images":["image:tag"]},{"id":null,"name":"ignored","comment":"fixture","days":1,"domains":[],"images":[]}]}}`, "/v1/fed/cluster/member-one/v1/vulnerability/profile/profile%2Fone/entry", `{"config":{"name":"entry-one","comment":"fixture","days":7,"domains":[],"images":["image:tag"]}}`},
		{"update vulnerability entry", http.MethodPatch, "/risk/cve/profile/entry?name=profile%2Fone", `{"config":{"id":7,"name":"entry-one","comment":"updated","days":14,"domains":["prod"],"images":[]}}`, "/v1/fed/cluster/member-one/v1/vulnerability/profile/profile%2Fone/entry/7", `{"config":{"id":7,"name":"entry-one","comment":"updated","days":14,"domains":["prod"],"images":[]}}`},
		{"delete vulnerability entry", http.MethodDelete, "/risk/cve/profile/entry?profile_name=profile%2Fone&entry_id=entry%2F7", "", "/v1/fed/cluster/member-one/v1/vulnerability/profile/profile%2Fone/entry/entry%2F7", ""},
		{"list compliance", http.MethodGet, "/risk/compliance/profile", "", "/v1/fed/cluster/member-one/v1/compliance/profile", ""},
		{"get compliance", http.MethodGet, "/risk/compliance/profile?name=profile%2Fone", "", "/v1/fed/cluster/member-one/v1/compliance/profile/profile%2Fone", ""},
		{"update compliance", http.MethodPatch, "/risk/compliance/profile", `{"name":"profile/one","disable_system":null,"entries":[{"test_number":"I.4.1","tags":["PCI"]}]}`, "/v1/fed/cluster/member-one/v1/compliance/profile/profile%2Fone", `{"config":{"name":"profile/one","entries":[{"test_number":"I.4.1","tags":["PCI"]}]}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.Method != test.method || r.URL.EscapedPath() != test.wantPath || string(body) != test.wantBody {
					t.Errorf("request=%s %s body=%s", r.Method, r.URL.EscapedPath(), body)
				}
				_, _ = w.Write([]byte("ok"))
			}))
			defer controllerServer.Close()
			engine := testEngine(t, controllerServer)
			request := httptest.NewRequest(test.method, test.route, strings.NewReader(test.input))
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "ok" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestRiskQueries(t *testing.T) {
	tests := []struct{ name, method, route, input, wantMethod, wantPath, wantQuery, wantBody string }{
		{"cve", http.MethodGet, "/risk/cve?show=all", "", http.MethodGet, "/v1/fed/cluster/member-one/v1/scan/asset", "show=all", ""},
		{"assets", http.MethodPatch, "/risk/cve/assets-view?queryId=query%2Fone", `{"last_modified_timestamp":123,"include_no_vul_assets":null}`, http.MethodPost, "/v1/fed/cluster/member-one/v1/assetvul", "token=query%2Fone", `{"last_modified_timestamp":123}`},
		{"compliance", http.MethodGet, "/risk/compliance", "", http.MethodGet, "/v1/fed/cluster/member-one/v1/compliance/asset", "", ""},
		{"template", http.MethodGet, "/risk/compliance/template", "", http.MethodGet, "/v1/fed/cluster/member-one/v1/list/compliance", "", ""},
		{"filters", http.MethodGet, "/risk/compliance/available_filter", "", http.MethodGet, "/v1/fed/cluster/member-one/v1/compliance/available_filter", "", ""},
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
			engine := testEngine(t, controllerServer)
			request := httptest.NewRequest(test.method, test.route, strings.NewReader(test.input))
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "ok" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAssetQueries(t *testing.T) {
	tests := []struct{ name, method, route, input, wantMethod, wantPath, wantQuery, wantBody string }{
		{"query scanned", http.MethodPost, "/scanned-assets", `{"type":"image"}`, http.MethodPost, "/v1/fed/cluster/member-one/v1/scan/asset/images", "", `{"type":"image"}`},
		{"get scanned", http.MethodGet, "/scanned-assets?token=page%2Fone&start=0&row=20&orderby=desc&orderbyColumn=severity&qf=name%3Dfixture+image", "", http.MethodGet, "/v1/fed/cluster/member-one/v1/scan/asset/images", "orderby=desc&orderbyColumn=severity&qf=name%3Dfixture+image&row=20&start=0&token=page%2Fone", ""},
		{"query vulnerable", http.MethodPost, "/vulasset", `{"last_modified_timestamp":null,"viewType":"image"}`, http.MethodPost, "/v1/fed/cluster/member-one/v1/vulasset", "", `{"viewType":"image"}`},
		{"get vulnerable", http.MethodGet, "/vulasset?token=page-one&start=20&row=10&lastmtime=123&orderby=asc&orderbyColumn=name&qf=domain%3Dprod&scoretype=v3", "", http.MethodGet, "/v1/fed/cluster/member-one/v1/vulasset", "lastmtime=123&orderby=asc&orderbyColumn=name&qf=domain%3Dprod&row=10&scoretype=v3&start=20&token=page-one", ""},
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
			engine := testEngine(t, controllerServer)
			request := httptest.NewRequest(test.method, test.route, strings.NewReader(test.input))
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "ok" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestQueryNISTCompliances(t *testing.T) {
	t.Setenv("CIS_NIST_DB", "../../../admin/src/main/resources/CIS_NIST-MASTER.CSV")
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("Controller must not be called")
	}))
	defer server.Close()
	engine := testEngine(t, server)
	request := httptest.NewRequest(http.MethodPost, "/risk/complianceNIST", strings.NewReader(`{"config":{"names":["D.1.1.2","missing"]}}`))
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response=%d type=%q body=%s", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	want := `{"nist_map":{"D.1.1.2":{"name":"D.1.1.2","subcontrol":"6.1","control_id":"IA-4,IA-5,AC-1,AC-2,AC-2(1)","title":"Establish an Access Granting Process"}}}`
	if response.Body.String() != want {
		t.Fatalf("body=%s", response.Body.String())
	}
}

func testEngine(t *testing.T, controllerServer *httptest.Server) *gin.Engine {
	t.Helper()
	baseURL, _ := url.Parse(controllerServer.URL + "/v1")
	store := session.NewStore(10)
	store.Put("token", "cookie")
	store.SetCluster("token", "member-one")
	handler := NewHandler(controller.NewWithHTTPClient(baseURL, controllerServer.Client()), controller.NewTargetResolver(baseURL, store), store)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/risk/cve/profile/export", handler.Export("vulnerability"))
	engine.POST("/risk/cve/profile/import", handler.ImportVulnerability)
	engine.GET("/risk/cve/profile", handler.GetVulnerabilityProfiles)
	engine.PATCH("/risk/cve/profile", handler.UpdateVulnerabilityProfile)
	engine.POST("/risk/cve/profile/entry", handler.AddVulnerabilityEntry)
	engine.PATCH("/risk/cve/profile/entry", handler.UpdateVulnerabilityEntry)
	engine.DELETE("/risk/cve/profile/entry", handler.DeleteVulnerabilityEntry)
	engine.POST("/risk/compliance/profile/export", handler.Export("compliance"))
	engine.POST("/risk/compliance/profile/import", handler.ImportCompliance)
	engine.GET("/risk/compliance/profile", handler.GetComplianceProfiles)
	engine.PATCH("/risk/compliance/profile", handler.UpdateComplianceProfile)
	engine.GET("/risk/cve", handler.GetCVE)
	engine.PATCH("/risk/cve/assets-view", handler.QueryCVEAssets)
	engine.GET("/risk/compliance", handler.GetCompliances)
	engine.GET("/risk/compliance/template", handler.GetComplianceTemplate)
	engine.GET("/risk/compliance/available_filter", handler.GetAvailableComplianceFilters)
	engine.POST("/scanned-assets", handler.QueryScannedAssets)
	engine.GET("/scanned-assets", handler.GetScannedAssets)
	engine.POST("/vulasset", handler.QueryVulnerabilityAssets)
	engine.GET("/vulasset", handler.GetVulnerabilityAssets)
	engine.POST("/risk/complianceNIST", handler.QueryNISTCompliances)
	return engine
}
