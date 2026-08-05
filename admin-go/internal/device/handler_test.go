package device

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/session"
)

func TestDebugLogRestrictsFilesToAuthenticatedToken(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/v1/fed/cluster/member-one/v1/auth" {
			t.Errorf("request=%s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer controllerServer.Close()
	baseURL, _ := url.Parse(controllerServer.URL + "/v1")
	store := session.NewStore(4)
	store.SetCluster("token", "member-one")
	handler := NewHandler(controller.NewWithHTTPClient(baseURL, controllerServer.Client()), controller.NewTargetResolver(baseURL, store), store)
	temp := t.TempDir()
	handler.tempDir = temp
	handler.supportCmd = supportTestCommand("success")
	engine := gin.New()
	engine.POST("/file/debug", handler.CreateDebugLog)
	engine.GET("/file/debug/check", handler.CheckDebugLog)
	engine.GET("/file/debug", handler.GetDebugLog)
	start := httptest.NewRecorder()
	startRequest := httptest.NewRequest(http.MethodPost, "/file/debug", nil)
	startRequest.Header.Set("Token", "token")
	engine.ServeHTTP(start, startRequest)
	if start.Code != http.StatusAccepted {
		t.Fatalf("start=%d %s", start.Code, start.Body.String())
	}
	file, found := handler.debugFile("token")
	if !found {
		t.Fatal("debug file authorization was not stored")
	}
	if filepath.Dir(file.Path) != temp {
		t.Fatalf("path=%s", file.Path)
	}
	job, _ := handler.debugJob("token")
	<-job.done
	info, err := os.Stat(file.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("support output mode = %v", info.Mode().Perm())
	}
	check := httptest.NewRecorder()
	checkRequest := httptest.NewRequest(http.MethodGet, "/file/debug/check", nil)
	checkRequest.Header.Set("Token", "token")
	engine.ServeHTTP(check, checkRequest)
	if check.Code != http.StatusOK || check.Body.String() != "Ready" || check.Header().Get("Content-Type") != "text/plain; charset=UTF-8" {
		t.Fatalf("check=%d %s", check.Code, check.Body.String())
	}
	download := httptest.NewRecorder()
	downloadRequest := httptest.NewRequest(http.MethodGet, "/file/debug", nil)
	downloadRequest.Header.Set("Token", "token")
	engine.ServeHTTP(download, downloadRequest)
	reader, err := gzip.NewReader(download.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(reader)
	if download.Code != http.StatusOK || string(body) != "fixture-gzip" || download.Header().Get("Content-Disposition") != `inline; filename="debug.gz"` {
		t.Fatalf("download=%d headers=%v body=%s", download.Code, download.Header(), download.Body.String())
	}
	if _, err := os.Stat(file.Path); !os.IsNotExist(err) {
		t.Fatalf("file persists after download: %v", err)
	}
}

func supportTestCommand(mode string) func(context.Context, string, ...string) *exec.Cmd {
	return func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		output := ""
		for index := range args {
			if args[index] == "-o" && index+1 < len(args) {
				output = args[index+1]
			}
		}
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=TestSupportHelperProcess", "--", mode, output)
		command.Env = append(os.Environ(), "GO_WANT_SUPPORT_HELPER=1")
		return command
	}
}

func TestSupportHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_SUPPORT_HELPER") != "1" {
		return
	}
	separator := 0
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	mode, output := os.Args[separator+1], os.Args[separator+2]
	switch mode {
	case "success":
		file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(2)
		}
		writer := gzip.NewWriter(file)
		_, _ = writer.Write([]byte("fixture-gzip"))
		_ = writer.Close()
		_ = file.Close()
		os.Exit(0)
	case "failure":
		os.Exit(3)
	case "corrupt":
		_ = os.WriteFile(output, []byte("not-gzip"), 0600)
		os.Exit(0)
	case "insecure":
		_ = os.WriteFile(output, []byte("not-private"), 0644)
		_ = os.Chmod(output, 0644)
		os.Exit(0)
	case "wait":
		for {
			time.Sleep(time.Second)
		}
	default:
		os.Exit(4)
	}
}

func TestDeviceReadRoutes(t *testing.T) {
	tests := []struct {
		name      string
		manager   string
		wantPath  string
		wantQuery url.Values
	}{
		{"enforcers", "/enforcer", "/v1/fed/cluster/member-one/v1/enforcer", nil},
		{"enforcer stats", "/enforcer?id=enforcer%2Fone", "/v1/fed/cluster/member-one/v1/enforcer/enforcer%2Fone/stats", nil},
		{"single enforcer", "/single-enforcer?id=enforcer%2Fone", "/v1/fed/cluster/member-one/v1/enforcer/enforcer%2Fone", nil},
		{"controllers", "/controller", "/v1/fed/cluster/member-one/v1/controller", nil},
		{"controller stats", "/controller?id=controller%2Fone", "/v1/fed/cluster/member-one/v1/controller/controller%2Fone/stats", nil},
		{"scanners", "/scanner", "/v1/fed/cluster/member-one/v1/scan/scanner", nil},
		{"summary", "/summary", "/v1/fed/cluster/member-one/v1/system/summary", nil},
		{"IBM setup", "/ibmsa_setup", "/v1/partner/ibm_sa_ep", nil},
		{"usage", "/usage", "/v1/system/usage", nil},
		{"hosts", "/host", "/v1/fed/cluster/member-one/v1/host", nil},
		{"host", "/host?id=host%2Fone", "/v1/fed/cluster/member-one/v1/host/host%2Fone", nil},
		{"host workloads", "/host/workload?id=host%2Fone", "/v1/fed/cluster/member-one/v1/workload", url.Values{"view": {"pod"}, "f_host_id": {"host/one"}}},
		{"host compliance", "/host/compliance?id=host%2Fone", "/v1/fed/cluster/member-one/v1/host/host%2Fone/compliance", nil},
		{"all file config", "/file/config?id=all", "/v1/fed/cluster/member-one/v1/file/config", url.Values{"raw": {"true"}}},
		{"policy file config", "/file/config?id=policy", "/v1/fed/cluster/member-one/v1/file/config", url.Values{"raw": {"true"}, "section": {"policy"}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() != test.wantPath {
					t.Errorf("escaped path = %q, want %q", r.URL.EscapedPath(), test.wantPath)
				}
				if r.URL.Query().Encode() != test.wantQuery.Encode() {
					t.Errorf("query = %q, want %q", r.URL.Query().Encode(), test.wantQuery.Encode())
				}
				if r.Header.Get("X-Auth-Token") != "token" || r.Header.Get("X-R-Sess") != "cookie" {
					t.Errorf("unexpected auth headers: %v", r.Header)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Fixture", "forwarded")
				_, _ = w.Write([]byte(`{"status":"ok"}`))
			}))
			defer controllerServer.Close()
			engine, store := testEngine(t, controllerServer)
			store.Put("token", "cookie")
			store.SetCluster("token", "member-one")

			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.manager, nil)
			request.Header.Set("Token", "token")
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK ||
				response.Header().Get("X-Fixture") != "forwarded" ||
				response.Header().Get("Content-Length") != "15" ||
				response.Body.String() != `{"status":"ok"}` {
				t.Fatalf("response = %d %v %s", response.Code, response.Header(), response.Body.String())
			}
		})
	}
}

func TestBenchAndCSPRoutes(t *testing.T) {
	tests := []struct{ name, method, manager, body, wantPath string }{
		{"get docker", http.MethodGet, "/bench/docker?id=host%2Fone", "", "/v1/fed/cluster/member-one/v1/bench/host/host%2Fone/docker"},
		{"post docker", http.MethodPost, "/bench/docker", "host/one", "/v1/fed/cluster/member-one/v1/bench/host/host%2Fone/docker"},
		{"get kubernetes", http.MethodGet, "/bench/kubernetes?id=host%2Fone", "", "/v1/fed/cluster/member-one/v1/bench/host/host%2Fone/kubernetes"},
		{"post kubernetes", http.MethodPost, "/bench/kubernetes", "host/one", "/v1/fed/cluster/member-one/v1/bench/host/host%2Fone/kubernetes"},
		{"csp support", http.MethodPost, "/csp-support", "", "/v1/fed/cluster/member-one/v1/csp/file/support"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.Method != test.method || r.URL.EscapedPath() != test.wantPath || len(body) != 0 {
					t.Errorf("request=%s %s body=%q", r.Method, r.URL.EscapedPath(), body)
				}
				_, _ = w.Write([]byte("ok"))
			}))
			defer controllerServer.Close()
			engine, store := testEngine(t, controllerServer)
			store.SetCluster("token", "member-one")
			request := httptest.NewRequest(test.method, test.manager, strings.NewReader(test.body))
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "ok" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestExportFedSystemConfig(t *testing.T) {
	tests := []struct {
		name, input, wantBody string
	}{
		{"remote repository", `{"remote_export_options":{"remote_repository_nickname":"backup","file_path":"nightly/config.yaml","comment":null}}`, `{"remote_export_options":{"remote_repository_nickname":"backup","file_path":"nightly/config.yaml"}}`},
		{"local download", `{}`, `{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.EscapedPath() != "/v1/fed/cluster/member-one/v1/file/fed_config" {
					t.Errorf("target = %s %s", r.Method, r.URL.EscapedPath())
				}
				body, _ := io.ReadAll(r.Body)
				if string(body) != test.wantBody {
					t.Errorf("body = %s, want %s", body, test.wantBody)
				}
				_, _ = w.Write([]byte("exported"))
			}))
			defer controllerServer.Close()
			engine, store := testEngine(t, controllerServer)
			store.SetCluster("token", "member-one")
			request := httptest.NewRequest(http.MethodPost, "/file/export-config-fed", strings.NewReader(test.input))
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "exported" {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestCreateFileConfigTransactionRetriesWithTemporaryToken(t *testing.T) {
	var calls int
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.EscapedPath() != "/v1/fed/cluster/member-one/v1/file/config" || r.Header.Get("X-Transaction-Id") != "tx-one" || r.Header.Get("X-As-Standalone") != "true" {
			t.Errorf("unexpected request target or headers: %s %v", r.URL.EscapedPath(), r.Header)
		}
		wantToken := "token"
		if calls == 2 {
			wantToken = "temporary-token"
		}
		if r.Header.Get("X-Auth-Token") != wantToken {
			t.Errorf("call %d token = %q, want %q", calls, r.Header.Get("X-Auth-Token"), wantToken)
		}
		if calls == 1 {
			w.WriteHeader(http.StatusRequestTimeout)
			return
		}
		_, _ = w.Write([]byte("imported"))
	}))
	defer controllerServer.Close()
	engine, store := testEngine(t, controllerServer)
	store.Put("token", "cookie")
	store.SetCluster("token", "member-one")
	request := httptest.NewRequest(http.MethodPost, "/file/config", strings.NewReader("temporary-token"))
	request.Header.Set("Token", "token")
	request.Header.Set("X-Transaction-Id", "tx-one")
	request.Header.Set("X-As-Standalone", "true")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if calls != 2 || response.Code != http.StatusOK || response.Body.String() != "imported" {
		t.Fatalf("calls=%d response=%d %s", calls, response.Code, response.Body.String())
	}
}

func TestCreateFileConfigStreamsMultipart(t *testing.T) {
	const multipartBody = "--fixture\r\nContent-Disposition: form-data; name=\"file\"; filename=\"config.yaml\"\r\n\r\ncontent\r\n--fixture--\r\n"
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != multipartBody || r.Header.Get("Content-Type") != "multipart/form-data; boundary=fixture" || r.Header.Get("X-As-Standalone") != "false" {
			t.Errorf("unexpected multipart request: headers=%v body=%q", r.Header, body)
		}
		if r.ContentLength != int64(len(multipartBody)) || len(r.TransferEncoding) != 0 {
			t.Errorf("content length=%d transfer encoding=%v", r.ContentLength, r.TransferEncoding)
		}
		if _, present := r.Header["X-Transaction-Id"]; present {
			t.Error("multipart branch must not send X-Transaction-Id")
		}
		_, _ = w.Write([]byte("uploaded"))
	}))
	defer controllerServer.Close()
	engine, store := testEngine(t, controllerServer)
	store.SetCluster("token", "member-one")
	request := httptest.NewRequest(http.MethodPost, "/file/config", strings.NewReader(multipartBody))
	request.Header.Set("Token", "token")
	request.Header.Set("X-As-Standalone", "false")
	request.Header.Set("Content-Type", "multipart/form-data; boundary=fixture")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "uploaded" {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

func TestImportFedSystemConfig(t *testing.T) {
	tests := []struct {
		name, input, transactionID, wantBody string
	}{
		{"transaction", "ignored", "tx-fed", ""},
		{"form data", "--fixture\nContent-Disposition: form-data; name=\"file\"\nContent-Type: application/octet-stream\n\nline-one\nline-two\n--fixture--\n", "", "line-one\nline-two"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.URL.EscapedPath() != "/v1/fed/cluster/member-one/v1/file/config" || r.URL.Query().Get("scope") != "fed" || string(body) != test.wantBody {
					t.Errorf("target=%s?%s body=%q", r.URL.EscapedPath(), r.URL.RawQuery, body)
				}
				if r.Header.Get("X-Transaction-Id") != test.transactionID {
					t.Errorf("transaction header=%q, want %q", r.Header.Get("X-Transaction-Id"), test.transactionID)
				}
				_, _ = w.Write([]byte("fed-imported"))
			}))
			defer controllerServer.Close()
			engine, store := testEngine(t, controllerServer)
			store.SetCluster("token", "member-one")
			request := httptest.NewRequest(http.MethodPost, "/file/config-fed", strings.NewReader(test.input))
			request.Header.Set("Token", "token")
			if test.transactionID != "" {
				request.Header.Set("X-Transaction-Id", test.transactionID)
			}
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != "fed-imported" {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestDeviceConfigRoutes(t *testing.T) {
	webhookBody := `{"name":"alerts","url":"https://hooks.example.invalid","enable":true,"use_proxy":false,"type":"slack","cfg_type":"local"}`
	federalWebhookBody := `{"name":"fed-alerts","url":"https://hooks.example.invalid","enable":true,"use_proxy":false,"type":"slack","cfg_type":"federal"}`
	repository := `{"nickname":"repo-one","provider":"github","comment":null,"enable":true,"github_configuration":{"repository_owner_username":"owner","repository_name":"repo","repository_branch_name":null,"personal_access_token":null,"personal_access_token_committer_name":null,"personal_access_token_email":null},"azure_devops_configuration":null}`
	tests := []struct {
		name, method, manager, body, wantPath, wantBody string
		wantQuery                                       url.Values
		wantSource                                      *string
	}{
		{"create webhook", http.MethodPost, "/webhook", webhookBody, "/v1/fed/cluster/member-one/v1/system/config/webhook", `{"config":` + webhookBody + `}`, nil, nil},
		{"create federal webhook", http.MethodPost, "/webhook", federalWebhookBody, "/v1/system/config/webhook", `{"config":` + federalWebhookBody + `}`, nil, nil},
		{"update webhook", http.MethodPatch, "/webhook?scope=fed", webhookBody, "/v1/system/config/webhook/alerts", `{"config":` + webhookBody + `}`, url.Values{"scope": {"fed"}}, nil},
		{"delete webhook", http.MethodDelete, "/webhook?name=alerts&scope=local", "", "/v1/fed/cluster/member-one/v1/system/config/webhook/alerts", "", url.Values{"scope": {"local"}}, nil},
		{"get config", http.MethodGet, "/config?scope=fed", "", "/v1/system/config", "", url.Values{"scope": {"fed"}}, nil},
		{"update config", http.MethodPatch, "/config", `{"cluster_name":"cluster","syslog_ip":null}`, "/v1/fed/cluster/member-one/v1/system/config", `{"config":{"cluster_name":"cluster"}}`, nil, nil},
		{"update fed config", http.MethodPatch, "/config?scope=fed", `{"cluster_name":"cluster","syslog_ip":null}`, "/v1/fed/cluster/member-one/v1/system/config", `{"fed_config":{"cluster_name":"cluster"}}`, url.Values{"scope": {"fed"}}, nil},
		{"get config v2", http.MethodGet, "/config-v2?scope=fed&source=settings", "", "/v1/fed/cluster/member-one/v2/system/config", "", url.Values{"scope": {"fed"}}, stringPointer("settings")},
		{"get config v2 empty source", http.MethodGet, "/config-v2", "", "/v1/fed/cluster/member-one/v2/system/config", "", nil, stringPointer("")},
		{"update config v2", http.MethodPatch, "/config-v2", `{"config":null,"config_v2":{"misc_cfg":{"cluster_name":"cluster","csp_type":null}},"fed_config":null,"net_config":null,"atmo_config":null}`, "/v1/fed/cluster/member-one/v2/system/config", `{"config_v2":{"misc_cfg":{"cluster_name":"cluster"}}}`, nil, nil},
		{"create repository", http.MethodPost, "/remote_repository", repository, "/v1/fed/cluster/member-one/v1/system/config/remote_repository", `{"nickname":"repo-one","provider":"github","enable":true,"github_configuration":{"repository_owner_username":"owner","repository_name":"repo"}}`, nil, nil},
		{"update repository", http.MethodPatch, "/remote_repository", `{"config":` + repository + `}`, "/v1/fed/cluster/member-one/v1/system/config/remote_repository/repo-one", `{"config":{"nickname":"repo-one","provider":"github","enable":true,"github_configuration":{"repository_owner_username":"owner","repository_name":"repo"}}}`, nil, nil},
		{"delete repository", http.MethodDelete, "/remote_repository?name=repo-one", "", "/v1/fed/cluster/member-one/v1/system/config/remote_repository/repo-one", "", nil, nil},
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
				if test.wantSource != nil {
					values, present := r.Header["X-Nv-Page"]
					if !present || len(values) != 1 || values[0] != *test.wantSource {
						t.Errorf("X-Nv-Page = %v, present=%v, want %q", values, present, *test.wantSource)
					}
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

func TestHostScanReport(t *testing.T) {
	wantBody := `{"cursor":{"host_name":"host-one"},"filters":[{"name":"severity","op":"eq","value":["high"]}],"max_cve_records":25,"show_accepted":true,"vul_score_filter":{"score_bottom":4,"score_version":"v3"}}`
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/v1/fed/cluster/member-one/v1/scan/hosts/scan_report" {
			t.Errorf("target = %s %s", r.Method, r.URL.EscapedPath())
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != wantBody {
			t.Errorf("body = %s, want %s", body, wantBody)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"report":"ok"}`))
	}))
	defer controllerServer.Close()
	engine, store := testEngine(t, controllerServer)
	store.SetCluster("token", "member-one")
	input := `{"show_accepted":true,"max_cve_records":25,"cursor":{"host_name":"host-one"},"vul_score_filter":{"score_version":"v3","score_bottom":4},"filters":[{"name":"severity","op":"eq","value":["high"]}]}`
	request := httptest.NewRequest(http.MethodPost, "/host/scan-report", strings.NewReader(input))
	request.Header.Set("Token", "token")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != `{"report":"ok"}` {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestDeviceConfigValidation(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("Controller must not be called")
	}))
	defer controllerServer.Close()
	engine, _ := testEngine(t, controllerServer)
	for _, test := range []struct{ method, path, body string }{
		{http.MethodDelete, "/webhook", ""},
		{http.MethodPatch, "/config", "not-json"},
		{http.MethodPatch, "/remote_repository", `{"config":{}}`},
		{http.MethodDelete, "/remote_repository", ""},
		{http.MethodGet, "/file/config", ""},
		{http.MethodPost, "/file/export-config-fed", "not-json"},
		{http.MethodPost, "/file/config", "not-multipart"},
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

func stringPointer(value string) *string { return &value }

func TestDeviceRequiredQueries(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("Controller must not be called")
	}))
	defer controllerServer.Close()
	engine, _ := testEngine(t, controllerServer)
	for _, path := range []string{"/single-enforcer", "/host/workload", "/host/compliance"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Token", "token")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d", path, response.Code)
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
	handler := NewHandler(
		controller.NewWithHTTPClient(baseURL, controllerServer.Client()),
		controller.NewTargetResolver(baseURL, store), store,
	)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/enforcer", handler.GetEnforcers)
	engine.GET("/single-enforcer", handler.GetEnforcer)
	engine.GET("/controller", handler.GetControllers)
	engine.GET("/scanner", handler.GetScanners)
	engine.GET("/summary", handler.GetSummary)
	engine.GET("/ibmsa_setup", handler.GetIBMSetup)
	engine.GET("/usage", handler.GetUsage)
	engine.GET("/host", handler.GetHosts)
	engine.GET("/host/workload", handler.GetHostWorkloads)
	engine.GET("/host/compliance", handler.GetHostCompliance)
	engine.POST("/host/scan-report", handler.GetHostScanReport)
	engine.GET("/bench/docker", handler.GetDockerBench)
	engine.POST("/bench/docker", handler.CreateDockerBench)
	engine.GET("/bench/kubernetes", handler.GetKubernetesBench)
	engine.POST("/bench/kubernetes", handler.CreateKubernetesBench)
	engine.POST("/csp-support", handler.DownloadCSPFile)
	engine.GET("/file/config", handler.GetFileConfig)
	engine.POST("/file/config", handler.CreateFileConfig)
	engine.POST("/file/config-fed", handler.ImportFedSystemConfig)
	engine.POST("/file/export-config-fed", handler.ExportFedSystemConfig)
	engine.POST("/webhook", handler.CreateWebhook)
	engine.PATCH("/webhook", handler.UpdateWebhook)
	engine.DELETE("/webhook", handler.DeleteWebhook)
	engine.GET("/config", handler.GetConfig)
	engine.PATCH("/config", handler.UpdateConfig)
	engine.GET("/config-v2", handler.GetConfigV2)
	engine.PATCH("/config-v2", handler.UpdateConfigV2)
	engine.POST("/remote_repository", handler.CreateRemoteRepository)
	engine.PATCH("/remote_repository", handler.UpdateRemoteRepository)
	engine.DELETE("/remote_repository", handler.DeleteRemoteRepository)
	return engine, store
}
