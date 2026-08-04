package notification

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	managerCache "github.com/neuvector/manager/admin-go/internal/cache"
)

type graphScanBrief struct {
	Status   string `json:"status"`
	Critical int    `json:"critical"`
	High     int    `json:"high"`
	Medium   int    `json:"medium"`
}

type graphChildSource struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	DisplayName        string          `json:"display_name"`
	ScanSummary        *graphScanBrief `json:"scan_summary"`
	ServiceMeshSidecar *bool           `json:"service_mesh_sidecar"`
}

type graphEndpointSource struct {
	ID                 string             `json:"id"`
	Name               string             `json:"name"`
	DisplayName        string             `json:"display_name"`
	State              string             `json:"state"`
	Kind               string             `json:"kind"`
	PlatformRole       *string            `json:"platform_role"`
	ServiceGroup       *string            `json:"service_group"`
	Domain             string             `json:"domain"`
	ShareNSWith        *string            `json:"share_ns_with"`
	PolicyMode         *string            `json:"policy_mode"`
	ScanSummary        *graphScanBrief    `json:"scan_summary"`
	CapQuarantine      bool               `json:"cap_quarantine"`
	CapChangeMode      bool               `json:"cap_change_mode"`
	CapSniff           bool               `json:"cap_sniff"`
	ServiceMesh        *bool              `json:"service_mesh"`
	ServiceMeshSidecar *bool              `json:"service_mesh_sidecar"`
	Children           []graphChildSource `json:"children"`
}

type graphConversationSource struct {
	From         string   `json:"from"`
	To           string   `json:"to"`
	Bytes        int64    `json:"bytes"`
	Severity     *string  `json:"severity"`
	PolicyAction string   `json:"policy_action"`
	EventType    []string `json:"event_type"`
	Protocols    []string `json:"protocols"`
	Applications []string `json:"applications"`
	Ports        []string `json:"ports"`
	SidecarProxy *bool    `json:"sidecar_proxy"`
}

type graphData struct {
	Endpoints     []graphEndpointSource     `json:"endpoints"`
	Conversations []graphConversationSource `json:"conversations"`
}

type graphSubNode struct {
	ID        string          `json:"id"`
	Label     string          `json:"label"`
	ScanBrief *graphScanBrief `json:"scanBrief"`
	Sidecar   *bool           `json:"sidecar"`
}

type graphNode struct {
	ID                 string          `json:"id"`
	Label              string          `json:"label"`
	Group              string          `json:"group"`
	ClusterID          string          `json:"clusterId"`
	ClusterName        string          `json:"clusterName"`
	ScanBrief          *graphScanBrief `json:"scanBrief,omitempty"`
	PlatformRole       string          `json:"platform_role"`
	State              string          `json:"state"`
	Domain             string          `json:"domain"`
	CapQuarantine      bool            `json:"cap_quarantine"`
	CapChangeMode      bool            `json:"cap_change_mode"`
	CapSniff           bool            `json:"cap_sniff"`
	ServiceMesh        *bool           `json:"service_mesh,omitempty"`
	ServiceMeshSidecar *bool           `json:"service_mesh_sidecar,omitempty"`
	Children           []graphSubNode  `json:"children,omitempty"`
	PolicyMode         *string         `json:"policyMode,omitempty"`
}

type graphEdge struct {
	ID           *string  `json:"id"`
	Source       string   `json:"source"`
	Target       string   `json:"target"`
	Label        *string  `json:"label"`
	Status       string   `json:"status"`
	FromGroup    *string  `json:"fromGroup"`
	ToGroup      *string  `json:"toGroup"`
	FromDomain   *string  `json:"fromDomain"`
	ToDomain     *string  `json:"toDomain"`
	Protocols    []string `json:"protocols"`
	Applications []string `json:"applications"`
	EventType    []string `json:"event_type"`
	SidecarProxy *bool    `json:"sidecar_proxy"`
	Bytes        int64    `json:"bytes"`
}

type graphBlacklist struct {
	Domains []struct {
		Name string `json:"name"`
	} `json:"domains"`
	Groups []struct {
		Name string `json:"name"`
	} `json:"groups"`
	Endpoints []struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	} `json:"endpoints"`
}

type graphPosition struct {
	X float32 `json:"x"`
	Y float32 `json:"y"`
}
type graphLayout struct {
	User          string                   `json:"user"`
	NodePositions map[string]graphPosition `json:"nodePositions"`
}
type graphBlacklistInput struct {
	User      string          `json:"user"`
	Blacklist *graphBlacklist `json:"blacklist"`
}

type networkGraph struct {
	Nodes     []graphNode     `json:"nodes"`
	Edges     []graphEdge     `json:"edges"`
	Blacklist *graphBlacklist `json:"blacklist,omitempty"`
	EnableGPU bool            `json:"enableGPU"`
}

func NewGraphLayoutCache(maxEntries int, maxBytes int64, ttl time.Duration) *managerCache.Store[graphLayout] {
	return managerCache.New[graphLayout](maxEntries, maxBytes, ttl)
}

func NewGraphBlacklistCache(maxEntries int, maxBytes int64, ttl time.Duration) *managerCache.Store[graphBlacklist] {
	return managerCache.New[graphBlacklist](maxEntries, maxBytes, ttl)
}

func (h *Handler) GetNetworkGraph(c *gin.Context) {
	user, ok := c.GetQuery("user")
	if !ok {
		c.String(http.StatusBadRequest, "query parameter 'user' is required")
		return
	}
	payload, status, err := h.fetchGraphData(c)
	if err != nil || status != http.StatusOK {
		h.writeGraphError(c, status)
		return
	}
	var data graphData
	if err := json.Unmarshal(payload, &data); err != nil {
		writeNotificationError(c)
		return
	}
	nodes, edgeSource := graphNodes(data.Endpoints), data.Conversations
	nodeMap := make(map[string]graphNode, len(nodes))
	for _, node := range nodes {
		nodeMap[node.ID] = node
	}
	edges := make([]graphEdge, 0, len(edgeSource))
	for _, conversation := range edgeSource {
		edges = append(edges, graphEdgeFor(conversation, nodeMap))
	}
	var blacklist *graphBlacklist
	if found, exists := h.blacklists.Get(h.graphCacheKey(c.GetHeader("Token"), "blacklist", user)); exists && len(found) > 0 {
		blacklist = &found[0]
	}
	writeJSON(c, networkGraph{Nodes: nodes, Edges: edges, Blacklist: blacklist, EnableGPU: os.Getenv("ENABLE_GPU") == "true"})
}

func (h *Handler) CreateNetworkGraph(c *gin.Context) {
	var layout graphLayout
	if err := json.NewDecoder(c.Request.Body).Decode(&layout); err != nil || layout.User == "" {
		writeInvalidBody(c)
		return
	}
	if !h.validateGraphToken(c) {
		return
	}
	if layout.NodePositions != nil {
		payload, _ := json.Marshal(layout.NodePositions)
		h.layouts.Set(h.graphCacheKey(c.GetHeader("Token"), "layout", layout.User), []graphLayout{layout}, int64(len(payload)))
	}
	c.Status(http.StatusOK)
}

func (h *Handler) GetNetworkGraphLayout(c *gin.Context) {
	user, ok := c.GetQuery("user")
	if !ok {
		c.String(http.StatusBadRequest, "query parameter 'user' is required")
		return
	}
	if !h.validateGraphToken(c) {
		return
	}
	result := graphLayout{User: user}
	if found, exists := h.layouts.Get(h.graphCacheKey(c.GetHeader("Token"), "layout", user)); exists && len(found) > 0 {
		result.NodePositions = found[0].NodePositions
	}
	writeJSON(c, result)
}

func (h *Handler) GetNetworkGraphBlacklist(c *gin.Context) {
	user, ok := c.GetQuery("user")
	if !ok {
		c.String(http.StatusBadRequest, "query parameter 'user' is required")
		return
	}
	if !h.validateGraphToken(c) {
		return
	}
	if found, exists := h.blacklists.Get(h.graphCacheKey(c.GetHeader("Token"), "blacklist", user)); exists && len(found) > 0 {
		writeJSON(c, found[0])
		return
	}
	c.Data(http.StatusOK, "application/json", []byte("null"))
}

func (h *Handler) CreateNetworkGraphBlacklist(c *gin.Context) {
	var input graphBlacklistInput
	if err := json.NewDecoder(c.Request.Body).Decode(&input); err != nil || input.User == "" {
		writeInvalidBody(c)
		return
	}
	if !h.validateGraphToken(c) {
		return
	}
	if input.Blacklist != nil {
		payload, _ := json.Marshal(input.Blacklist)
		h.blacklists.Set(h.graphCacheKey(c.GetHeader("Token"), "blacklist", input.User), []graphBlacklist{*input.Blacklist}, int64(len(payload)))
	}
	c.Status(http.StatusOK)
}

func (h *Handler) fetchGraphData(c *gin.Context) ([]byte, int, error) {
	response, err := h.transfer.Fetch(c, http.MethodGet, nil, nil, nil, "conversation")
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	var reader io.Reader = response.Body
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Encoding")), "gzip") {
		decoded, gzipErr := gzip.NewReader(response.Body)
		if gzipErr != nil {
			return nil, response.StatusCode, gzipErr
		}
		defer decoded.Close()
		reader = decoded
	}
	payload, err := io.ReadAll(io.LimitReader(reader, h.layouts.MaxBytes()+1))
	if int64(len(payload)) > h.layouts.MaxBytes() {
		err = errors.New("controller graph response exceeds cache budget")
	}
	return payload, response.StatusCode, err
}

func (h *Handler) validateGraphToken(c *gin.Context) bool {
	response, err := h.transfer.Fetch(c, http.MethodPatch, nil, nil, []byte{}, "auth")
	if err != nil {
		h.writeGraphError(c, 0)
		return false
	}
	status := response.StatusCode
	_, readErr := io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if readErr != nil || status < 200 || status >= 300 {
		h.writeGraphError(c, status)
		return false
	}
	return true
}

func (h *Handler) graphCacheKey(token, kind, user string) managerCache.Key {
	cluster, _ := h.sessions.Cluster(token)
	return managerCache.Key{Token: token, Cluster: cluster, Domain: "network-graph-" + kind + ":" + user}
}

func (h *Handler) writeGraphError(c *gin.Context, status int) {
	switch status {
	case http.StatusRequestTimeout:
		c.Data(http.StatusRequestTimeout, "text/plain; charset=UTF-8", []byte("Session expired!"))
	case http.StatusUnauthorized:
		c.Data(http.StatusUnauthorized, "text/plain; charset=UTF-8", []byte("Authentication failed!"))
	case http.StatusServiceUnavailable:
		c.Data(http.StatusServiceUnavailable, "text/plain; charset=UTF-8", []byte("Server is not available!"))
	case http.StatusForbidden:
		c.Data(http.StatusForbidden, "text/plain; charset=UTF-8", []byte("Permission denied"))
	default:
		c.Data(http.StatusInternalServerError, "text/plain; charset=UTF-8", []byte("Internal server error"))
	}
}

func graphNodes(endpoints []graphEndpointSource) []graphNode {
	nodes := make([]graphNode, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if endpoint.State == "exit" || endpoint.ShareNSWith != nil || endpoint.ID == "" {
			continue
		}
		if endpoint.Domain == "" && endpoint.Kind == "container" && (endpoint.ServiceGroup == nil || strings.TrimSpace(*endpoint.ServiceGroup) == "") {
			continue
		}
		node := graphNode{
			ID: endpoint.ID, Label: compactGraphName(endpoint.DisplayName, endpoint.Name), Group: graphGroup(endpoint),
			ClusterID: graphClusterID(endpoint), ClusterName: graphClusterName(endpoint), ScanBrief: endpoint.ScanSummary,
			PlatformRole: graphPlatformRole(endpoint), State: endpoint.State, Domain: graphDomain(endpoint),
			CapQuarantine: endpoint.CapQuarantine, CapChangeMode: endpoint.CapChangeMode, CapSniff: endpoint.CapSniff,
			ServiceMesh: endpoint.ServiceMesh, ServiceMeshSidecar: endpoint.ServiceMeshSidecar, PolicyMode: endpoint.PolicyMode,
		}
		if endpoint.Children != nil {
			node.Children = make([]graphSubNode, len(endpoint.Children))
			for index, child := range endpoint.Children {
				node.Children[index] = graphSubNode{ID: child.ID, Label: compactGraphName(child.DisplayName, child.Name), ScanBrief: child.ScanSummary, Sidecar: child.ServiceMeshSidecar}
			}
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func graphEdgeFor(conversation graphConversationSource, nodes map[string]graphNode) graphEdge {
	id := conversation.From + conversation.To
	edge := graphEdge{ID: &id, Source: conversation.From, Target: conversation.To, Status: graphStatus(conversation, nodes), Protocols: conversation.Protocols, Applications: conversation.Applications, EventType: conversation.EventType, SidecarProxy: conversation.SidecarProxy, Bytes: conversation.Bytes}
	if label := graphLabel(conversation); label != "" {
		edge.Label = &label
	}
	if node, found := nodes[conversation.From]; found {
		edge.FromGroup, edge.FromDomain = stringPointer(node.ClusterID), stringPointer(node.Domain)
	}
	if node, found := nodes[conversation.To]; found {
		edge.ToGroup, edge.ToDomain = stringPointer(node.ClusterID), stringPointer(node.Domain)
	}
	return edge
}

func graphLabel(conversation graphConversationSource) string {
	values := append(append([]string(nil), conversation.Applications...), conversation.Ports...)
	filtered := make([]string, 0, 3)
	for _, value := range values {
		if value != "" && len(filtered) < 3 {
			filtered = append(filtered, value)
		}
	}
	label := strings.Join(filtered, ",")
	if strings.EqualFold(conversation.PolicyAction, "deny") {
		if label == "" {
			return "X"
		}
		return "X " + label
	}
	for _, event := range conversation.EventType {
		if event == "dlp" || event == "waf" {
			if label == "" {
				return "$"
			}
			return "$ " + label
		}
	}
	return label
}

func graphStatus(conversation graphConversationSource, nodes map[string]graphNode) string {
	if !strings.EqualFold(conversation.PolicyAction, "allow") && !strings.EqualFold(conversation.PolicyAction, "open") {
		return conversation.PolicyAction
	}
	if conversation.Severity != nil && *conversation.Severity != "" {
		return *conversation.Severity
	}
	from, fromFound := nodes[conversation.From]
	to, toFound := nodes[conversation.To]
	if !fromFound || !toFound || from.ClusterID != to.ClusterID {
		return "OK"
	}
	return "intraGroup"
}

func graphGroup(endpoint graphEndpointSource) string {
	policyMode := ""
	if endpoint.PolicyMode != nil {
		policyMode = *endpoint.PolicyMode
	}
	if endpoint.Kind == "container" || endpoint.Kind == "ip_service" {
		if endpoint.State == "unmanaged" {
			return "containerUnmanaged"
		}
		if endpoint.ServiceMesh != nil && *endpoint.ServiceMesh {
			return "mesh" + policyMode
		}
		return endpoint.Kind + policyMode
	}
	if endpoint.Kind == "node_ip" && endpoint.ServiceGroup != nil && strings.EqualFold(*endpoint.ServiceGroup, "nodes") {
		if strings.EqualFold(endpoint.State, "unmanaged") {
			return "hostUnmanaged"
		}
		return "host" + policyMode
	}
	return endpoint.Kind
}

func graphClusterID(endpoint graphEndpointSource) string {
	if endpoint.ServiceGroup == nil || *endpoint.ServiceGroup == "" {
		return graphFallbackCluster(endpoint)
	}
	return *endpoint.ServiceGroup
}

func graphClusterName(endpoint graphEndpointSource) string {
	if endpoint.ServiceGroup == nil || *endpoint.ServiceGroup == "" {
		return graphFallbackCluster(endpoint)
	}
	parts := strings.Split(*endpoint.ServiceGroup, ".")
	if len(parts) < 3 {
		return *endpoint.ServiceGroup
	}
	return strings.Join(parts[1:len(parts)-1], ".")
}

func graphFallbackCluster(endpoint graphEndpointSource) string {
	switch endpoint.Kind {
	case "node_ip", "workload_ip", "address", "ip_service":
		return endpoint.ID
	}
	return endpoint.Domain + "_group"
}

func graphDomain(endpoint graphEndpointSource) string {
	if endpoint.Domain != "" {
		return endpoint.Domain
	}
	switch endpoint.Kind {
	case "node_ip":
		if endpoint.ServiceGroup != nil && strings.EqualFold(*endpoint.ServiceGroup, "nodes") {
			return "nvManagedNode"
		}
		return "nvUnmanagedNode"
	case "workload_ip", "address":
		return "nvUnmanagedWorkload"
	case "external":
		return "external"
	default:
		return "_namespace"
	}
}

func graphPlatformRole(endpoint graphEndpointSource) string {
	if endpoint.PlatformRole != nil && *endpoint.PlatformRole != "" {
		return "System"
	}
	return ""
}
func compactGraphName(displayName, name string) string {
	if displayName == "" {
		return name
	}
	return displayName
}
func stringPointer(value string) *string { return &value }
