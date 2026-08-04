package device

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/scanreport"
	"github.com/neuvector/manager/admin-go/internal/session"
)

type Handler struct {
	controller        *controller.Client
	resolver          *controller.TargetResolver
	sessions          *session.Store
	supportCmd        func(context.Context, string, ...string) *exec.Cmd
	tempDir           string
	supportCommand    string
	supportTimeout    time.Duration
	debugFileTTL      time.Duration
	maxDebugFileBytes int64
	supportSlots      chan struct{}
	lifecycle         context.Context
	cancelLifecycle   context.CancelFunc
	jobsMu            sync.Mutex
	debugJobs         map[string]*supportJob
	jobsWG            sync.WaitGroup
	closed            bool
}

type debugFile struct{ Path string }

type SupportOptions struct {
	Command       string
	TempDir       string
	Timeout       time.Duration
	MaxFileBytes  int64
	MaxConcurrent int
}

var debugEnforcerID = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

type webhook struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Enable   bool   `json:"enable"`
	UseProxy bool   `json:"use_proxy"`
	Type     string `json:"type"`
	CfgType  string `json:"cfg_type"`
}

type githubConfiguration struct {
	RepositoryOwnerUsername           *string `json:"repository_owner_username,omitempty"`
	RepositoryName                    *string `json:"repository_name,omitempty"`
	RepositoryBranchName              *string `json:"repository_branch_name,omitempty"`
	PersonalAccessToken               *string `json:"personal_access_token,omitempty"`
	PersonalAccessTokenCommitterName  *string `json:"personal_access_token_committer_name,omitempty"`
	PersonalAccessTokenCommitterEmail *string `json:"personal_access_token_email,omitempty"`
}

type azureDevOpsConfiguration struct {
	OrganizationName    *string `json:"organization_name,omitempty"`
	ProjectName         *string `json:"project_name,omitempty"`
	RepoName            *string `json:"repo_name,omitempty"`
	BranchName          *string `json:"branch_name,omitempty"`
	PersonalAccessToken *string `json:"personal_access_token,omitempty"`
}

type remoteRepository struct {
	Nickname                 string                    `json:"nickname"`
	Provider                 string                    `json:"provider"`
	Comment                  *string                   `json:"comment,omitempty"`
	Enable                   bool                      `json:"enable"`
	GitHubConfiguration      *githubConfiguration      `json:"github_configuration,omitempty"`
	AzureDevOpsConfiguration *azureDevOpsConfiguration `json:"azure_devops_configuration,omitempty"`
}

type remoteRepositoryWrap struct {
	Config remoteRepository `json:"config"`
}

type remoteExportOptions struct {
	RemoteRepositoryNickname string  `json:"remote_repository_nickname"`
	FilePath                 *string `json:"file_path,omitempty"`
	Comment                  *string `json:"comment,omitempty"`
}

type exportedFedSystemConfig struct {
	RemoteExportOptions *remoteExportOptions `json:"remote_export_options,omitempty"`
}

func NewHandler(client *controller.Client, resolver *controller.TargetResolver, sessions *session.Store) *Handler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Handler{
		controller: client, resolver: resolver, sessions: sessions,
		supportCmd: exec.CommandContext, tempDir: os.TempDir(), supportCommand: "/usr/local/bin/support",
		supportTimeout: 10 * time.Minute, debugFileTTL: defaultDebugFileTTL,
		maxDebugFileBytes: 64 << 20, supportSlots: make(chan struct{}, 2),
		lifecycle: ctx, cancelLifecycle: cancel, debugJobs: make(map[string]*supportJob),
	}
}

func NewSupportHandler(client *controller.Client, resolver *controller.TargetResolver, sessions *session.Store, options SupportOptions) (*Handler, error) {
	if err := validateSupportEnvironment(options); err != nil {
		return nil, err
	}
	h := NewHandler(client, resolver, sessions)
	h.tempDir = options.TempDir
	h.supportCommand = options.Command
	h.supportTimeout = options.Timeout
	h.maxDebugFileBytes = options.MaxFileBytes
	h.supportSlots = make(chan struct{}, options.MaxConcurrent)
	return h, nil
}

func (h *Handler) GetEnforcers(c *gin.Context) {
	segments := []string{"enforcer"}
	if id, present := c.GetQuery("id"); present {
		segments = append(segments, id, "stats")
	}
	h.proxy(c, true, nil, segments...)
}

func (h *Handler) GetEnforcer(c *gin.Context) {
	id, ok := requiredQuery(c, "id")
	if ok {
		h.proxy(c, true, nil, "enforcer", id)
	}
}

func (h *Handler) GetControllers(c *gin.Context) {
	segments := []string{"controller"}
	if id, present := c.GetQuery("id"); present {
		segments = append(segments, id, "stats")
	}
	h.proxy(c, true, nil, segments...)
}

func (h *Handler) GetScanners(c *gin.Context) {
	h.proxy(c, true, nil, "scan", "scanner")
}

func (h *Handler) GetSummary(c *gin.Context) {
	h.proxy(c, true, nil, "system", "summary")
}

func (h *Handler) GetIBMSetup(c *gin.Context) {
	h.proxy(c, false, nil, "partner", "ibm_sa_ep")
}

func (h *Handler) GetUsage(c *gin.Context) {
	h.proxy(c, false, nil, "system", "usage")
}

func (h *Handler) GetHosts(c *gin.Context) {
	segments := []string{"host"}
	if id, present := c.GetQuery("id"); present {
		segments = append(segments, id)
	}
	h.proxy(c, true, nil, segments...)
}

func (h *Handler) GetHostWorkloads(c *gin.Context) {
	id, ok := requiredQuery(c, "id")
	if !ok {
		return
	}
	query := make(url.Values)
	query.Set("view", "pod")
	query.Set("f_host_id", id)
	h.proxy(c, true, query, "workload")
}

func (h *Handler) GetHostCompliance(c *gin.Context) {
	id, ok := requiredQuery(c, "id")
	if ok {
		h.proxy(c, true, nil, "host", id, "compliance")
	}
}

func (h *Handler) GetDockerBench(c *gin.Context) {
	h.getBench(c, "docker")
}

func (h *Handler) CreateDockerBench(c *gin.Context) {
	h.createBench(c, "docker")
}

func (h *Handler) GetKubernetesBench(c *gin.Context) {
	h.getBench(c, "kubernetes")
}

func (h *Handler) CreateKubernetesBench(c *gin.Context) {
	h.createBench(c, "kubernetes")
}

func (h *Handler) DownloadCSPFile(c *gin.Context) {
	h.proxyRequest(c, http.MethodPost, true, controller.V1, nil, nil, nil, "csp", "file", "support")
}

func (h *Handler) getBench(c *gin.Context, kind string) {
	id, ok := requiredQuery(c, "id")
	if ok {
		h.proxy(c, true, nil, "bench", "host", id, kind)
	}
}

func (h *Handler) createBench(c *gin.Context, kind string) {
	id, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeBodyReadError(c, err)
		return
	}
	h.proxyRequest(c, http.MethodPost, true, controller.V1, nil, nil, nil, "bench", "host", string(id), kind)
}

func (h *Handler) GetFileConfig(c *gin.Context) {
	id, ok := requiredQuery(c, "id")
	if !ok {
		return
	}
	query := url.Values{"raw": {"true"}}
	if id != "all" {
		query.Set("section", "policy")
	}
	h.proxy(c, true, query, "file", "config")
}

func (h *Handler) ExportFedSystemConfig(c *gin.Context) {
	var input exportedFedSystemConfig
	if !decodeBody(c, &input) {
		return
	}
	body, _ := json.Marshal(input)
	h.proxyRequest(c, http.MethodPost, true, controller.V1, nil, body, nil,
		"file", "fed_config")
}

func (h *Handler) CreateFileConfig(c *gin.Context) {
	transactionID, hasTransaction := requestHeader(c.Request.Header, "X-Transaction-Id")
	asStandalone, hasStandalone := requestHeader(c.Request.Header, "X-As-Standalone")
	if !hasStandalone {
		writeBadRequest(c, "header 'X-As-Standalone' is required")
		return
	}
	if hasTransaction {
		tempToken, err := io.ReadAll(c.Request.Body)
		if err != nil {
			writeBodyReadError(c, err)
			return
		}
		response, err := h.sendFileConfig(c, c.GetHeader("Token"), &transactionID, asStandalone, nil, "text/plain; charset=UTF-8", true)
		if err != nil {
			writeInternalError(c)
			return
		}
		if response.StatusCode == http.StatusRequestTimeout {
			response.Body.Close()
			response, err = h.sendFileConfig(c, string(tempToken), &transactionID, asStandalone, nil, "text/plain; charset=UTF-8", true)
			if err != nil {
				writeInternalError(c)
				return
			}
		}
		defer response.Body.Close()
		copyResponse(c, response)
		return
	}

	contentType := c.GetHeader("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" {
		writeBadRequest(c, "multipart/form-data request body is required")
		return
	}
	response, err := h.sendFileConfig(c, c.GetHeader("Token"), nil, asStandalone, c.Request.Body, contentType, false)
	if err != nil {
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	copyResponse(c, response)
}

func (h *Handler) ImportFedSystemConfig(c *gin.Context) {
	query := url.Values{"scope": {"fed"}}
	if transactionID, present := requestHeader(c.Request.Header, "X-Transaction-Id"); present {
		headers := make(http.Header)
		headers.Set("X-Transaction-Id", transactionID)
		h.proxyRequest(c, http.MethodPost, true, controller.V1, query, []byte{}, headers,
			"file", "config")
		return
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		writeBodyReadError(c, err)
		return
	}
	lines := strings.Split(string(body), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	content := ""
	if len(lines) > 5 {
		content = strings.Join(lines[4:len(lines)-1], "\n")
	}
	h.proxyRequest(c, http.MethodPost, true, controller.V1, query, []byte(content), nil,
		"file", "config")
}

func (h *Handler) sendFileConfig(c *gin.Context, token string, transactionID *string, asStandalone string, body io.Reader, contentType string, standardHeaders bool) (*http.Response, error) {
	target, err := h.resolver.Resolve(c.GetHeader("Token"), controller.V1, "file", "config")
	if err != nil {
		return nil, err
	}
	headers := make(http.Header)
	headers.Set("X-Auth-Token", token)
	headers.Set("X-As-Standalone", asStandalone)
	headers.Set("Content-Type", contentType)
	if transactionID != nil {
		headers.Set("X-Transaction-Id", *transactionID)
	}
	if standardHeaders {
		headers.Set("Accept-Encoding", "gzip")
		headers.Set("Cache-Control", "no-cache")
	}
	if entry, ok := h.sessions.Get(token); ok {
		headers.Set("X-R-Sess", entry.SUSEToken)
	} else {
		headers.Set("X-R-Sess", "")
	}
	contentLength := int64(0)
	if body != nil {
		contentLength = c.Request.ContentLength
	}
	return h.controller.DoTargetSized(c.Request.Context(), http.MethodPost, target, body, headers, contentLength)
}

func (h *Handler) GetHostScanReport(c *gin.Context) {
	var input scanreport.Request
	if !decodeBody(c, &input) {
		return
	}
	body, _ := json.Marshal(input)
	h.proxyRequest(c, http.MethodPost, true, controller.V1, nil, body, nil,
		"scan", "hosts", "scan_report")
}

func (h *Handler) CreateWebhook(c *gin.Context) {
	var input webhook
	if !decodeBody(c, &input) {
		return
	}
	body, _ := json.Marshal(struct {
		Config webhook `json:"config"`
	}{Config: input})
	h.proxyRequest(c, http.MethodPost, input.CfgType != "federal", controller.V1, nil, body, nil,
		"system", "config", "webhook")
}

func (h *Handler) UpdateWebhook(c *gin.Context) {
	var input webhook
	if !decodeBody(c, &input) {
		return
	}
	body, _ := json.Marshal(struct {
		Config webhook `json:"config"`
	}{Config: input})
	query, clusterAware := scopeTarget(c)
	h.proxyRequest(c, http.MethodPatch, clusterAware, controller.V1, query, body, nil,
		"system", "config", "webhook", input.Name)
}

func (h *Handler) DeleteWebhook(c *gin.Context) {
	name, ok := requiredQuery(c, "name")
	if !ok {
		return
	}
	query, clusterAware := scopeTarget(c)
	h.proxyRequest(c, http.MethodDelete, clusterAware, controller.V1, query, nil, nil,
		"system", "config", "webhook", name)
}

func (h *Handler) GetConfig(c *gin.Context) {
	query, clusterAware := scopeTarget(c)
	h.proxyRequest(c, http.MethodGet, clusterAware, controller.V1, query, nil, nil, "system", "config")
}

func (h *Handler) UpdateConfig(c *gin.Context) {
	// Scala omits its standard web headers on this route only.
	clearManagerWebHeaders(c)
	value, ok := cleanJSONBody(c)
	if !ok {
		return
	}
	key := "config"
	query := url.Values(nil)
	if scope, present := c.GetQuery("scope"); present {
		key = "fed_config"
		query = url.Values{"scope": {scope}}
	}
	body, _ := json.Marshal(map[string]any{key: value})
	h.proxyRequest(c, http.MethodPatch, true, controller.V1, query, body, nil, "system", "config")
}

func clearManagerWebHeaders(c *gin.Context) {
	for _, name := range []string{
		"Cache-Control", "Content-Security-Policy", "Strict-Transport-Security",
		"X-Content-Type-Options", "X-Frame-Options", "X-Xss-Protection",
	} {
		c.Writer.Header().Del(name)
	}
}

func (h *Handler) GetConfigV2(c *gin.Context) {
	query := optionalQuery(c, "scope")
	headers := make(http.Header)
	headers.Set("X-Nv-Page", c.Query("source"))
	h.proxyRequest(c, http.MethodGet, true, controller.V2, query, nil, headers, "system", "config")
}

func (h *Handler) UpdateConfigV2(c *gin.Context) {
	value, ok := cleanJSONBody(c)
	if !ok {
		return
	}
	body, _ := json.Marshal(value)
	h.proxyRequest(c, http.MethodPatch, true, controller.V2, optionalQuery(c, "scope"), body, nil,
		"system", "config")
}

func (h *Handler) CreateRemoteRepository(c *gin.Context) {
	var input remoteRepository
	if !decodeBody(c, &input) {
		return
	}
	body, _ := json.Marshal(input)
	h.proxyRequest(c, http.MethodPost, true, controller.V1, nil, body, nil,
		"system", "config", "remote_repository")
}

func (h *Handler) UpdateRemoteRepository(c *gin.Context) {
	var input remoteRepositoryWrap
	if !decodeBody(c, &input) {
		return
	}
	name := input.Config.Nickname
	if name == "" {
		writeBadRequest(c, "remote repository config.nickname is required")
		return
	}
	body, _ := json.Marshal(input)
	h.proxyRequest(c, http.MethodPatch, true, controller.V1, nil, body, nil,
		"system", "config", "remote_repository", name)
}

func (h *Handler) DeleteRemoteRepository(c *gin.Context) {
	name, ok := requiredQuery(c, "name")
	if ok {
		h.proxyRequest(c, http.MethodDelete, true, controller.V1, nil, nil, nil,
			"system", "config", "remote_repository", name)
	}
}

func (h *Handler) proxy(c *gin.Context, clusterAware bool, query url.Values, segments ...string) {
	h.proxyRequest(c, http.MethodGet, clusterAware, controller.V1, query, nil, nil, segments...)
}

func (h *Handler) proxyRequest(
	c *gin.Context,
	method string,
	clusterAware bool,
	version controller.APIVersion,
	query url.Values,
	body []byte,
	extraHeaders http.Header,
	segments ...string,
) {
	var (
		target *url.URL
		err    error
	)
	if clusterAware {
		target, err = h.resolver.Resolve(c.GetHeader("Token"), version, segments...)
	} else {
		target, err = h.resolver.ResolveLocal(version, segments...)
	}
	if err != nil {
		writeInternalError(c)
		return
	}
	if query != nil {
		target.RawQuery = query.Encode()
	}

	headers := make(http.Header)
	token := c.GetHeader("Token")
	headers.Set("X-Auth-Token", token)
	headers.Set("Accept-Encoding", "gzip")
	headers.Set("Cache-Control", "no-cache")
	for name, values := range extraHeaders {
		for _, value := range values {
			headers.Add(name, value)
		}
	}
	if entry, ok := h.sessions.Get(token); ok {
		headers.Set("X-R-Sess", entry.SUSEToken)
	} else {
		headers.Set("X-R-Sess", "")
	}
	if body != nil {
		headers.Set("Content-Type", "text/plain; charset=UTF-8")
	}
	response, err := h.controller.DoTarget(c.Request.Context(), method, target, bytes.NewReader(body), headers)
	if err != nil {
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	copyResponse(c, response)
}

func scopeTarget(c *gin.Context) (url.Values, bool) {
	scope, present := c.GetQuery("scope")
	if !present {
		return nil, true
	}
	return url.Values{"scope": {scope}}, scope != "fed"
}

func optionalQuery(c *gin.Context, name string) url.Values {
	value, present := c.GetQuery(name)
	if !present {
		return nil
	}
	return url.Values{name: {value}}
}

func decodeBody(c *gin.Context, target any) bool {
	if err := json.NewDecoder(c.Request.Body).Decode(target); err != nil {
		writeDecodeError(c, err)
		return false
	}
	return true
}

func cleanJSONBody(c *gin.Context) (any, bool) {
	var value any
	if !decodeBody(c, &value) {
		return nil, false
	}
	return removeNullFields(value), true
}

func removeNullFields(value any) any {
	switch current := value.(type) {
	case map[string]any:
		cleaned := make(map[string]any, len(current))
		for key, item := range current {
			if item != nil {
				cleaned[key] = removeNullFields(item)
			}
		}
		return cleaned
	case []any:
		for index, item := range current {
			current[index] = removeNullFields(item)
		}
	}
	return value
}

func writeDecodeError(c *gin.Context, err error) {
	var sizeError *http.MaxBytesError
	if errors.As(err, &sizeError) {
		c.Data(http.StatusRequestEntityTooLarge, "text/plain; charset=UTF-8", []byte("Request entity too large"))
		return
	}
	writeBadRequest(c, "invalid JSON request body")
}

func writeBodyReadError(c *gin.Context, err error) {
	var sizeError *http.MaxBytesError
	if errors.As(err, &sizeError) {
		c.Data(http.StatusRequestEntityTooLarge, "text/plain; charset=UTF-8", []byte("Request entity too large"))
		return
	}
	writeBadRequest(c, "invalid request body")
}

func requestHeader(header http.Header, name string) (string, bool) {
	values, present := header[http.CanonicalHeaderKey(name)]
	if !present || len(values) == 0 {
		return "", false
	}
	return values[0], true
}

func writeBadRequest(c *gin.Context, message string) {
	c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte(message))
}

func requiredQuery(c *gin.Context, name string) (string, bool) {
	value, present := c.GetQuery(name)
	if !present {
		c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("query parameter '"+name+"' is required"))
	}
	return value, present
}

func copyResponse(c *gin.Context, response *http.Response) {
	for name, values := range response.Header {
		if strings.EqualFold(name, "Content-Length") {
			continue
		}
		for _, value := range values {
			c.Writer.Header().Add(name, value)
		}
	}
	c.Status(response.StatusCode)
	_, _ = io.Copy(c.Writer, response.Body)
}

func writeInternalError(c *gin.Context) {
	c.Data(http.StatusInternalServerError, "application/json", []byte("Internal server error"))
}
