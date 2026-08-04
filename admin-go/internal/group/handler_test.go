package group

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/session"
)

func TestGroupExportRoutes(t *testing.T) {
	tests := []struct{ route, scope string }{{"/group/export", "local"}, {"/group/export-fed", "fed"}}
	for _, test := range tests {
		t.Run(test.scope, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				want := `{"groups":["group-one"],"use_name_referral":true,"remote_export_options":{"remote_repository_nickname":"repo-one"}}`
				if r.URL.EscapedPath() != "/v1/fed/cluster/member-one/v1/file/group" || r.URL.Query().Get("scope") != test.scope || string(body) != want {
					t.Errorf("target=%s?%s body=%s", r.URL.EscapedPath(), r.URL.RawQuery, body)
				}
				_, _ = w.Write([]byte("exported"))
			}))
			defer controllerServer.Close()
			engine := testEngine(t, controllerServer)
			input := `{"groups":["group-one"],"policy_mode":null,"profile_mode":null,"use_name_referral":true,"remote_export_options":{"remote_repository_nickname":"repo-one","file_path":null,"comment":null}}`
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

func TestGroupImportRoutes(t *testing.T) {
	tests := []struct {
		route, scope, transaction, input, wantBody string
	}{
		{"/group/import", "local", "tx-local", "ignored", ""},
		{"/group/import-fed", "fed", "", "--b\nDisposition\nType\n\nline-one\nline-two\n--b--\n", "line-one\nline-two"},
	}
	for _, test := range tests {
		t.Run(test.scope, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.URL.EscapedPath() != "/v1/fed/cluster/member-one/v1/file/group/config" || r.URL.Query().Get("scope") != test.scope || string(body) != test.wantBody || r.Header.Get("X-Transaction-Id") != test.transaction {
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

func TestGroupExportValidation(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("Controller must not be called")
	}))
	defer controllerServer.Close()
	engine := testEngine(t, controllerServer)
	for _, input := range []string{`not-json`, `{"groups":["one"]}`, `{"use_name_referral":true}`, `{"groups":[],"use_name_referral":false,"remote_export_options":{}}`} {
		request := httptest.NewRequest(http.MethodPost, "/group/export", strings.NewReader(input))
		request.Header.Set("Token", "token")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("input %q status=%d", input, response.Code)
		}
	}
}

func TestGroupListAndCustomCheck(t *testing.T) {
	tests := []struct{ name, method, route, body, wantPath, wantQuery, wantBody string }{
		{"group list", http.MethodGet, "/group-list?scope=fed&f_kind=container", "", "/v1/fed/cluster/member-one/v1/group", "brief=true&f_kind=container&scope=fed&start=0", ""},
		{"get custom check", http.MethodGet, "/group/custom_check?name=group%2Fone", "", "/v1/fed/cluster/member-one/v1/custom_check/group%2Fone", "", ""},
		{"update custom check", http.MethodPatch, "/group/custom_check", `{"group":"group/one","config":{"add":{"scripts":[{"name":"check","script":"echo ok"}]},"delete":null,"update":null}}`, "/v1/fed/cluster/member-one/v1/custom_check/group%2Fone", "", `{"config":{"add":{"scripts":[{"name":"check","script":"echo ok"}]}}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.Method != test.method || r.URL.EscapedPath() != test.wantPath || r.URL.Query().Encode() != test.wantQuery || string(body) != test.wantBody {
					t.Errorf("request=%s %s?%s body=%s", r.Method, r.URL.EscapedPath(), r.URL.RawQuery, body)
				}
				_, _ = w.Write([]byte("ok"))
			}))
			defer controllerServer.Close()
			engine := testEngine(t, controllerServer)
			request := httptest.NewRequest(test.method, test.route, strings.NewReader(test.body))
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "ok" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestGroupMutations(t *testing.T) {
	baseInput := `{"name":"group/one","comment":"fixture","criteria":[{"name":"image=nginx"},{"name":"domain!=prod"}],"cfg_type":"user_created","monitor_metric":true,"group_sess_cur":10,"group_sess_rate":20,"group_band_width":30}`
	learnedInput := strings.Replace(baseInput, `"cfg_type":"user_created"`, `"cfg_type":"learned"`, 1)
	federalInput := strings.Replace(baseInput, `"cfg_type":"user_created"`, `"cfg_type":"federal"`, 1)
	fullUser := `{"config":{"name":"group/one","comment":"fixture","criteria":[{"key":"image","value":"nginx","op":"="},{"key":"domain","value":"prod","op":"!="}],"cfg_type":"user_created","monitor_metric":true,"group_sess_cur":10,"group_sess_rate":20,"group_band_width":30}}`
	fullFederal := strings.Replace(fullUser, `"cfg_type":"user_created"`, `"cfg_type":"federal"`, 1)
	learned := `{"config":{"name":"group/one","monitor_metric":true,"group_sess_cur":10,"group_sess_rate":20,"group_band_width":30}}`
	tests := []struct{ name, method, route, input, wantPath, wantQuery, wantBody string }{
		{"create local", http.MethodPost, "/group", baseInput, "/v1/fed/cluster/member-one/v1/group", "", fullUser},
		{"create federal", http.MethodPost, "/group", federalInput, "/v1/group", "", fullFederal},
		{"update local", http.MethodPatch, "/group", baseInput, "/v1/fed/cluster/member-one/v1/group/group%2Fone", "", fullUser},
		{"update learned", http.MethodPatch, "/group", learnedInput, "/v1/fed/cluster/member-one/v1/group/group%2Fone", "", learned},
		{"update federal", http.MethodPatch, "/group", federalInput, "/v1/group/group%2Fone", "", fullFederal},
		{"delete local", http.MethodDelete, "/group?name=group%2Fone", "", "/v1/fed/cluster/member-one/v1/group/group%2Fone", "", ""},
		{"delete scoped", http.MethodDelete, "/group?name=group%2Fone&scope=local", "", "/v1/fed/cluster/member-one/v1/group/group%2Fone", "scope=local", ""},
		{"delete federal", http.MethodDelete, "/group?name=group%2Fone&scope=fed", "", "/v1/group/group%2Fone", "", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.Method != test.method || r.URL.EscapedPath() != test.wantPath || r.URL.Query().Encode() != test.wantQuery || string(body) != test.wantBody {
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

func TestGetGroupsBranches(t *testing.T) {
	group := `{"name":"group-one","comment":"fixture","domain":"prod","learned":false,"reserved":false,"criteria":[{"key":"image","value":"nginx","op":"contains"},{"key":"domain","value":"prod","op":"!regex"}],"members":[],"policy_rules":[1],"response_rules":null,"policy_mode":null,"profile_mode":"basic","baseline_profile":null,"platform_role":"","cap_change_mode":true,"cap_scorable":null,"kind":"container","cfg_type":"user_created","not_scored":false,"monitor_metric":true,"group_sess_cur":10,"group_sess_rate":20,"group_band_width":30}`
	tests := []struct {
		name, route, wantPath, wantQuery, response string
		wantArray, wantEmptyRules                  bool
	}{
		{"list", "/group", "/v1/fed/cluster/member-one/v1/group", "view=pod&with_cap=false", `{"groups":[` + group + `]}`, true, true},
		{"federal list", "/group?scope=fed&with_cap=true", "/v1/group", "scope=fed&view=pod", `{"groups":[` + group + `]}`, true, true},
		{"single", "/group?name=group-one&with_cap=true", "/v1/fed/cluster/member-one/v1/group/group-one", "view=pod&with_cap=true", `{"group":` + strings.Replace(group, `"policy_rules":[1]`, `"policy_rules":[{"id":1}]`, 1) + `}`, false, false},
		{"scoped single", "/group?name=group-one&scope=fed", "/v1/fed/cluster/member-one/v1/group/group-one", "scope=Some%28fed%29&view=pod&with_cap=false", `{"group":` + group + `}`, false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() != test.wantPath || r.URL.Query().Encode() != test.wantQuery {
					t.Errorf("target=%s?%s", r.URL.EscapedPath(), r.URL.RawQuery)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.response))
			}))
			defer controllerServer.Close()
			response := performGroupRequest(testEngine(t, controllerServer), test.route)
			if response.Code != http.StatusOK {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
			var decoded any
			if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
				t.Fatal(err)
			}
			var output map[string]any
			if test.wantArray {
				items, ok := decoded.([]any)
				if !ok || len(items) != 1 {
					t.Fatalf("expected one-item array, got %#v", decoded)
				}
				output = items[0].(map[string]any)
			} else {
				output = decoded.(map[string]any)["group"].(map[string]any)
			}
			wantCriteria := []any{map[string]any{"name": "image@nginx"}, map[string]any{"name": "domain!~prod"}}
			if !reflect.DeepEqual(output["criteria"], wantCriteria) {
				t.Errorf("criteria=%#v", output["criteria"])
			}
			if test.wantEmptyRules && !reflect.DeepEqual(output["response_rules"], []any{}) {
				t.Errorf("response_rules=%#v", output["response_rules"])
			}
		})
	}
}

func TestGetGroupsPaginationUsesAndEvictsCache(t *testing.T) {
	var calls atomic.Int32
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		group := func(name string) string {
			return `{"name":"` + name + `","comment":"","domain":"","learned":false,"reserved":false,"criteria":[],"members":[],"policy_rules":[],"response_rules":[],"policy_mode":null,"profile_mode":null,"baseline_profile":null,"platform_role":"","cap_change_mode":null,"cap_scorable":null,"kind":"container","cfg_type":null,"not_scored":false,"monitor_metric":false,"group_sess_cur":0,"group_sess_rate":0,"group_band_width":0}`
		}
		_, _ = w.Write([]byte(`{"groups":[` + group("one") + `,` + group("two") + `]}`))
	}))
	defer controllerServer.Close()
	engine := testEngine(t, controllerServer)

	first := performGroupRequest(engine, "/group?start=0&limit=1")
	second := performGroupRequest(engine, "/group?start=1&limit=2")
	afterEviction := performGroupRequest(engine, "/group?start=1&limit=2")
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"name":"one"`) {
		t.Fatalf("first=%d %s", first.Code, first.Body.String())
	}
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), `"name":"two"`) {
		t.Fatalf("second=%d %s", second.Code, second.Body.String())
	}
	if afterEviction.Code != http.StatusOK || afterEviction.Body.String() != "[]" {
		t.Fatalf("after eviction=%d %s", afterEviction.Code, afterEviction.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("Controller calls=%d, want 1", calls.Load())
	}
}

func TestServiceRoutes(t *testing.T) {
	tests := []struct{ name, method, wantMethod, route, input, wantPath, wantQuery, wantBody string }{
		{"list", http.MethodGet, http.MethodGet, "/service", "", "/v1/fed/cluster/member-one/v1/service", "view=pod&with_cap=false", ""},
		{"single", http.MethodGet, http.MethodGet, "/service?name=service%2Fone&with_cap=true", "", "/v1/fed/cluster/member-one/v1/service/service%2Fone", "view=pod&with_cap=true", ""},
		{"update", http.MethodPatch, http.MethodPatch, "/service", `{"config":{"policy_mode":"Monitor","profile_mode":"Basic","baseline_profile":"zero-drift","services":["service-one"],"not_scored":false}}`, "/v1/fed/cluster/member-one/v1/service/config", "", `{"config":{"policy_mode":"Monitor","profile_mode":"Basic","baseline_profile":"zero-drift","services":["service-one"],"not_scored":false}}`},
		{"create", http.MethodPost, http.MethodPatch, "/service", `{"policy_mode":"Monitor","profile_mode":null,"baseline_profile":null,"services":null,"not_scored":null}`, "/v1/fed/cluster/member-one/v1/system/config", "", `{"config":{"new_service_policy_mode":"Monitor"}}`},
		{"create default", http.MethodPost, http.MethodPatch, "/service", `{}`, "/v1/fed/cluster/member-one/v1/system/config", "", `{"config":{"new_service_policy_mode":"Discover"}}`},
		{"all", http.MethodPatch, http.MethodPost, "/service/all", `{"policy_mode":"Monitor","profile_mode":null,"baseline_profile":null}`, "/v1/fed/cluster/member-one/v1/system/request", "", `{"request":{"policy_mode":"Monitor"}}`},
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

func TestServiceUpdateValidation(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("Controller must not be called")
	}))
	defer controllerServer.Close()
	engine := testEngine(t, controllerServer)
	for _, input := range []string{"not-json", `{}`} {
		request := httptest.NewRequest(http.MethodPatch, "/service", strings.NewReader(input))
		request.Header.Set("Token", "token")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("input %q returned %d", input, response.Code)
		}
	}
}

func TestProcessProfileRoutes(t *testing.T) {
	input := `{"process_profile_config":{"group":"group/one","alert_disabled":false,"hash_enabled":true,"process_delete_list":null,"process_change_list":[{"name":"sh","path":"/bin/sh","user":null,"uid":null,"action":"allow"}],"process_replace_list":null}}`
	wantBody := `{"process_profile_config":{"group":"group/one","alert_disabled":false,"hash_enabled":true,"process_change_list":[{"name":"sh","path":"/bin/sh","action":"allow"}]}}`
	tests := []struct{ name, method, route, body, wantPath, wantQuery, wantBody string }{
		{"single", http.MethodGet, "/processProfile?name=group%2Fone", "", "/v1/fed/cluster/member-one/v1/process_profile/group%2Fone", "", ""},
		{"list", http.MethodGet, "/processProfile", "", "/v1/fed/cluster/member-one/v1/process_profile", "limit=1000&start=0", ""},
		{"federal list", http.MethodGet, "/processProfile?scope=fed", "", "/v1/process_profile", "limit=1000&scope=fed&start=0", ""},
		{"update", http.MethodPatch, "/processProfile?scope=local", input, "/v1/fed/cluster/member-one/v1/process_profile/group%2Fone", "scope=local", wantBody},
		{"federal update", http.MethodPatch, "/processProfile?scope=fed", input, "/v1/process_profile/group%2Fone", "scope=fed", wantBody},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.Method != test.method || r.URL.EscapedPath() != test.wantPath || r.URL.Query().Encode() != test.wantQuery || string(body) != test.wantBody {
					t.Errorf("request=%s %s?%s body=%s", r.Method, r.URL.EscapedPath(), r.URL.RawQuery, body)
				}
				_, _ = w.Write([]byte("ok"))
			}))
			defer controllerServer.Close()
			engine := testEngine(t, controllerServer)
			request := httptest.NewRequest(test.method, test.route, strings.NewReader(test.body))
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "ok" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestProcessProfileUpdateValidation(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("Controller must not be called")
	}))
	defer controllerServer.Close()
	engine := testEngine(t, controllerServer)
	for _, input := range []string{"not-json", `{}`, `{"process_profile_config":{"group":""}}`} {
		request := httptest.NewRequest(http.MethodPatch, "/processProfile", strings.NewReader(input))
		request.Header.Set("Token", "token")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("input %q returned %d", input, response.Code)
		}
	}
}

func TestFileProfileRoutes(t *testing.T) {
	input := `{"group":"group/one","fileMonitorConfigData":{"config":{"add_filters":[{"filter":"/etc/*","recursive":true,"behavior":"monitor","applications":null}],"delete_filters":null,"update_filters":null}}}`
	wantBody := `{"config":{"add_filters":[{"filter":"/etc/*","recursive":true,"behavior":"monitor"}]}}`
	tests := []struct{ name, method, route, body, wantPath, wantQuery, wantBody string }{
		{"single", http.MethodGet, "/fileProfile?name=group%2Fone", "", "/v1/fed/cluster/member-one/v1/file_monitor/group%2Fone", "", ""},
		{"list", http.MethodGet, "/fileProfile", "", "/v1/fed/cluster/member-one/v1/file_monitor", "limit=1000&start=0", ""},
		{"federal list", http.MethodGet, "/fileProfile?scope=fed", "", "/v1/file_monitor", "limit=1000&scope=fed&start=0", ""},
		{"update", http.MethodPatch, "/fileProfile?scope=local", input, "/v1/fed/cluster/member-one/v1/file_monitor/group%2Fone", "scope=local", wantBody},
		{"federal update", http.MethodPatch, "/fileProfile?scope=fed", input, "/v1/file_monitor/group%2Fone", "scope=fed", wantBody},
		{"predefined", http.MethodGet, "/filePreProfile?name=group%2Fone", "", "/v1/fed/cluster/member-one/v1/file_monitor/group%2Fone", "predefined=", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.Method != test.method || r.URL.EscapedPath() != test.wantPath || r.URL.Query().Encode() != test.wantQuery || string(body) != test.wantBody {
					t.Errorf("request=%s %s?%s body=%s", r.Method, r.URL.EscapedPath(), r.URL.RawQuery, body)
				}
				_, _ = w.Write([]byte("ok"))
			}))
			defer controllerServer.Close()
			engine := testEngine(t, controllerServer)
			request := httptest.NewRequest(test.method, test.route, strings.NewReader(test.body))
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "ok" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestFileProfileValidation(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("Controller must not be called")
	}))
	defer controllerServer.Close()
	engine := testEngine(t, controllerServer)
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPatch, "/fileProfile", strings.NewReader(`{}`)),
		httptest.NewRequest(http.MethodGet, "/filePreProfile", nil),
	} {
		request.Header.Set("Token", "token")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("request %s returned %d", request.URL, response.Code)
		}
	}
}

func TestDlpSensorRoutes(t *testing.T) {
	input := `{"config":{"name":"sensor/one","comment":"fixture","cfg_type":"user_created","change":null,"delete":null,"rules":[{"name":"rule-one","id":null,"patterns":[]}],"predefine":false,"prerules":null}}`
	wantBody := `{"config":{"name":"sensor/one","comment":"fixture","cfg_type":"user_created","rules":[{"name":"rule-one","patterns":[]}],"predefine":false}}`
	tests := []struct{ name, method, route, body, wantPath, wantQuery, wantBody string }{
		{"list", http.MethodGet, "/dlp/sensor?scope=fed", "", "/v1/fed/cluster/member-one/v1/dlp/sensor", "scope=fed", ""},
		{"single", http.MethodGet, "/dlp/sensor?name=sensor%2Fone&scope=fed", "", "/v1/fed/cluster/member-one/v1/dlp/sensor/sensor%2Fone", "", ""},
		{"create", http.MethodPost, "/dlp/sensor", input, "/v1/fed/cluster/member-one/v1/dlp/sensor", "", wantBody},
		{"update", http.MethodPatch, "/dlp/sensor", input, "/v1/fed/cluster/member-one/v1/dlp/sensor/sensor%2Fone", "", wantBody},
		{"delete", http.MethodDelete, "/dlp/sensor?name=sensor%2Fone", "", "/v1/fed/cluster/member-one/v1/dlp/sensor/sensor%2Fone", "", ""},
		{"export", http.MethodPost, "/dlp/sensor/export", `{"names":["sensor-one"],"remote_export_options":null}`, "/v1/fed/cluster/member-one/v1/file/dlp", "scope=local", `{"names":["sensor-one"]}`},
		{"export fed", http.MethodPost, "/dlp/sensor/export-fed", `{"names":["sensor-one"],"remote_export_options":{"remote_repository_nickname":"repo-one","file_path":null,"comment":null}}`, "/v1/fed/cluster/member-one/v1/file/dlp", "scope=fed", `{"names":["sensor-one"],"remote_export_options":{"remote_repository_nickname":"repo-one"}}`},
		{"import transaction", http.MethodPost, "/dlp/sensor/import", "ignored", "/v1/fed/cluster/member-one/v1/file/dlp/config", "scope=local", ""},
		{"import form", http.MethodPost, "/dlp/sensor/import-fed", "--b\nDisposition\nType\n\nline-one\n--b--\n", "/v1/fed/cluster/member-one/v1/file/dlp/config", "scope=fed", "line-one"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.URL.EscapedPath() != test.wantPath || r.URL.Query().Encode() != test.wantQuery || string(body) != test.wantBody {
					t.Errorf("request=%s %s?%s body=%s", r.Method, r.URL.EscapedPath(), r.URL.RawQuery, body)
				}
				_, _ = w.Write([]byte("ok"))
			}))
			defer controllerServer.Close()
			engine := testEngine(t, controllerServer)
			request := httptest.NewRequest(test.method, test.route, strings.NewReader(test.body))
			request.Header.Set("Token", "token")
			if test.name == "import transaction" {
				request.Header.Set("X-Transaction-Id", "tx-one")
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "ok" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestDlpGroupRoutes(t *testing.T) {
	input := `{"config":{"name":"group/one","status":true,"delete":null,"sensors":[{"name":"sensor-one","action":"allow"}],"replace":null}}`
	wantBody := `{"config":{"name":"group/one","status":true,"sensors":[{"name":"sensor-one","action":"allow"}]}}`
	tests := []struct{ name, method, route, body, wantPath, wantBody string }{
		{"get", http.MethodGet, "/dlp/group?name=group%2Fone", "", "/v1/fed/cluster/member-one/v1/dlp/group/group%2Fone", ""},
		{"update", http.MethodPatch, "/dlp/group", input, "/v1/fed/cluster/member-one/v1/dlp/group/group%2Fone", wantBody},
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
			request := httptest.NewRequest(test.method, test.route, strings.NewReader(test.body))
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "ok" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestWafRoutes(t *testing.T) {
	sensorInput := `{"config":{"name":"sensor/one","comment":"fixture","cfg_type":"user_created","change":null,"delete":null,"rules":[{"name":"rule-one","id":null,"patterns":[]}]}}`
	sensorBody := `{"config":{"name":"sensor/one","comment":"fixture","cfg_type":"user_created","rules":[{"name":"rule-one","patterns":[]}]}}`
	groupInput := `{"config":{"name":"group/one","status":true,"delete":null,"sensors":[{"name":"sensor-one","action":"allow"}],"replace":null}}`
	groupBody := `{"config":{"name":"group/one","status":true,"sensors":[{"name":"sensor-one","action":"allow"}]}}`
	tests := []struct{ name, method, route, body, wantPath, wantQuery, wantBody string }{
		{"list", http.MethodGet, "/waf/sensor?scope=fed", "", "/v1/fed/cluster/member-one/v1/waf/sensor", "scope=fed", ""},
		{"single", http.MethodGet, "/waf/sensor?name=sensor%2Fone&scope=fed", "", "/v1/fed/cluster/member-one/v1/waf/sensor/sensor%2Fone", "", ""},
		{"create", http.MethodPost, "/waf/sensor", sensorInput, "/v1/fed/cluster/member-one/v1/waf/sensor", "", sensorBody},
		{"update", http.MethodPatch, "/waf/sensor", sensorInput, "/v1/fed/cluster/member-one/v1/waf/sensor/sensor%2Fone", "", sensorBody},
		{"delete", http.MethodDelete, "/waf/sensor?name=sensor%2Fone", "", "/v1/fed/cluster/member-one/v1/waf/sensor/sensor%2Fone", "", ""},
		{"export", http.MethodPost, "/waf/sensor/export", `{"names":["sensor-one"],"remote_export_options":null}`, "/v1/fed/cluster/member-one/v1/file/waf", "scope=local", `{"names":["sensor-one"]}`},
		{"export fed", http.MethodPost, "/waf/sensor/export-fed", `{"names":["sensor-one"],"remote_export_options":{"remote_repository_nickname":"repo-one","file_path":null,"comment":null}}`, "/v1/fed/cluster/member-one/v1/file/waf", "scope=fed", `{"names":["sensor-one"],"remote_export_options":{"remote_repository_nickname":"repo-one"}}`},
		{"import transaction", http.MethodPost, "/waf/sensor/import", "ignored", "/v1/fed/cluster/member-one/v1/file/waf/config", "scope=local", ""},
		{"import form", http.MethodPost, "/waf/sensor/import-fed", "--b\nDisposition\nType\n\nline-one\n--b--\n", "/v1/fed/cluster/member-one/v1/file/waf/config", "scope=fed", "line-one"},
		{"group get", http.MethodGet, "/waf/group?name=group%2Fone", "", "/v1/fed/cluster/member-one/v1/waf/group/group%2Fone", "", ""},
		{"group update", http.MethodPatch, "/waf/group", groupInput, "/v1/fed/cluster/member-one/v1/waf/group/group%2Fone", "", groupBody},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.Method != test.method || r.URL.EscapedPath() != test.wantPath || r.URL.Query().Encode() != test.wantQuery || string(body) != test.wantBody {
					t.Errorf("request=%s %s?%s body=%s", r.Method, r.URL.EscapedPath(), r.URL.RawQuery, body)
				}
				_, _ = w.Write([]byte("ok"))
			}))
			defer controllerServer.Close()
			engine := testEngine(t, controllerServer)
			request := httptest.NewRequest(test.method, test.route, strings.NewReader(test.body))
			request.Header.Set("Token", "token")
			if test.name == "import transaction" {
				request.Header.Set("X-Transaction-Id", "tx-one")
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "ok" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func performGroupRequest(engine http.Handler, route string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, route, nil)
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
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
	engine.POST("/group/export", handler.Export("local"))
	engine.POST("/group/export-fed", handler.Export("fed"))
	engine.POST("/group/import", handler.Import("local"))
	engine.POST("/group/import-fed", handler.Import("fed"))
	engine.GET("/group-list", handler.GetGroupList)
	engine.GET("/group", handler.GetGroups)
	engine.GET("/group/custom_check", handler.GetCustomCheck)
	engine.PATCH("/group/custom_check", handler.UpdateCustomCheck)
	engine.POST("/group", handler.CreateGroup)
	engine.PATCH("/group", handler.UpdateGroup)
	engine.DELETE("/group", handler.DeleteGroup)
	engine.GET("/service", handler.GetService)
	engine.PATCH("/service", handler.UpdateService)
	engine.POST("/service", handler.CreateService)
	engine.PATCH("/service/all", handler.UpdateSystemRequest)
	engine.GET("/processProfile", handler.GetProcessProfile)
	engine.PATCH("/processProfile", handler.UpdateProcessProfile)
	engine.GET("/fileProfile", handler.GetFileProfile)
	engine.PATCH("/fileProfile", handler.UpdateFileProfile)
	engine.GET("/filePreProfile", handler.GetPredefinedFileProfile)
	engine.GET("/dlp/sensor", handler.GetDlpSensor)
	engine.POST("/dlp/sensor", handler.CreateDlpSensor)
	engine.PATCH("/dlp/sensor", handler.UpdateDlpSensor)
	engine.DELETE("/dlp/sensor", handler.DeleteDlpSensor)
	engine.POST("/dlp/sensor/export", handler.ExportDlpSensor("local"))
	engine.POST("/dlp/sensor/export-fed", handler.ExportDlpSensor("fed"))
	engine.POST("/dlp/sensor/import", handler.ImportDlpSensor("local"))
	engine.POST("/dlp/sensor/import-fed", handler.ImportDlpSensor("fed"))
	engine.GET("/dlp/group", handler.GetDlpGroup)
	engine.PATCH("/dlp/group", handler.UpdateDlpGroup)
	engine.GET("/waf/sensor", handler.GetWafSensor)
	engine.POST("/waf/sensor", handler.CreateWafSensor)
	engine.PATCH("/waf/sensor", handler.UpdateWafSensor)
	engine.DELETE("/waf/sensor", handler.DeleteWafSensor)
	engine.POST("/waf/sensor/export", handler.ExportWafSensor("local"))
	engine.POST("/waf/sensor/export-fed", handler.ExportWafSensor("fed"))
	engine.POST("/waf/sensor/import", handler.ImportWafSensor("local"))
	engine.POST("/waf/sensor/import-fed", handler.ImportWafSensor("fed"))
	engine.GET("/waf/group", handler.GetWafGroup)
	engine.PATCH("/waf/group", handler.UpdateWafGroup)
	return engine
}
