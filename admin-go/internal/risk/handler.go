package risk

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/controller"
	managerMiddleware "github.com/neuvector/manager/admin-go/internal/middleware"
	"github.com/neuvector/manager/admin-go/internal/session"
	"github.com/neuvector/manager/admin-go/internal/transfer"
)

type Handler struct {
	transfer *transfer.Proxy
	sessions *session.Store
	nist     *nistDatabase
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

type profileExportInput struct {
	Names               []string                  `json:"names"`
	RemoteExportOptions *remoteExportOptionsInput `json:"remote_export_options"`
}

type profileExport struct {
	Names               []string             `json:"names"`
	RemoteExportOptions *remoteExportOptions `json:"remote_export_options,omitempty"`
}

type vulnerabilityEntryInput struct {
	ID      *int64    `json:"id"`
	Name    *string   `json:"name"`
	Comment *string   `json:"comment"`
	Days    *int      `json:"days"`
	Domains *[]string `json:"domains"`
	Images  *[]string `json:"images"`
}

type vulnerabilityEntry struct {
	ID      *int64   `json:"id,omitempty"`
	Name    string   `json:"name"`
	Comment string   `json:"comment"`
	Days    int      `json:"days"`
	Domains []string `json:"domains"`
	Images  []string `json:"images"`
}

type vulnerabilityConfigInput struct {
	Name    *string                    `json:"name"`
	Entries *[]vulnerabilityEntryInput `json:"entries"`
}

type vulnerabilityConfigWrapInput struct {
	Config vulnerabilityConfigInput `json:"config"`
}

type vulnerabilityConfig struct {
	Name    string                `json:"name"`
	Entries *[]vulnerabilityEntry `json:"entries,omitempty"`
}

type vulnerabilityConfigWrap struct {
	Config vulnerabilityConfig `json:"config"`
}

type vulnerabilityEntryWrap struct {
	Config vulnerabilityEntry `json:"config"`
}

type complianceEntryInput struct {
	TestNumber *string   `json:"test_number"`
	Tags       *[]string `json:"tags"`
}

type complianceEntry struct {
	TestNumber string   `json:"test_number"`
	Tags       []string `json:"tags"`
}

type complianceConfigInput struct {
	Name          *string                 `json:"name"`
	DisableSystem *bool                   `json:"disable_system"`
	Entries       *[]complianceEntryInput `json:"entries"`
}

type complianceConfig struct {
	Name          string             `json:"name"`
	DisableSystem *bool              `json:"disable_system,omitempty"`
	Entries       *[]complianceEntry `json:"entries,omitempty"`
}

type complianceConfigWrap struct {
	Config complianceConfig `json:"config"`
}

func NewHandler(client *controller.Client, resolver *controller.TargetResolver, sessions *session.Store) *Handler {
	return &Handler{transfer: transfer.New(client, resolver, sessions), sessions: sessions, nist: newNISTDatabase()}
}

func (h *Handler) LoadLocalData() error {
	h.nist.once.Do(h.nist.load)
	return h.nist.err
}

func (h *Handler) Export(resource string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input profileExportInput
		if err := json.NewDecoder(c.Request.Body).Decode(&input); err != nil {
			transfer.WriteBodyError(c, err)
			return
		}
		if input.Names == nil {
			c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("names is required"))
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
		body, _ := json.Marshal(profileExport{Names: input.Names, RemoteExportOptions: remoteOptions})
		h.transfer.PostQuery(c, nil, nil, body, "file", resource, "profile")
	}
}

func (h *Handler) ImportVulnerability(c *gin.Context) {
	option, present := c.GetQuery("option")
	if !present {
		c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("query parameter 'option' is required"))
		return
	}
	h.transfer.ImportQuery(url.Values{"option": {option}}, "file", "vulnerability", "profile", "config")(c)
}

func (h *Handler) ImportCompliance(c *gin.Context) {
	h.transfer.ImportQuery(nil, "file", "compliance", "profile", "config")(c)
}

func (h *Handler) GetVulnerabilityProfiles(c *gin.Context) {
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, "vulnerability", "profile")
}

func (h *Handler) GetCVE(c *gin.Context) {
	query := url.Values{}
	if show, present := c.GetQuery("show"); present {
		query.Set("show", show)
	}
	h.transfer.Request(c, http.MethodGet, query, nil, nil, "scan", "asset")
}

func (h *Handler) QueryCVEAssets(c *gin.Context) {
	var value any
	if err := json.NewDecoder(c.Request.Body).Decode(&value); err != nil {
		transfer.WriteBodyError(c, err)
		return
	}
	body, _ := json.Marshal(cleanNulls(value))
	query := url.Values{}
	if token, present := c.GetQuery("queryToken"); present {
		query.Set("token", token)
	}
	h.transfer.Request(c, http.MethodPost, query, nil, body, "assetvul")
}

func (h *Handler) GetCompliances(c *gin.Context) {
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, "compliance", "asset")
}

func (h *Handler) GetComplianceTemplate(c *gin.Context) {
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, "list", "compliance")
}

func (h *Handler) GetAvailableComplianceFilters(c *gin.Context) {
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, "compliance", "available_filter")
}

func (h *Handler) QueryScannedAssets(c *gin.Context) {
	h.queryAssets(c, "scan", "asset", "images")
}

func (h *Handler) QueryVulnerabilityAssets(c *gin.Context) {
	h.queryAssets(c, "vulasset")
}

func (h *Handler) queryAssets(c *gin.Context, segments ...string) {
	var value any
	if err := json.NewDecoder(c.Request.Body).Decode(&value); err != nil {
		transfer.WriteBodyError(c, err)
		return
	}
	body, _ := json.Marshal(cleanNulls(value))
	h.transfer.Request(c, http.MethodPost, nil, nil, body, segments...)
}

func (h *Handler) GetScannedAssets(c *gin.Context) {
	query, ok := assetPageQuery(c, false)
	if !ok {
		return
	}
	h.transfer.Request(c, http.MethodGet, query, nil, nil, "scan", "asset", "images")
}

func (h *Handler) GetVulnerabilityAssets(c *gin.Context) {
	query, ok := assetPageQuery(c, true)
	if !ok {
		return
	}
	h.transfer.Request(c, http.MethodGet, query, nil, nil, "vulasset")
}

func (h *Handler) QueryNISTCompliances(c *gin.Context) {
	var input struct {
		Config struct {
			Names []string `json:"names"`
		} `json:"config"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&input); err != nil {
		transfer.WriteBodyError(c, err)
		return
	}
	if input.Config.Names == nil {
		c.String(http.StatusBadRequest, "config.names is required")
		return
	}
	if _, ok := h.sessions.Get(c.GetHeader("Token")); !ok {
		c.String(http.StatusUnauthorized, "Authentication failed!")
		return
	}
	result, err := h.nist.lookup(input.Config.Names)
	if err != nil {
		c.String(http.StatusInternalServerError, "Internal server error")
		return
	}
	payload, _ := json.Marshal(map[string]any{"nist_map": result})
	managerMiddleware.RemoveSecurityHeaders(c.Writer.Header())
	c.Data(http.StatusOK, "application/json", payload)
}

func assetPageQuery(c *gin.Context, vulnerability bool) (url.Values, bool) {
	query := url.Values{}
	for _, name := range []string{"token", "start", "row"} {
		value, present := c.GetQuery(name)
		if !present {
			writeRequiredQuery(c, name)
			return nil, false
		}
		query.Set(name, value)
	}
	optional := []string{"orderby", "orderbyColumn", "qf"}
	if vulnerability {
		optional = append(optional, "lastmtime", "scoretype")
	}
	for _, name := range optional {
		if value, present := c.GetQuery(name); present {
			query.Set(name, value)
		}
	}
	return query, true
}

func (h *Handler) UpdateVulnerabilityProfile(c *gin.Context) {
	input, config, ok := decodeVulnerabilityConfig(c)
	if !ok {
		return
	}
	body, _ := json.Marshal(vulnerabilityConfigWrap{Config: config})
	h.transfer.Request(c, http.MethodPatch, nil, nil, body, "vulnerability", "profile", *input.Config.Name)
}

func (h *Handler) AddVulnerabilityEntry(c *gin.Context) {
	input, _, ok := decodeVulnerabilityConfig(c)
	if !ok {
		return
	}
	if input.Config.Entries == nil || len(*input.Config.Entries) == 0 {
		c.Data(http.StatusInternalServerError, "text/plain; charset=UTF-8", []byte("No entry has in the config data!"))
		return
	}
	entry, ok := convertVulnerabilityEntry(c, (*input.Config.Entries)[0], false)
	if !ok {
		return
	}
	body, _ := json.Marshal(vulnerabilityEntryWrap{Config: entry})
	h.transfer.Request(c, http.MethodPost, nil, nil, body, "vulnerability", "profile", *input.Config.Name, "entry")
}

func (h *Handler) UpdateVulnerabilityEntry(c *gin.Context) {
	name, present := c.GetQuery("name")
	if !present {
		writeRequiredQuery(c, "name")
		return
	}
	var input struct {
		Config vulnerabilityEntryInput `json:"config"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&input); err != nil {
		transfer.WriteBodyError(c, err)
		return
	}
	entry, ok := convertVulnerabilityEntry(c, input.Config, true)
	if !ok {
		return
	}
	body, _ := json.Marshal(vulnerabilityEntryWrap{Config: entry})
	h.transfer.Request(c, http.MethodPatch, nil, nil, body, "vulnerability", "profile", name, "entry", int64String(*entry.ID))
}

func (h *Handler) DeleteVulnerabilityEntry(c *gin.Context) {
	profileName, profilePresent := c.GetQuery("profile_name")
	entryID, entryPresent := c.GetQuery("entry_id")
	if !profilePresent {
		writeRequiredQuery(c, "profile_name")
		return
	}
	if !entryPresent {
		writeRequiredQuery(c, "entry_id")
		return
	}
	h.transfer.Request(c, http.MethodDelete, nil, nil, nil, "vulnerability", "profile", profileName, "entry", entryID)
}

func (h *Handler) GetComplianceProfiles(c *gin.Context) {
	segments := []string{"compliance", "profile"}
	if name, present := c.GetQuery("name"); present {
		segments = append(segments, name)
	}
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, segments...)
}

func (h *Handler) UpdateComplianceProfile(c *gin.Context) {
	var input complianceConfigInput
	if err := json.NewDecoder(c.Request.Body).Decode(&input); err != nil {
		transfer.WriteBodyError(c, err)
		return
	}
	if input.Name == nil {
		c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("name is required"))
		return
	}
	var entries *[]complianceEntry
	if input.Entries != nil {
		converted := make([]complianceEntry, len(*input.Entries))
		for index, item := range *input.Entries {
			if item.TestNumber == nil || item.Tags == nil {
				c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("compliance entry fields are required"))
				return
			}
			converted[index] = complianceEntry{TestNumber: *item.TestNumber, Tags: *item.Tags}
		}
		entries = &converted
	}
	config := complianceConfig{Name: *input.Name, DisableSystem: input.DisableSystem, Entries: entries}
	body, _ := json.Marshal(complianceConfigWrap{Config: config})
	h.transfer.Request(c, http.MethodPatch, nil, nil, body, "compliance", "profile", *input.Name)
}

func decodeVulnerabilityConfig(c *gin.Context) (vulnerabilityConfigWrapInput, vulnerabilityConfig, bool) {
	var input vulnerabilityConfigWrapInput
	if err := json.NewDecoder(c.Request.Body).Decode(&input); err != nil {
		transfer.WriteBodyError(c, err)
		return input, vulnerabilityConfig{}, false
	}
	if input.Config.Name == nil {
		c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("config.name is required"))
		return input, vulnerabilityConfig{}, false
	}
	var entries *[]vulnerabilityEntry
	if input.Config.Entries != nil {
		converted := make([]vulnerabilityEntry, len(*input.Config.Entries))
		for index, item := range *input.Config.Entries {
			entry, ok := convertVulnerabilityEntry(c, item, false)
			if !ok {
				return input, vulnerabilityConfig{}, false
			}
			converted[index] = entry
		}
		entries = &converted
	}
	return input, vulnerabilityConfig{Name: *input.Config.Name, Entries: entries}, true
}

func convertVulnerabilityEntry(c *gin.Context, input vulnerabilityEntryInput, requireID bool) (vulnerabilityEntry, bool) {
	if requireID && input.ID == nil {
		c.Data(http.StatusInternalServerError, "application/json", []byte("Internal server error"))
		return vulnerabilityEntry{}, false
	}
	if input.Name == nil || input.Comment == nil || input.Days == nil || input.Domains == nil || input.Images == nil {
		c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("vulnerability entry fields are required"))
		return vulnerabilityEntry{}, false
	}
	return vulnerabilityEntry{ID: input.ID, Name: *input.Name, Comment: *input.Comment, Days: *input.Days, Domains: *input.Domains, Images: *input.Images}, true
}

func writeRequiredQuery(c *gin.Context, name string) {
	c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("query parameter '"+name+"' is required"))
}

func int64String(value int64) string {
	return strconv.FormatInt(value, 10)
}

func cleanNulls(value any) any {
	switch current := value.(type) {
	case map[string]any:
		for key, item := range current {
			if item == nil {
				delete(current, key)
			} else {
				current[key] = cleanNulls(item)
			}
		}
	case []any:
		for index, item := range current {
			current[index] = cleanNulls(item)
		}
	}
	return value
}
