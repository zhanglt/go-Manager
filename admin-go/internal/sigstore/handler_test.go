package sigstore

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

func TestSigstoreRoutes(t *testing.T) {
	rootInput := `{"name":"root/one","comment":null,"is_private":false,"rootless_keypairs_only":true,"rekor_public_key":null,"root_cert":"fixture-cert","sct_public_key":null,"verifiers":null,"cfg_type":"user_created"}`
	rootBody := `{"name":"root/one","is_private":false,"rootless_keypairs_only":true,"root_cert":"fixture-cert","cfg_type":"user_created"}`
	verifierInput := `{"name":"verifier/one","root_of_trust_name":"root/one","comment":null,"is_private":false,"verifier_type":"keypair","ignore_tlog":true,"ignore_sct":false,"public_key":"fixture-key","cert_issuer":null,"cert_subject":null}`
	verifierBody := `{"name":"verifier/one","root_of_trust_name":"root/one","is_private":false,"verifier_type":"keypair","ignore_tlog":true,"ignore_sct":false,"public_key":"fixture-key"}`
	tests := []struct {
		name, method, manager, body, wantPath, wantBody string
	}{
		{"list roots", http.MethodGet, "/sigstore", "", "/v1/fed/cluster/member-one/v1/scan/sigstore/root_of_trust", ""},
		{"create root", http.MethodPost, "/sigstore", rootInput, "/v1/fed/cluster/member-one/v1/scan/sigstore/root_of_trust", rootBody},
		{"update root", http.MethodPatch, "/sigstore", rootInput, "/v1/fed/cluster/member-one/v1/scan/sigstore/root_of_trust/root%2Fone", rootBody},
		{"delete root", http.MethodDelete, "/sigstore?rootOfTrustName=root%2Fone", "", "/v1/fed/cluster/member-one/v1/scan/sigstore/root_of_trust/root%2Fone", ""},
		{"list verifiers", http.MethodGet, "/verifier?rootOfTrustName=root%2Fone", "", "/v1/fed/cluster/member-one/v1/scan/sigstore/root_of_trust/root%2Fone/verifier", ""},
		{"create verifier", http.MethodPost, "/verifier", verifierInput, "/v1/fed/cluster/member-one/v1/scan/sigstore/root_of_trust/root%2Fone/verifier", verifierBody},
		{"update verifier", http.MethodPatch, "/verifier", verifierInput, "/v1/fed/cluster/member-one/v1/scan/sigstore/root_of_trust/root%2Fone/verifier/verifier%2Fone", verifierBody},
		{"delete verifier", http.MethodDelete, "/verifier?rootOfTrustName=root%2Fone&verifierName=verifier%2Fone", "", "/v1/fed/cluster/member-one/v1/scan/sigstore/root_of_trust/root%2Fone/verifier/verifier%2Fone", ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controllerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() != test.wantPath {
					t.Errorf("path = %q, want %q", r.URL.EscapedPath(), test.wantPath)
				}
				body, _ := io.ReadAll(r.Body)
				if string(body) != test.wantBody {
					t.Errorf("body = %q, want %q", body, test.wantBody)
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
			request := httptest.NewRequest(test.method, test.manager, strings.NewReader(test.body))
			request.Header.Set("Token", "token")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Header().Get("X-Fixture") != "forwarded" || response.Body.String() != `{"status":"ok"}` {
				t.Fatalf("response = %d %v %s", response.Code, response.Header(), response.Body.String())
			}
		})
	}
}

func TestSigstoreValidation(t *testing.T) {
	controllerServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("Controller must not be called")
	}))
	defer controllerServer.Close()
	engine, _ := testEngine(t, controllerServer)
	for _, test := range []struct{ method, path, body string }{
		{http.MethodPatch, "/sigstore", `{}`},
		{http.MethodDelete, "/sigstore", ""},
		{http.MethodGet, "/verifier", ""},
		{http.MethodPost, "/verifier", `{}`},
		{http.MethodPatch, "/verifier", `not-json`},
		{http.MethodDelete, "/verifier?rootOfTrustName=root", ""},
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
	handler := NewHandler(
		controller.NewWithHTTPClient(baseURL, controllerServer.Client()),
		controller.NewTargetResolver(baseURL, store), store,
	)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/sigstore", handler.GetRoots)
	engine.POST("/sigstore", handler.CreateRoot)
	engine.PATCH("/sigstore", handler.UpdateRoot)
	engine.DELETE("/sigstore", handler.DeleteRoot)
	engine.GET("/verifier", handler.GetVerifiers)
	engine.POST("/verifier", handler.CreateVerifier)
	engine.PATCH("/verifier", handler.UpdateVerifier)
	engine.DELETE("/verifier", handler.DeleteVerifier)
	return engine, store
}
