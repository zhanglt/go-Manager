package group

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	managerCache "github.com/neuvector/manager/admin-go/internal/cache"
	"github.com/neuvector/manager/admin-go/internal/controller"
)

type criteriaItem struct {
	Name string `json:"name"`
}

type groupDTO struct {
	Name           string            `json:"name"`
	Comment        string            `json:"comment"`
	Domain         string            `json:"domain"`
	Learned        bool              `json:"learned"`
	Reserved       bool              `json:"reserved"`
	Criteria       []criteriaItem    `json:"criteria"`
	Members        []json.RawMessage `json:"members"`
	PolicyRules    json.RawMessage   `json:"policy_rules"`
	ResponseRules  json.RawMessage   `json:"response_rules"`
	PolicyMode     *string           `json:"policy_mode,omitempty"`
	ProfileMode    *string           `json:"profile_mode,omitempty"`
	Baseline       *string           `json:"baseline_profile,omitempty"`
	PlatformRole   string            `json:"platform_role"`
	CapChangeMode  *bool             `json:"cap_change_mode,omitempty"`
	CapScorable    *bool             `json:"cap_scorable,omitempty"`
	Kind           string            `json:"kind"`
	CfgType        *string           `json:"cfg_type,omitempty"`
	NotScored      bool              `json:"not_scored"`
	MonitorMetric  bool              `json:"monitor_metric"`
	GroupSessCur   int64             `json:"group_sess_cur"`
	GroupSessRate  int64             `json:"group_sess_rate"`
	GroupBandWidth int64             `json:"group_band_width"`
}

type groupSource struct {
	Name           string            `json:"name"`
	Comment        string            `json:"comment"`
	Domain         string            `json:"domain"`
	Learned        bool              `json:"learned"`
	Reserved       bool              `json:"reserved"`
	Criteria       []criteriaEntry   `json:"criteria"`
	Members        []json.RawMessage `json:"members"`
	PolicyRules    json.RawMessage   `json:"policy_rules"`
	ResponseRules  json.RawMessage   `json:"response_rules"`
	PolicyMode     *string           `json:"policy_mode"`
	ProfileMode    *string           `json:"profile_mode"`
	Baseline       *string           `json:"baseline_profile"`
	PlatformRole   string            `json:"platform_role"`
	CapChangeMode  *bool             `json:"cap_change_mode"`
	CapScorable    *bool             `json:"cap_scorable"`
	Kind           string            `json:"kind"`
	CfgType        *string           `json:"cfg_type"`
	NotScored      bool              `json:"not_scored"`
	MonitorMetric  bool              `json:"monitor_metric"`
	GroupSessCur   int64             `json:"group_sess_cur"`
	GroupSessRate  int64             `json:"group_sess_rate"`
	GroupBandWidth int64             `json:"group_band_width"`
}

type groupsSource struct {
	Groups []groupSource `json:"groups"`
}

type groupSourceWrap struct {
	Group groupSource `json:"group"`
}

func (h *Handler) GetGroups(c *gin.Context) {
	name, hasName := c.GetQuery("name")
	scope, hasScope := c.GetQuery("scope")
	if hasName {
		h.getSingleGroup(c, name, scope, hasScope)
		return
	}
	h.getGroupList(c, scope, hasScope)
}

func (h *Handler) getGroupList(c *gin.Context, scope string, hasScope bool) {
	start, hasStart, ok := groupInteger(c, "start")
	if !ok {
		return
	}
	limit, hasLimit, ok := groupInteger(c, "limit")
	if !ok {
		return
	}

	fetched := !hasStart || start == 0
	var groups []groupDTO
	if fetched {
		var err error
		groups, err = h.fetchGroupList(c, scope, hasScope)
		if err != nil {
			h.writeGroupError(c, err)
			return
		}
	}
	if !hasStart || !hasLimit {
		if !fetched {
			writeGroupJSON(c, nil)
		} else {
			writeGroupJSON(c, groups)
		}
		return
	}

	withCap := c.DefaultQuery("with_cap", "false")
	if hasScope && scope == "fed" {
		withCap = "<ignored>"
	}
	key := h.groupCacheKey(c.GetHeader("Token"), scope, hasScope, withCap)
	if fetched {
		encoded, _ := json.Marshal(groups)
		h.groups.Set(key, groups, int64(len(encoded)))
	} else if cached, found := h.groups.Get(key); found {
		groups = cached
	} else {
		groups = make([]groupDTO, 0)
	}
	page := sliceGroups(groups, start, limit)
	if len(page) < limit {
		h.groups.Delete(key)
	}
	writeGroupJSON(c, page)
}

func (h *Handler) fetchGroupList(c *gin.Context, scope string, hasScope bool) ([]groupDTO, error) {
	query := url.Values{"view": {"pod"}}
	local := hasScope && scope == "fed"
	if hasScope {
		query.Set("scope", scope)
	}
	if !local {
		query.Set("with_cap", c.DefaultQuery("with_cap", "false"))
	}
	var source groupsSource
	if err := h.fetchGroupJSON(c, local, query, &source, "group"); err != nil {
		return nil, err
	}
	result := make([]groupDTO, len(source.Groups))
	for index, group := range source.Groups {
		result[index] = convertGroup(group, false)
	}
	return result, nil
}

func (h *Handler) getSingleGroup(c *gin.Context, name, scope string, hasScope bool) {
	query := url.Values{"view": {"pod"}, "with_cap": {c.DefaultQuery("with_cap", "false")}}
	if hasScope {
		// Scala interpolates Option[String] directly for this branch.
		query.Set("scope", "Some("+scope+")")
	}
	var source groupSourceWrap
	if err := h.fetchGroupJSON(c, false, query, &source, "group", name); err != nil {
		h.writeGroupError(c, err)
		return
	}
	writeGroupJSON(c, gin.H{"group": convertGroup(source.Group, !hasScope)})
}

func (h *Handler) fetchGroupJSON(c *gin.Context, local bool, query url.Values, output any, segments ...string) error {
	var target *url.URL
	var err error
	if local {
		target, err = h.resolver.ResolveLocal(controller.V1, segments...)
	} else {
		target, err = h.resolver.Resolve(c.GetHeader("Token"), controller.V1, segments...)
	}
	if err != nil {
		return err
	}
	target.RawQuery = query.Encode()
	token := c.GetHeader("Token")
	headers := make(http.Header)
	headers.Set("X-Auth-Token", token)
	headers.Set("Accept-Encoding", "gzip")
	headers.Set("Cache-Control", "no-cache")
	if current, found := h.sessions.Get(token); found {
		headers.Set("X-R-Sess", current.SUSEToken)
	} else {
		headers.Set("X-R-Sess", "")
	}
	response, err := h.controller.DoTarget(c.Request.Context(), http.MethodGet, target, nil, headers)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return groupStatusError(response.StatusCode)
	}
	reader := io.Reader(response.Body)
	if strings.EqualFold(response.Header.Get("Content-Encoding"), "gzip") {
		compressed, err := gzip.NewReader(response.Body)
		if err != nil {
			return err
		}
		defer compressed.Close()
		reader = compressed
	}
	limited := io.LimitReader(reader, h.groups.MaxBytes()+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if int64(len(payload)) > h.groups.MaxBytes() {
		return errors.New("controller group response exceeds cache budget")
	}
	return json.Unmarshal(payload, output)
}

type groupStatusError int

func (status groupStatusError) Error() string { return fmt.Sprintf("controller status %d", status) }

func (h *Handler) writeGroupError(c *gin.Context, err error) {
	var status groupStatusError
	if errors.As(err, &status) {
		switch int(status) {
		case http.StatusUnauthorized:
			c.Data(http.StatusUnauthorized, "text/plain; charset=UTF-8", []byte("Authentication failed!"))
		case http.StatusRequestTimeout:
			c.Data(http.StatusRequestTimeout, "text/plain; charset=UTF-8", []byte("Session expired!"))
		case http.StatusServiceUnavailable:
			c.Data(http.StatusServiceUnavailable, "text/plain; charset=UTF-8", []byte("Server is not available!"))
		default:
			c.Data(http.StatusInternalServerError, "text/plain; charset=UTF-8", []byte("Internal server error"))
		}
		return
	}
	c.Data(http.StatusInternalServerError, "text/plain; charset=UTF-8", []byte("Internal server error"))
}

func convertGroup(source groupSource, single bool) groupDTO {
	criteria := make([]criteriaItem, len(source.Criteria))
	for index, entry := range source.Criteria {
		criteria[index] = criteriaItem{Name: entry.Key + criteriaOperator(entry.Op) + entry.Value}
	}
	responseRules := source.ResponseRules
	if !single && (len(responseRules) == 0 || string(responseRules) == "null") {
		responseRules = json.RawMessage("[]")
	}
	return groupDTO{
		Name: source.Name, Comment: source.Comment, Domain: source.Domain, Learned: source.Learned,
		Reserved: source.Reserved, Criteria: criteria, Members: source.Members,
		PolicyRules: source.PolicyRules, ResponseRules: responseRules, PolicyMode: source.PolicyMode,
		ProfileMode: source.ProfileMode, Baseline: source.Baseline, PlatformRole: source.PlatformRole,
		CapChangeMode: source.CapChangeMode, CapScorable: source.CapScorable, Kind: source.Kind,
		CfgType: source.CfgType, NotScored: source.NotScored, MonitorMetric: source.MonitorMetric,
		GroupSessCur: source.GroupSessCur, GroupSessRate: source.GroupSessRate,
		GroupBandWidth: source.GroupBandWidth,
	}
}

func criteriaOperator(operator string) string {
	switch operator {
	case "contains":
		return "@"
	case "prefix":
		return "^"
	case "regex":
		return "~"
	case "!=":
		return "!="
	case "!regex":
		return "!~"
	default:
		return "="
	}
}

func groupInteger(c *gin.Context, name string) (int, bool, bool) {
	value, present := c.GetQuery(name)
	if !present {
		return 0, false, true
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		c.Data(http.StatusInternalServerError, "text/plain; charset=UTF-8", []byte("Internal server error"))
		return 0, true, false
	}
	return parsed, true, true
}

func sliceGroups(groups []groupDTO, start, limit int) []groupDTO {
	if start < 0 {
		start = 0
	}
	if limit <= 0 || start >= len(groups) {
		return []groupDTO{}
	}
	end := start + limit
	if end < start || end > len(groups) {
		end = len(groups)
	}
	return groups[start:end]
}

func (h *Handler) groupCacheKey(token, scope string, hasScope bool, withCap string) managerCache.Key {
	cluster, _ := h.sessions.Cluster(token)
	if !hasScope {
		scope = "<none>"
	}
	return managerCache.Key{Token: token, Cluster: cluster, Domain: "group:" + scope + ":" + withCap}
}

func writeGroupJSON(c *gin.Context, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		c.Data(http.StatusInternalServerError, "text/plain; charset=UTF-8", []byte("Internal server error"))
		return
	}
	c.Data(http.StatusOK, "application/json", payload)
}
