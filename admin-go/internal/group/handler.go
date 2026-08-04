package group

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	managerCache "github.com/neuvector/manager/admin-go/internal/cache"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/session"
	"github.com/neuvector/manager/admin-go/internal/transfer"
)

type Handler struct {
	controller *controller.Client
	resolver   *controller.TargetResolver
	sessions   *session.Store
	groups     *managerCache.Store[groupDTO]
	transfer   *transfer.Proxy
}

type remoteExportOptions struct {
	RemoteRepositoryNickname string  `json:"remote_repository_nickname"`
	FilePath                 *string `json:"file_path,omitempty"`
	Comment                  *string `json:"comment,omitempty"`
}

type remoteExportOptionsInput struct {
	RemoteRepositoryNickname *string `json:"remote_repository_nickname"`
	FilePath                 *string `json:"file_path"`
	Comment                  *string `json:"comment"`
}

type groupsExportInput struct {
	Groups              []string                  `json:"groups"`
	PolicyMode          *string                   `json:"policy_mode"`
	ProfileMode         *string                   `json:"profile_mode"`
	UseNameReferral     *bool                     `json:"use_name_referral"`
	RemoteExportOptions *remoteExportOptionsInput `json:"remote_export_options"`
}

type groupsExport struct {
	Groups              []string             `json:"groups"`
	PolicyMode          *string              `json:"policy_mode,omitempty"`
	ProfileMode         *string              `json:"profile_mode,omitempty"`
	UseNameReferral     bool                 `json:"use_name_referral"`
	RemoteExportOptions *remoteExportOptions `json:"remote_export_options,omitempty"`
}

type criteriaItemInput struct {
	Name *string `json:"name"`
}

type criteriaEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Op    string `json:"op"`
}

type groupConfigInput struct {
	Name           *string              `json:"name"`
	Comment        *string              `json:"comment"`
	Criteria       *[]criteriaItemInput `json:"criteria"`
	CfgType        *string              `json:"cfg_type"`
	MonitorMetric  *bool                `json:"monitor_metric"`
	GroupSessCur   *int64               `json:"group_sess_cur"`
	GroupSessRate  *int64               `json:"group_sess_rate"`
	GroupBandWidth *int64               `json:"group_band_width"`
}

type groupConfig struct {
	Name           string          `json:"name"`
	Comment        string          `json:"comment"`
	Criteria       []criteriaEntry `json:"criteria"`
	CfgType        *string         `json:"cfg_type,omitempty"`
	MonitorMetric  bool            `json:"monitor_metric"`
	GroupSessCur   int64           `json:"group_sess_cur"`
	GroupSessRate  int64           `json:"group_sess_rate"`
	GroupBandWidth int64           `json:"group_band_width"`
}

type learnedGroupConfig struct {
	Name           string `json:"name"`
	MonitorMetric  bool   `json:"monitor_metric"`
	GroupSessCur   int64  `json:"group_sess_cur"`
	GroupSessRate  int64  `json:"group_sess_rate"`
	GroupBandWidth int64  `json:"group_band_width"`
}

type customCheckInput struct {
	Name   *string `json:"name"`
	Script *string `json:"script"`
}

type customChecksInput struct {
	Scripts *[]customCheckInput `json:"scripts"`
}

type customCheckConfigInput struct {
	Add    *customChecksInput `json:"add,omitempty"`
	Delete *customChecksInput `json:"delete,omitempty"`
	Update *customChecksInput `json:"update,omitempty"`
}

type customCheckDTOInput struct {
	Group  *string                `json:"group"`
	Config customCheckConfigInput `json:"config"`
}

func NewCache(maxEntries int, maxBytes int64, ttl time.Duration) *managerCache.Store[groupDTO] {
	return managerCache.New[groupDTO](maxEntries, maxBytes, ttl)
}

func NewHandler(client *controller.Client, resolver *controller.TargetResolver, sessions *session.Store, stores ...*managerCache.Store[groupDTO]) *Handler {
	var groups *managerCache.Store[groupDTO]
	if len(stores) > 0 {
		groups = stores[0]
	} else {
		groups = NewCache(1000, 64*1024*1024, 5*time.Minute)
	}
	return &Handler{
		controller: client, resolver: resolver, sessions: sessions, groups: groups,
		transfer: transfer.New(client, resolver, sessions),
	}
}

func (h *Handler) Export(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input groupsExportInput
		if err := json.NewDecoder(c.Request.Body).Decode(&input); err != nil {
			writeDecodeError(c, err)
			return
		}
		if input.Groups == nil || input.UseNameReferral == nil {
			c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("groups and use_name_referral are required"))
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
		body, _ := json.Marshal(groupsExport{
			Groups: input.Groups, PolicyMode: input.PolicyMode, ProfileMode: input.ProfileMode,
			UseNameReferral: *input.UseNameReferral, RemoteExportOptions: remoteOptions,
		})
		h.transfer.Post(c, scope, nil, body, "file", "group")
	}
}

func (h *Handler) Import(scope string) gin.HandlerFunc {
	return h.transfer.Import(scope, "file", "group", "config")
}

func (h *Handler) GetGroupList(c *gin.Context) {
	query := url.Values{"start": {"0"}, "brief": {"true"}}
	for _, name := range []string{"f_kind", "scope"} {
		if value, present := c.GetQuery(name); present {
			query.Set(name, value)
		}
	}
	h.transfer.Request(c, http.MethodGet, query, nil, nil, "group")
}

func (h *Handler) GetCustomCheck(c *gin.Context) {
	name, present := c.GetQuery("name")
	if !present {
		writeRequiredQuery(c, "name")
		return
	}
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, "custom_check", name)
}

func (h *Handler) UpdateCustomCheck(c *gin.Context) {
	var input customCheckDTOInput
	if err := json.NewDecoder(c.Request.Body).Decode(&input); err != nil {
		transfer.WriteBodyError(c, err)
		return
	}
	if input.Group == nil || !validateCustomCheckConfig(input.Config) {
		c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("invalid custom check config"))
		return
	}
	body, _ := json.Marshal(struct {
		Config customCheckConfigInput `json:"config"`
	}{Config: input.Config})
	h.transfer.Request(c, http.MethodPatch, nil, nil, body, "custom_check", *input.Group)
}

func (h *Handler) CreateGroup(c *gin.Context) {
	input, full, _, ok := decodeGroupConfig(c)
	if !ok {
		return
	}
	body, _ := json.Marshal(struct {
		Config groupConfig `json:"config"`
	}{Config: full})
	if input.CfgType != nil && *input.CfgType == "federal" {
		h.transfer.RequestLocal(c, http.MethodPost, nil, nil, body, "group")
	} else {
		h.transfer.Request(c, http.MethodPost, nil, nil, body, "group")
	}
}

func (h *Handler) UpdateGroup(c *gin.Context) {
	input, full, learned, ok := decodeGroupConfig(c)
	if !ok {
		return
	}
	cfgType := "user_created"
	if input.CfgType != nil {
		cfgType = *input.CfgType
	}
	var body []byte
	if cfgType == "user_created" || cfgType == "federal" {
		body, _ = json.Marshal(struct {
			Config groupConfig `json:"config"`
		}{Config: full})
	} else {
		body, _ = json.Marshal(struct {
			Config learnedGroupConfig `json:"config"`
		}{Config: learned})
	}
	if cfgType == "federal" {
		h.transfer.RequestLocal(c, http.MethodPatch, nil, nil, body, "group", *input.Name)
	} else {
		h.transfer.Request(c, http.MethodPatch, nil, nil, body, "group", *input.Name)
	}
}

func (h *Handler) DeleteGroup(c *gin.Context) {
	name, present := c.GetQuery("name")
	if !present {
		writeRequiredQuery(c, "name")
		return
	}
	scope, hasScope := c.GetQuery("scope")
	if hasScope && scope == "fed" {
		h.transfer.RequestLocal(c, http.MethodDelete, nil, nil, nil, "group", name)
		return
	}
	var query url.Values
	if hasScope {
		query = url.Values{"scope": {scope}}
	}
	h.transfer.Request(c, http.MethodDelete, query, nil, nil, "group", name)
}

func decodeGroupConfig(c *gin.Context) (groupConfigInput, groupConfig, learnedGroupConfig, bool) {
	var input groupConfigInput
	if err := json.NewDecoder(c.Request.Body).Decode(&input); err != nil {
		transfer.WriteBodyError(c, err)
		return input, groupConfig{}, learnedGroupConfig{}, false
	}
	if input.Name == nil || input.Comment == nil || input.Criteria == nil || input.MonitorMetric == nil || input.GroupSessCur == nil || input.GroupSessRate == nil || input.GroupBandWidth == nil {
		c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("group config fields are required"))
		return input, groupConfig{}, learnedGroupConfig{}, false
	}
	criteria := make([]criteriaEntry, 0, len(*input.Criteria))
	for _, item := range *input.Criteria {
		if item.Name == nil {
			c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("criteria.name is required"))
			return input, groupConfig{}, learnedGroupConfig{}, false
		}
		if entry, ok := parseCriteria(*item.Name); ok {
			criteria = append(criteria, entry)
		}
	}
	if len(criteria) == 0 {
		c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("Bad criteria"))
		return input, groupConfig{}, learnedGroupConfig{}, false
	}
	full := groupConfig{Name: *input.Name, Comment: *input.Comment, Criteria: criteria, CfgType: input.CfgType, MonitorMetric: *input.MonitorMetric, GroupSessCur: *input.GroupSessCur, GroupSessRate: *input.GroupSessRate, GroupBandWidth: *input.GroupBandWidth}
	learned := learnedGroupConfig{Name: *input.Name, MonitorMetric: *input.MonitorMetric, GroupSessCur: *input.GroupSessCur, GroupSessRate: *input.GroupSessRate, GroupBandWidth: *input.GroupBandWidth}
	return input, full, learned, true
}

func parseCriteria(value string) (criteriaEntry, bool) {
	type operator struct{ token, output string }
	operators := []operator{{"=", "="}, {"!=", "!="}, {"@", "contains"}, {"^", "prefix"}, {"~", "regex"}, {"!~", "!regex"}}
	bestIndex := -1
	var best operator
	for _, candidate := range operators {
		if index := strings.Index(value, candidate.token); index > 0 && (bestIndex < 0 || index < bestIndex) {
			bestIndex, best = index, candidate
		}
	}
	if bestIndex < 0 {
		return criteriaEntry{}, false
	}
	return criteriaEntry{Key: value[:bestIndex], Value: value[bestIndex+len(best.token):], Op: best.output}, true
}

func validateCustomCheckConfig(config customCheckConfigInput) bool {
	for _, collection := range []*customChecksInput{config.Add, config.Delete, config.Update} {
		if collection == nil {
			continue
		}
		if collection.Scripts == nil {
			return false
		}
		for _, script := range *collection.Scripts {
			if script.Name == nil || script.Script == nil {
				return false
			}
		}
	}
	return true
}

func writeRequiredQuery(c *gin.Context, name string) {
	c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("query parameter '"+name+"' is required"))
}

func writeDecodeError(c *gin.Context, err error) {
	transfer.WriteBodyError(c, err)
}
