package device

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/session"
)

func TestDebugLogFailureModesCleanOutput(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		prepare func(*Handler)
	}{
		{name: "non-zero exit", mode: "failure"},
		{name: "invalid gzip", mode: "corrupt"},
		{name: "insecure mode", mode: "insecure"},
		{name: "oversized file", mode: "success", prepare: func(handler *Handler) { handler.maxDebugFileBytes = 8 }},
		{name: "timeout", mode: "wait", prepare: func(handler *Handler) { handler.supportTimeout = 30 * time.Millisecond }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, engine := newDebugFixture(t)
			handler.supportCmd = supportTestCommand(test.mode)
			if test.prepare != nil {
				test.prepare(handler)
			}
			response := debugRequest(engine, http.MethodPost, "/file/debug", "token")
			if response.Code != http.StatusAccepted {
				t.Fatalf("create = %d %s", response.Code, response.Body.String())
			}
			job, found := handler.debugJob("token")
			if !found {
				t.Fatal("support job was not registered")
			}
			select {
			case <-job.done:
			case <-time.After(3 * time.Second):
				t.Fatal("support job did not terminate")
			}
			if _, err := os.Stat(job.path); !os.IsNotExist(err) {
				t.Fatalf("failed output remains: %v", err)
			}
			check := debugRequest(engine, http.MethodGet, "/file/debug/check", "token")
			if check.Code != http.StatusInternalServerError {
				t.Fatalf("check = %d %s", check.Code, check.Body.String())
			}
			_ = handler.Close()
		})
	}
}

func TestDebugLogStartFailureCleansOutput(t *testing.T) {
	handler, engine := newDebugFixture(t)
	handler.supportCmd = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, filepath.Join(t.TempDir(), "missing-command"))
	}
	response := debugRequest(engine, http.MethodPost, "/file/debug", "token")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}
	job, found := handler.debugJob("token")
	if !found {
		t.Fatal("failed job status was not retained")
	}
	<-job.done
	if _, err := os.Stat(job.path); !os.IsNotExist(err) {
		t.Fatalf("reserved output remains: %v", err)
	}
	_ = handler.Close()
}

func TestDebugLogTemporaryPathFailure(t *testing.T) {
	handler, engine := newDebugFixture(t)
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	handler.tempDir = filepath.Join(parent, "output")
	response := debugRequest(engine, http.MethodPost, "/file/debug", "token")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("create = %d %s", response.Code, response.Body.String())
	}
	if _, found := handler.debugJob("token"); found {
		t.Fatal("job registered after temporary path failure")
	}
	_ = handler.Close()
}

func TestDebugLogInterruptedDownloadRemovesOutput(t *testing.T) {
	handler, engine := newDebugFixture(t)
	handler.supportCmd = supportTestCommand("success")
	response := debugRequest(engine, http.MethodPost, "/file/debug", "token")
	if response.Code != http.StatusAccepted {
		t.Fatalf("create = %d", response.Code)
	}
	job, _ := handler.debugJob("token")
	<-job.done
	request := httptest.NewRequest(http.MethodGet, "/file/debug", nil)
	request.Header.Set("Token", "token")
	writer := &failingResponseWriter{header: make(http.Header)}
	engine.ServeHTTP(writer, request)
	if _, err := os.Stat(job.path); !os.IsNotExist(err) {
		t.Fatalf("output remains after interrupted download: %v", err)
	}
	if _, found := handler.debugJob("token"); found {
		t.Fatal("authorization remains after interrupted download")
	}
	_ = handler.Close()
}

func TestDebugLogResultExpiresAndIsRemoved(t *testing.T) {
	handler, engine := newDebugFixture(t)
	handler.supportCmd = supportTestCommand("success")
	handler.debugFileTTL = 20 * time.Millisecond
	response := debugRequest(engine, http.MethodPost, "/file/debug", "token")
	if response.Code != http.StatusAccepted {
		t.Fatalf("create = %d", response.Code)
	}
	job, _ := handler.debugJob("token")
	<-job.done
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(job.path); os.IsNotExist(err) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(job.path); !os.IsNotExist(err) {
		t.Fatalf("expired output remains: %v", err)
	}
	if _, found := handler.debugJob("token"); found {
		t.Fatal("expired job authorization remains")
	}
	_ = handler.Close()
}

func TestDebugLogConcurrencyLimit(t *testing.T) {
	handler, engine := newDebugFixture(t)
	handler.supportSlots = make(chan struct{}, 1)
	handler.supportCmd = supportTestCommand("wait")
	first := debugRequest(engine, http.MethodPost, "/file/debug", "token")
	if first.Code != http.StatusAccepted {
		t.Fatalf("first create = %d", first.Code)
	}
	second := debugRequest(engine, http.MethodPost, "/file/debug", "other-token")
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second create = %d %s", second.Code, second.Body.String())
	}
	if err := handler.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDebugLogCredentialsAreNotCommandArguments(t *testing.T) {
	handler, engine := newDebugFixture(t)
	var command *exec.Cmd
	handler.supportCmd = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		command = supportTestCommand("wait")(ctx, name, args...)
		return command
	}
	response := debugRequest(engine, http.MethodPost, "/file/debug", "token")
	if response.Code != http.StatusAccepted {
		t.Fatalf("create = %d", response.Code)
	}
	arguments := strings.Join(command.Args, " ")
	if strings.Contains(arguments, "token") || strings.Contains(arguments, "session-cookie") {
		t.Fatalf("credentials leaked into argv: %q", arguments)
	}
	environment := strings.Join(command.Env, "\n")
	if !strings.Contains(environment, "NV_SUPPORT_TOKEN=token") || !strings.Contains(environment, "NV_SUPPORT_RESS=session-cookie") {
		t.Fatal("support credentials were not provided through the child environment")
	}
	_ = handler.Close()
}

func TestDebugLogReplacementCancelsPreviousJob(t *testing.T) {
	handler, engine := newDebugFixture(t)
	handler.supportSlots = make(chan struct{}, 1)
	var calls atomic.Int32
	handler.supportCmd = func(ctx context.Context, command string, args ...string) *exec.Cmd {
		if calls.Add(1) == 1 {
			return supportTestCommand("wait")(ctx, command, args...)
		}
		return supportTestCommand("success")(ctx, command, args...)
	}
	first := debugRequest(engine, http.MethodPost, "/file/debug", "token")
	if first.Code != http.StatusAccepted {
		t.Fatalf("first create = %d", first.Code)
	}
	firstJob, _ := handler.debugJob("token")
	second := debugRequest(engine, http.MethodPost, "/file/debug", "token")
	if second.Code != http.StatusAccepted {
		t.Fatalf("replacement create = %d %s", second.Code, second.Body.String())
	}
	select {
	case <-firstJob.done:
	case <-time.After(3 * time.Second):
		t.Fatal("replaced job did not terminate")
	}
	if _, err := os.Stat(firstJob.path); !os.IsNotExist(err) {
		t.Fatalf("replaced output remains: %v", err)
	}
	secondJob, _ := handler.debugJob("token")
	<-secondJob.done
	if secondJob.err != nil {
		t.Fatalf("replacement job failed: %v", secondJob.err)
	}
	_ = handler.Close()
}

func TestDebugLogCloseCancelsJobAndRemovesOutput(t *testing.T) {
	handler, engine := newDebugFixture(t)
	handler.supportCmd = supportTestCommand("wait")
	response := debugRequest(engine, http.MethodPost, "/file/debug", "token")
	if response.Code != http.StatusAccepted {
		t.Fatalf("create = %d", response.Code)
	}
	job, _ := handler.debugJob("token")
	if err := handler.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-job.done:
	default:
		t.Fatal("Close returned before the support process exited")
	}
	if _, err := os.Stat(job.path); !os.IsNotExist(err) {
		t.Fatalf("output remains after Close: %v", err)
	}
}

func TestValidateSupportEnvironment(t *testing.T) {
	root := t.TempDir()
	command := filepath.Join(root, "support")
	if err := os.WriteFile(command, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	tempDir := filepath.Join(root, "output")
	options := SupportOptions{Command: command, TempDir: tempDir, Timeout: time.Minute, MaxFileBytes: 1024, MaxConcurrent: 1}
	if err := validateSupportEnvironment(options); err != nil {
		t.Fatalf("valid environment rejected: %v", err)
	}
	info, err := os.Stat(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("temporary directory mode = %v", info.Mode().Perm())
	}
	if err := os.Chmod(tempDir, 0770); err != nil {
		t.Fatal(err)
	}
	if err := validateSupportEnvironment(options); err == nil {
		t.Fatal("group-writable temporary directory was accepted")
	}
	if err := os.Chmod(tempDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(command, 0775); err != nil {
		t.Fatal(err)
	}
	if err := validateSupportEnvironment(options); err == nil {
		t.Fatal("group-writable support command was accepted")
	}
}

func newDebugFixture(t *testing.T) (*Handler, *gin.Engine) {
	t.Helper()
	controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(controllerServer.Close)
	baseURL, err := url.Parse(controllerServer.URL + "/v1")
	if err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(8)
	store.Put("token", "session-cookie")
	store.SetCluster("token", "member-one")
	handler := NewHandler(controller.NewWithHTTPClient(baseURL, controllerServer.Client()), controller.NewTargetResolver(baseURL, store), store)
	handler.tempDir = t.TempDir()
	engine := gin.New()
	engine.POST("/file/debug", handler.CreateDebugLog)
	engine.GET("/file/debug/check", handler.CheckDebugLog)
	engine.GET("/file/debug", handler.GetDebugLog)
	return handler, engine
}

func debugRequest(engine http.Handler, method, path, token string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Token", token)
	engine.ServeHTTP(response, request)
	return response
}

type failingResponseWriter struct {
	header http.Header
	status int
}

func (w *failingResponseWriter) Header() http.Header    { return w.header }
func (w *failingResponseWriter) WriteHeader(status int) { w.status = status }
func (*failingResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("client disconnected")
}
