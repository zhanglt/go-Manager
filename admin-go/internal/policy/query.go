package policy

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
)

type policyRuleWrap struct {
	Rules []json.RawMessage `json:"rules"`
}

func (h *Handler) GetPolicy(c *gin.Context) {
	start, hasStart, ok := policyInteger(c, "start")
	if !ok {
		return
	}
	limit, hasLimit, ok := policyInteger(c, "limit")
	if !ok {
		return
	}
	scope, hasScope := c.GetQuery("scope")
	fetched := !hasStart || start == 0
	var rules []json.RawMessage
	var original []byte
	if fetched {
		query := url.Values{}
		var response *http.Response
		var err error
		if hasScope {
			query.Set("scope", scope)
			response, err = h.transfer.FetchLocal(c, http.MethodGet, query, nil, nil, "policy", "rule")
		} else {
			response, err = h.transfer.Fetch(c, http.MethodGet, nil, nil, nil, "policy", "rule")
		}
		if err != nil {
			h.writePolicyError(c, 0)
			return
		}
		status := response.StatusCode
		original, err = readPolicyResponse(response, h.rules.MaxBytes())
		if err != nil || status != http.StatusOK {
			h.writePolicyError(c, status)
			return
		}
		var wrapper policyRuleWrap
		if err := json.Unmarshal(original, &wrapper); err != nil {
			h.writePolicyError(c, 0)
			return
		}
		rules = wrapper.Rules
	}
	if !hasStart || !hasLimit {
		if fetched {
			c.Data(http.StatusOK, "text/plain; charset=UTF-8", original)
		} else {
			c.Data(http.StatusOK, "text/plain; charset=UTF-8", []byte("null"))
		}
		return
	}
	key := h.policyCacheKey(c.GetHeader("Token"), scope, hasScope)
	if fetched {
		h.rules.Set(key, rules, int64(len(original)))
	} else if cached, found := h.rules.Get(key); found {
		rules = cached
	} else {
		rules = make([]json.RawMessage, 0)
	}
	page := slicePolicyRules(rules, start, limit)
	if len(page) < limit {
		h.rules.Delete(key)
	}
	writePolicyJSON(c, page)
}

type graphGroup struct {
	Name     string `json:"name"`
	Learned  bool   `json:"learned"`
	Reserved bool   `json:"reserved"`
}

type graphGroups struct {
	Groups []graphGroup `json:"groups"`
}

type graphRule struct {
	ID           *int      `json:"id"`
	From         string    `json:"from"`
	To           string    `json:"to"`
	Applications *[]string `json:"applications"`
	Action       string    `json:"action"`
}

type graphRules struct {
	Rules []graphRule `json:"rules"`
}

type policyNode struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Group         string `json:"group"`
	ClusterID     string `json:"clusterId"`
	ClusterName   string `json:"clusterName"`
	PlatformRole  string `json:"platform_role"`
	State         string `json:"state"`
	Domain        string `json:"domain"`
	CapQuarantine bool   `json:"cap_quarantine"`
	CapChangeMode bool   `json:"cap_change_mode"`
	CapSniff      bool   `json:"cap_sniff"`
}

type policyEdge struct {
	ID           *string   `json:"id,omitempty"`
	Source       string    `json:"source"`
	Target       string    `json:"target"`
	Label        *string   `json:"label,omitempty"`
	Status       string    `json:"status"`
	Applications *[]string `json:"applications,omitempty"`
	Bytes        int64     `json:"bytes"`
}

type policyGraph struct {
	Nodes     []policyNode `json:"nodes"`
	Edges     []policyEdge `json:"edges"`
	EnableGPU bool         `json:"enableGPU"`
}

func (h *Handler) GetPolicyGraph(c *gin.Context) {
	var groups graphGroups
	if err := h.fetchPolicyJSON(c, &groups, "group"); err != nil {
		writePolicyInternalError(c)
		return
	}
	var rules graphRules
	if err := h.fetchPolicyJSON(c, &rules, "policy", "rule"); err != nil {
		writePolicyInternalError(c)
		return
	}
	output := policyGraph{Nodes: make([]policyNode, len(groups.Groups)), Edges: make([]policyEdge, len(rules.Rules))}
	for index, group := range groups.Groups {
		kind := "custom"
		if group.Reserved && group.Name == "nv.external" {
			kind = "external"
		} else if group.Learned {
			kind = "learned"
		}
		output.Nodes[index] = policyNode{ID: group.Name, Label: group.Name, Group: kind}
	}
	for index, rule := range rules.Rules {
		idValue := "None"
		if rule.ID != nil {
			idValue = "Some(" + strconv.Itoa(*rule.ID) + ")"
		}
		var label *string
		if strings.EqualFold(rule.Action, "deny") {
			value := "X"
			label = &value
		}
		output.Edges[index] = policyEdge{ID: &idValue, Source: rule.From, Target: rule.To, Label: label, Status: rule.Action, Applications: rule.Applications}
	}
	writePolicyJSON(c, output)
}

type scanChild struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	BaseOS           string  `json:"base_os"`
	DisplayName      string  `json:"display_name"`
	Domain           string  `json:"domain"`
	Critical         int     `json:"critical"`
	High             int     `json:"high"`
	Medium           int     `json:"medium"`
	Host             string  `json:"host"`
	Image            string  `json:"image"`
	PlatformRole     string  `json:"platform_role"`
	PolicyMode       *string `json:"policy_mode,omitempty"`
	Result           string  `json:"result"`
	Service          string  `json:"service"`
	ServiceGroup     string  `json:"service_group"`
	State            string  `json:"state"`
	Status           string  `json:"status"`
	ScannerVersion   string  `json:"scanner_version"`
	ScannedTimestamp int64   `json:"scanned_timestamp"`
	ScannedAt        string  `json:"scanned_at"`
}

type scannedWorkload struct {
	ID               string       `json:"id"`
	Name             string       `json:"name"`
	BaseOS           string       `json:"base_os"`
	DisplayName      string       `json:"display_name"`
	Domain           string       `json:"domain"`
	Critical         int          `json:"critical"`
	High             int          `json:"high"`
	Medium           int          `json:"medium"`
	Host             string       `json:"host"`
	Image            string       `json:"image"`
	PlatformRole     string       `json:"platform_role"`
	PolicyMode       *string      `json:"policy_mode,omitempty"`
	Result           string       `json:"result"`
	Service          string       `json:"service"`
	ServiceGroup     string       `json:"service_group"`
	State            string       `json:"state"`
	Status           string       `json:"status"`
	ScannerVersion   string       `json:"scanner_version"`
	Children         *[]scanChild `json:"children,omitempty"`
	ScannedTimestamp int64        `json:"scanned_timestamp"`
	ScannedAt        string       `json:"scanned_at"`
}

type workloadStatus struct {
	Scanned         int    `json:"scanned"`
	Scheduled       int    `json:"scheduled"`
	Scanning        int    `json:"scanning"`
	Failed          int    `json:"failed"`
	CVEDBVersion    string `json:"cvedb_version"`
	CVEDBCreateTime string `json:"cvedb_create_time"`
}

type scannedWorkloadWrap struct {
	Workloads []scannedWorkload `json:"workloads"`
	Status    workloadStatus    `json:"status"`
}

type hiddenVulnerability struct {
	Critical int `json:"hidden_critical"`
	High     int `json:"hidden_high"`
	Medium   int `json:"hidden_medium"`
}

type convertedScannedWorkload struct {
	scannedWorkload
	Hidden hiddenVulnerability `json:"hidden_vulnerability"`
}

func (value convertedScannedWorkload) MarshalJSON() ([]byte, error) {
	type alias scannedWorkload
	base, err := json.Marshal(alias(value.scannedWorkload))
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(base, &fields); err != nil {
		return nil, err
	}
	hidden, _ := json.Marshal(value.Hidden)
	fields["hidden_vulnerability"] = hidden
	return json.Marshal(fields)
}

type convertedScannedWrap struct {
	Workloads []convertedScannedWorkload `json:"workloads"`
	Status    workloadStatus             `json:"status"`
}

func (h *Handler) GetScanWorkload(c *gin.Context) {
	if id, present := c.GetQuery("id"); present {
		query := url.Values{}
		if show, hasShow := c.GetQuery("show"); hasShow {
			query.Set("show", show)
		}
		h.transfer.Request(c, http.MethodGet, query, nil, nil, "scan", "workload", id)
		return
	}
	var input scannedWorkloadWrap
	if err := h.fetchPolicyJSONQuery(c, url.Values{"view": {"pod"}, "start": {"0"}, "limit": {"0"}}, &input, "scan", "workload"); err != nil {
		writePolicyInternalError(c)
		return
	}
	output := convertedScannedWrap{Workloads: make([]convertedScannedWorkload, len(input.Workloads)), Status: input.Status}
	for index, workload := range input.Workloads {
		critical, high, medium := workload.Critical, workload.High, workload.Medium
		if workload.Children != nil {
			for _, child := range *workload.Children {
				critical += child.Critical
				high += child.High
				medium += child.Medium
			}
		}
		output.Workloads[index] = convertedScannedWorkload{scannedWorkload: workload, Hidden: hiddenVulnerability{critical, high, medium}}
	}
	writePolicyJSON(c, output)
}

func (h *Handler) fetchPolicyJSON(c *gin.Context, output any, segments ...string) error {
	return h.fetchPolicyJSONQuery(c, nil, output, segments...)
}

func (h *Handler) fetchPolicyJSONQuery(c *gin.Context, query url.Values, output any, segments ...string) error {
	response, err := h.transfer.Fetch(c, http.MethodGet, query, nil, nil, segments...)
	if err != nil {
		return err
	}
	status := response.StatusCode
	payload, err := readPolicyResponse(response, h.rules.MaxBytes())
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("controller status %d", status)
	}
	return json.Unmarshal(payload, output)
}

func readPolicyResponse(response *http.Response, maxBytes int64) ([]byte, error) {
	defer response.Body.Close()
	var reader io.Reader = response.Body
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Encoding")), "gzip") {
		compressed, err := gzip.NewReader(response.Body)
		if err != nil {
			return nil, err
		}
		defer compressed.Close()
		reader = compressed
	}
	payload, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maxBytes {
		return nil, errors.New("controller policy response exceeds cache budget")
	}
	return payload, nil
}

func (h *Handler) policyCacheKey(token, scope string, hasScope bool) managerCache.Key {
	cluster, _ := h.sessions.Cluster(token)
	if hasScope {
		cluster = "<local>"
	}
	return managerCache.Key{Token: token, Cluster: cluster, Domain: "network-rule:" + scope}
}

func policyInteger(c *gin.Context, name string) (int, bool, bool) {
	value, present := c.GetQuery(name)
	if !present {
		return 0, false, true
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		c.Data(http.StatusInternalServerError, "text/plain; charset=UTF-8", []byte("Controller unavailable!"))
		return 0, true, false
	}
	return parsed, true, true
}

func slicePolicyRules(rules []json.RawMessage, start, limit int) []json.RawMessage {
	from := max(start, 0)
	if from > len(rules) {
		from = len(rules)
	}
	to := from
	if limit > 0 {
		to = min(from+limit, len(rules))
	}
	return rules[from:to]
}

func (h *Handler) writePolicyError(c *gin.Context, status int) {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		c.Data(http.StatusUnauthorized, "text/plain; charset=UTF-8", []byte("Authentication failed!"))
		return
	}
	c.Data(http.StatusInternalServerError, "text/plain; charset=UTF-8", []byte("Controller unavailable!"))
}

func writePolicyInternalError(c *gin.Context) {
	c.Data(http.StatusInternalServerError, "text/plain; charset=UTF-8", []byte("Internal server error"))
}

func writePolicyJSON(c *gin.Context, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		writePolicyInternalError(c)
		return
	}
	c.Data(http.StatusOK, "application/json", payload)
}
