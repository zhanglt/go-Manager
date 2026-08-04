package policy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	managerCache "github.com/neuvector/manager/admin-go/internal/cache"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/session"
	"github.com/neuvector/manager/admin-go/internal/transfer"
)

type Handler struct {
	transfer *transfer.Proxy
	sessions *session.Store
	rules    *managerCache.Store[json.RawMessage]
}

type remoteExportOptionsInput struct {
	RemoteRepositoryNickname *string `json:"remote_repository_nickname"`
	FilePath                 *string `json:"file_path"`
	Comment                  *string `json:"comment"`
}

type remoteExportOptions struct {
	RemoteRepositoryNickname string  `json:"remote_repository_nickname"`
	FilePath                 *string `json:"file_path,omitempty"`
	Comment                  *string `json:"comment,omitempty"`
}

type responseRuleExportInput struct {
	IDs                 []int                     `json:"ids"`
	RemoteExportOptions *remoteExportOptionsInput `json:"remote_export_options"`
}

type responseRuleExport struct {
	IDs                 []int                `json:"ids"`
	RemoteExportOptions *remoteExportOptions `json:"remote_export_options,omitempty"`
}

func NewHandler(client *controller.Client, resolver *controller.TargetResolver, sessions *session.Store, rules *managerCache.Store[json.RawMessage]) *Handler {
	return &Handler{transfer: transfer.New(client, resolver, sessions), sessions: sessions, rules: rules}
}

func NewCache(maxEntries int, maxBytes int64, ttl time.Duration) *managerCache.Store[json.RawMessage] {
	return managerCache.New[json.RawMessage](maxEntries, maxBytes, ttl)
}

func (h *Handler) Export(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input responseRuleExportInput
		if err := json.NewDecoder(c.Request.Body).Decode(&input); err != nil {
			transfer.WriteBodyError(c, err)
			return
		}
		if input.IDs == nil {
			c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("ids is required"))
			return
		}
		var remoteOptions *remoteExportOptions
		if input.RemoteExportOptions != nil {
			if input.RemoteExportOptions.RemoteRepositoryNickname == nil {
				c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("remote_repository_nickname is required"))
				return
			}
			remoteOptions = &remoteExportOptions{
				RemoteRepositoryNickname: *input.RemoteExportOptions.RemoteRepositoryNickname,
				FilePath:                 input.RemoteExportOptions.FilePath, Comment: input.RemoteExportOptions.Comment,
			}
		}
		body, _ := json.Marshal(responseRuleExport{IDs: input.IDs, RemoteExportOptions: remoteOptions})
		h.transfer.Post(c, scope, nil, body, "file", "response", "rule")
	}
}

func (h *Handler) Import(scope string) gin.HandlerFunc {
	return h.transfer.Import(scope, "file", "response", "rule", "config")
}

func (h *Handler) GetResponseRule(c *gin.Context) {
	id, present := c.GetQuery("id")
	if !present {
		c.String(http.StatusBadRequest, "query parameter 'id' is required")
		return
	}
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, "response", "rule", id)
}

func (h *Handler) GetResponsePolicy(c *gin.Context)    { h.responsePolicy(c, http.MethodGet) }
func (h *Handler) DeleteResponsePolicy(c *gin.Context) { h.responsePolicy(c, http.MethodDelete) }

func (h *Handler) responsePolicy(c *gin.Context, method string) {
	query := url.Values{}
	if scope, present := c.GetQuery("scope"); present {
		query.Set("scope", scope)
	}
	segments := []string{"response", "rule"}
	if id, present := c.GetQuery("id"); present {
		segments = append(segments, id)
	}
	if query.Get("scope") == "fed" {
		h.transfer.RequestLocal(c, method, query, nil, nil, segments...)
		return
	}
	h.transfer.Request(c, method, query, nil, nil, segments...)
}

func (h *Handler) CreateResponsePolicy(c *gin.Context) { h.mutateResponsePolicy(c, true) }
func (h *Handler) UpdateResponsePolicy(c *gin.Context) { h.mutateResponsePolicy(c, false) }

func (h *Handler) mutateResponsePolicy(c *gin.Context, create bool) {
	var body any
	if err := json.NewDecoder(c.Request.Body).Decode(&body); err != nil {
		transfer.WriteBodyError(c, err)
		return
	}
	body = withoutNulls(body)
	config := body
	if create {
		wrap, ok := body.(map[string]any)
		if !ok {
			c.String(http.StatusBadRequest, "insert is required")
			return
		}
		insert, ok := wrap["insert"].(map[string]any)
		if !ok {
			c.String(http.StatusBadRequest, "insert is required")
			return
		}
		rules, ok := insert["rules"].([]any)
		if !ok || len(rules) == 0 {
			c.String(http.StatusBadRequest, "insert.rules is required")
			return
		}
		config = rules[0]
	} else {
		wrap, ok := body.(map[string]any)
		if !ok {
			c.String(http.StatusBadRequest, "response rule config is required")
			return
		}
		config = wrap["config"]
	}
	rule, ok := config.(map[string]any)
	if !ok {
		c.String(http.StatusBadRequest, "response rule config is required")
		return
	}
	segments := []string{"response", "rule"}
	if !create {
		id, ok := rule["id"]
		if !ok {
			c.String(http.StatusBadRequest, "config.id is required")
			return
		}
		segments = append(segments, fmt.Sprint(id))
	}
	payload, _ := json.Marshal(body)
	if rule["cfg_type"] == "federal" {
		h.transfer.RequestLocal(c, http.MethodPatch, nil, nil, payload, segments...)
		return
	}
	h.transfer.Request(c, http.MethodPatch, nil, nil, payload, segments...)
}

func withoutNulls(value any) any {
	switch current := value.(type) {
	case map[string]any:
		for key, item := range current {
			if item == nil {
				delete(current, key)
			} else {
				current[key] = withoutNulls(item)
			}
		}
	case []any:
		for index, item := range current {
			current[index] = withoutNulls(item)
		}
	}
	return value
}

func (h *Handler) DeployFederal(c *gin.Context) {
	body, ok := decodeClean(c)
	if ok {
		h.transfer.RequestLocal(c, http.MethodPost, nil, nil, body, "fed", "deploy")
	}
}

func (h *Handler) GetConditionOptions(c *gin.Context) {
	query := url.Values{}
	if scope, present := c.GetQuery("scope"); present {
		query.Set("scope", scope)
	}
	if query.Get("scope") == "fed" {
		h.transfer.RequestLocal(c, http.MethodGet, query, nil, nil, "response", "options")
		return
	}
	h.transfer.Request(c, http.MethodGet, query, nil, nil, "response", "options")
}

func (h *Handler) Unquarantine(c *gin.Context) {
	body, ok := decodeClean(c)
	if ok {
		h.transfer.Request(c, http.MethodPost, nil, nil, body, "system", "request")
	}
}

func (h *Handler) GetPolicyApplications(c *gin.Context) {
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, "list", "application")
}

func (h *Handler) GetPolicyRule(c *gin.Context) {
	id, present := c.GetQuery("id")
	if !present {
		c.String(http.StatusBadRequest, "query parameter 'id' is required")
		return
	}
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, "policy", "rule", id)
}

func (h *Handler) CreatePolicyRule(c *gin.Context) {
	var rule any
	if err := json.NewDecoder(c.Request.Body).Decode(&rule); err != nil {
		transfer.WriteBodyError(c, err)
		return
	}
	body, _ := json.Marshal(map[string]any{"insert": map[string]any{"after": 0, "rules": []any{withoutNulls(rule)}}})
	h.transfer.Request(c, http.MethodPatch, nil, nil, body, "policy", "rule")
}

func (h *Handler) UpdatePolicyRule(c *gin.Context) {
	var rule any
	if err := json.NewDecoder(c.Request.Body).Decode(&rule); err != nil {
		transfer.WriteBodyError(c, err)
		return
	}
	config, ok := withoutNulls(rule).(map[string]any)
	if !ok {
		c.String(http.StatusBadRequest, "policy rule is required")
		return
	}
	id, present := config["id"]
	if !present {
		c.String(http.StatusBadRequest, "id is required")
		return
	}
	body, _ := json.Marshal(map[string]any{"config": config, "replicate": true})
	h.transfer.Request(c, http.MethodPatch, nil, nil, body, "policy", "rule", fmt.Sprint(id))
}

func (h *Handler) DeletePolicy(c *gin.Context) {
	segments := []string{"policy", "rule"}
	if id, present := c.GetQuery("id"); present {
		segments = append(segments, id)
	}
	h.transfer.Request(c, http.MethodDelete, nil, nil, nil, segments...)
}

func (h *Handler) UpdatePolicy(c *gin.Context) {
	scope, present := c.GetQuery("scope")
	if !present {
		c.String(http.StatusBadRequest, "query parameter 'scope' is required")
		return
	}
	body, ok := decodeClean(c)
	if !ok {
		return
	}
	query := url.Values{"scope": {scope}}
	if scope == "fed" {
		h.transfer.RequestLocal(c, http.MethodPatch, query, nil, body, "policy", "rule")
		return
	}
	h.transfer.Request(c, http.MethodPatch, query, nil, body, "policy", "rule")
}

func decodeClean(c *gin.Context) ([]byte, bool) {
	var value any
	if err := json.NewDecoder(c.Request.Body).Decode(&value); err != nil {
		transfer.WriteBodyError(c, err)
		return nil, false
	}
	body, _ := json.Marshal(withoutNulls(value))
	return body, true
}

func (h *Handler) GetAdmissionRules(c *gin.Context) {
	query := url.Values{}
	if scope, present := c.GetQuery("scope"); present {
		query.Set("scope", scope)
		h.transfer.RequestLocal(c, http.MethodGet, query, nil, nil, "admission", "rules")
		return
	}
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, "admission", "rules")
}

func (h *Handler) CreateAdmissionRule(c *gin.Context) { h.mutateAdmissionRule(c, http.MethodPost) }
func (h *Handler) UpdateAdmissionRule(c *gin.Context) { h.mutateAdmissionRule(c, http.MethodPatch) }

func (h *Handler) mutateAdmissionRule(c *gin.Context, method string) {
	var value any
	if err := json.NewDecoder(c.Request.Body).Decode(&value); err != nil {
		transfer.WriteBodyError(c, err)
		return
	}
	value = withoutNulls(value)
	wrap, ok := value.(map[string]any)
	if !ok {
		c.String(http.StatusBadRequest, "config is required")
		return
	}
	config, ok := wrap["config"].(map[string]any)
	if !ok {
		c.String(http.StatusBadRequest, "config is required")
		return
	}
	body, _ := json.Marshal(value)
	if config["cfg_type"] == "federal" {
		h.transfer.RequestLocal(c, method, nil, nil, body, "admission", "rule")
		return
	}
	h.transfer.Request(c, method, nil, nil, body, "admission", "rule")
}

func (h *Handler) DeleteAdmissionRule(c *gin.Context) {
	id, present := c.GetQuery("id")
	if !present {
		c.String(http.StatusBadRequest, "query parameter 'id' is required")
		return
	}
	query := url.Values{}
	if scope, present := c.GetQuery("scope"); present {
		query.Set("scope", scope)
		if scope == "fed" {
			h.transfer.RequestLocal(c, http.MethodDelete, query, nil, nil, "admission", "rule", id)
			return
		}
	}
	h.transfer.Request(c, http.MethodDelete, query, nil, nil, "admission", "rule", id)
}

func (h *Handler) GetAdmissionOptions(c *gin.Context) { h.admissionScopedGet(c, "options") }
func (h *Handler) admissionScopedGet(c *gin.Context, resource string) {
	query := url.Values{}
	if scope, present := c.GetQuery("scope"); present {
		query.Set("scope", scope)
		if scope == "fed" {
			h.transfer.RequestLocal(c, http.MethodGet, query, nil, nil, "admission", resource)
			return
		}
	}
	h.transfer.Request(c, http.MethodGet, query, nil, nil, "admission", resource)
}
func (h *Handler) GetAdmissionState(c *gin.Context) {
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, "admission", "state")
}
func (h *Handler) UpdateAdmissionState(c *gin.Context) {
	if body, ok := decodeClean(c); ok {
		h.transfer.Request(c, http.MethodPatch, nil, nil, body, "admission", "state")
	}
}
func (h *Handler) TestAdmission(c *gin.Context) {
	h.transfer.Request(c, http.MethodPost, nil, nil, nil, "debug", "admission", "test")
}

func (h *Handler) TestAdmissionMatching(c *gin.Context) {
	value, err := io.ReadAll(c.Request.Body)
	if err != nil {
		transfer.WriteBodyError(c, err)
		return
	}
	body := []byte(transfer.ExtractLegacyForm(string(value)))
	h.transfer.Request(c, http.MethodPost, nil, nil, body, "assess", "admission", "rule")
}
func (h *Handler) ExportAdmission(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if body, ok := decodeClean(c); ok {
			h.transfer.Post(c, scope, nil, body, "file", "admission")
		}
	}
}
func (h *Handler) ImportAdmission(scope string) gin.HandlerFunc {
	return h.transfer.Import(scope, "file", "admission", "config")
}
func (h *Handler) PromoteAdmission(c *gin.Context) {
	if body, ok := decodeClean(c); ok {
		h.transfer.Request(c, http.MethodPost, nil, nil, body, "admission", "rule", "promote")
	}
}

func (h *Handler) GetScanStatus(c *gin.Context) {
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, "scan", "status")
}
func (h *Handler) GetScanHost(c *gin.Context) {
	segments := []string{"scan", "host"}
	query := url.Values{}
	if id, present := c.GetQuery("id"); present {
		segments = append(segments, id)
		if show, ok := c.GetQuery("show"); ok {
			query.Set("show", show)
		}
	} else {
		query.Set("start", "0")
		query.Set("limit", "0")
	}
	h.transfer.Request(c, http.MethodGet, query, nil, nil, segments...)
}
func (h *Handler) ScanHost(c *gin.Context) { h.scanResource(c, "host", "body") }
func (h *Handler) GetScanPlatform(c *gin.Context) {
	segments := []string{"scan", "platform"}
	query := url.Values{}
	if _, present := c.GetQuery("platform"); present {
		segments = append(segments, "platform")
		if show, ok := c.GetQuery("show"); ok {
			query.Set("show", show)
		}
	}
	h.transfer.Request(c, http.MethodGet, query, nil, nil, segments...)
}
func (h *Handler) ScanPlatform(c *gin.Context) { h.scanResource(c, "platform", "platform") }
func (h *Handler) scanResource(c *gin.Context, resource, fixed string) {
	value, err := io.ReadAll(c.Request.Body)
	if err != nil {
		transfer.WriteBodyError(c, err)
		return
	}
	id := string(value)
	if fixed != "body" {
		id = fixed
	}
	h.transfer.Request(c, http.MethodPost, nil, nil, nil, "scan", resource, id)
}
func (h *Handler) GetScanConfig(c *gin.Context) {
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, "scan", "config")
}
func (h *Handler) UpdateScanConfig(c *gin.Context) {
	if body, ok := decodeClean(c); ok {
		h.transfer.Request(c, http.MethodPatch, nil, nil, body, "scan", "config")
	}
}
func (h *Handler) GetRegistryTypes(c *gin.Context) {
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, "list", "registry_type")
}

func (h *Handler) PromotePolicy(c *gin.Context) {
	if body, ok := decodeClean(c); ok {
		h.transfer.Request(c, http.MethodPost, nil, nil, body, "policy", "rules", "promote")
	}
}
func (h *Handler) ScanWorkload(c *gin.Context) { h.scanResource(c, "workload", "body") }
func (h *Handler) GetScanRegistry(c *gin.Context) {
	segments := []string{"scan", "registry"}
	if name, present := c.GetQuery("name"); present {
		segments = append(segments, name)
	}
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, segments...)
}
func (h *Handler) DeleteScanRegistry(c *gin.Context) {
	name, ok := requiredPolicyQuery(c, "name")
	if ok {
		h.transfer.Request(c, http.MethodDelete, nil, nil, nil, "scan", "registry", name)
	}
}
func (h *Handler) GetRegistryRepo(c *gin.Context) {
	name, ok := requiredPolicyQuery(c, "name")
	if ok {
		h.transfer.Request(c, http.MethodGet, nil, nil, nil, "scan", "registry", name, "images")
	}
}
func (h *Handler) ScanRegistryRepo(c *gin.Context) {
	value, err := io.ReadAll(c.Request.Body)
	if err != nil {
		transfer.WriteBodyError(c, err)
		return
	}
	h.transfer.Request(c, http.MethodPost, nil, nil, nil, "scan", "registry", string(value), "scan")
}
func (h *Handler) StopRegistryRepo(c *gin.Context) {
	name, ok := requiredPolicyQuery(c, "name")
	if ok {
		h.transfer.Request(c, http.MethodDelete, nil, nil, nil, "scan", "registry", name, "scan")
	}
}
func (h *Handler) GetFederatedRegistryRepo(c *gin.Context) {
	name, ok := requiredPolicyQuery(c, "fed_repo")
	if ok {
		h.transfer.Request(c, http.MethodGet, nil, nil, nil, "scan", "registry", name, "images")
	}
}
func (h *Handler) GetRegistryImage(c *gin.Context) { h.registryReport(c, "image", false) }
func (h *Handler) GetRegistryLayer(c *gin.Context) { h.registryReport(c, "layers", true) }
func (h *Handler) registryReport(c *gin.Context, resource string, scalaOption bool) {
	name, a := c.GetQuery("name")
	id, b := c.GetQuery("imageId")
	if !a || !b {
		c.String(http.StatusBadRequest, "query parameters 'name' and 'imageId' are required")
		return
	}
	query := url.Values{}
	if show, ok := c.GetQuery("show"); ok {
		if scalaOption {
			show = "Some(" + show + ")"
		}
		query.Set("show", show)
	}
	h.transfer.Request(c, http.MethodGet, query, nil, nil, "scan", "registry", name, resource, id)
}
func (h *Handler) GetScanTop(c *gin.Context) {
	h.transfer.Request(c, http.MethodGet, url.Values{"s_severity": {"desc"}, "start": {"0"}, "limit": {"5"}}, nil, nil, "scan", "image")
}
func requiredPolicyQuery(c *gin.Context, name string) (string, bool) {
	value, present := c.GetQuery(name)
	if !present {
		c.String(http.StatusBadRequest, "query parameter '"+name+"' is required")
	}
	return value, present
}

func (h *Handler) CreateScanRegistry(c *gin.Context) {
	if body, ok := decodeClean(c); ok {
		h.transfer.RequestV2(c, http.MethodPost, nil, nil, body, "scan", "registry")
	}
}
func (h *Handler) UpdateScanRegistry(c *gin.Context) {
	var value any
	if err := json.NewDecoder(c.Request.Body).Decode(&value); err != nil {
		transfer.WriteBodyError(c, err)
		return
	}
	wrap, ok := withoutNulls(value).(map[string]any)
	if !ok {
		c.String(http.StatusBadRequest, "registry config is required")
		return
	}
	name, ok := wrap["name"].(string)
	if !ok || name == "" {
		c.String(http.StatusBadRequest, "name is required")
		return
	}
	config, ok := wrap["wrap"]
	if !ok {
		c.String(http.StatusBadRequest, "wrap is required")
		return
	}
	body, _ := json.Marshal(config)
	h.transfer.RequestV2(c, http.MethodPatch, nil, nil, body, "scan", "registry", name)
}
func (h *Handler) TestScanRegistry(c *gin.Context) {
	var value any
	if err := json.NewDecoder(c.Request.Body).Decode(&value); err != nil {
		transfer.WriteBodyError(c, err)
		return
	}
	value = withoutNulls(value)
	wrap, ok := value.(map[string]any)
	if !ok {
		c.String(http.StatusBadRequest, "config is required")
		return
	}
	config, ok := wrap["config"].(map[string]any)
	if !ok {
		c.String(http.StatusBadRequest, "config is required")
		return
	}
	name, ok := config["name"].(string)
	if !ok || name == "" {
		c.String(http.StatusBadRequest, "config.name is required")
		return
	}
	body, _ := json.Marshal(value)
	headers := http.Header{}
	if tx, present := c.Request.Header[http.CanonicalHeaderKey("X-Transaction-Id")]; present && len(tx) > 0 {
		headers.Set("X-Transaction-Id", tx[0])
	}
	h.transfer.RequestTransaction(c, http.MethodPost, controller.V2, false, nil, headers, body, "scan", "registry", name, "test")
}
func (h *Handler) DeleteScanRegistryTest(c *gin.Context) {
	name, ok := requiredPolicyQuery(c, "name")
	if !ok {
		return
	}
	tx := c.GetHeader("X-Transaction-Id")
	if tx == "" {
		c.String(http.StatusBadRequest, "header 'X-Transaction-Id' is required")
		return
	}
	headers := http.Header{"X-Transaction-Id": {tx}}
	h.transfer.RequestTransaction(c, http.MethodDelete, controller.V2, true, nil, headers, nil, "scan", "registry", name, "test")
}
