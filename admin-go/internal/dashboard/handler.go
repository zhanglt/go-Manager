package dashboard

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	managerCache "github.com/neuvector/manager/admin-go/internal/cache"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/session"
	"github.com/neuvector/manager/admin-go/internal/transfer"
)

type riskScoreWorkloads struct {
	RunningPods     int `json:"running_pods"`
	Privileged      int `json:"privileged_wls"`
	Root            int `json:"root_wls"`
	DiscoverExtEPs  int `json:"discover_ext_eps"`
	MonitorExtEPs   int `json:"monitor_ext_eps"`
	ProtectExtEPs   int `json:"protect_ext_eps"`
	ThreatExtEPs    int `json:"threat_ext_eps"`
	ViolationExtEPs int `json:"violate_ext_eps"`
}

type riskScoreGroups struct {
	Groups                  int `json:"groups"`
	DiscoverGroups          int `json:"discover_groups"`
	MonitorGroups           int `json:"monitor_groups"`
	ProtectGroups           int `json:"protect_groups"`
	ProfileDiscoverGroups   int `json:"profile_discover_groups"`
	ProfileMonitorGroups    int `json:"profile_monitor_groups"`
	ProfileProtectGroups    int `json:"profile_protect_groups"`
	DiscoverGroupsZeroDrift int `json:"discover_groups_zero_drift"`
	MonitorGroupsZeroDrift  int `json:"monitor_groups_zero_drift"`
	ProtectGroupsZeroDrift  int `json:"protect_groups_zero_drift"`
}

type riskScoreCVEs struct {
	Discover int `json:"discover_cves"`
	Monitor  int `json:"monitor_cves"`
	Protect  int `json:"protect_cves"`
	Platform int `json:"platform_cves"`
	Host     int `json:"host_cves"`
}

type scoreMetrics struct {
	Platform              string             `json:"platform"`
	KubernetesVersion     string             `json:"kube_version"`
	OpenShiftVersion      string             `json:"openshift_version"`
	NewServicePolicyMode  string             `json:"new_service_policy_mode"`
	NewServiceProfileMode string             `json:"new_service_profile_mode"`
	AdmissionMode         *string            `json:"adm_mode,omitempty"`
	EnabledDenyRules      *int               `json:"enabled_deny_adm_ctrl_rules,omitempty"`
	DenyAdmissionRules    int                `json:"deny_adm_ctrl_rules"`
	Hosts                 int                `json:"hosts"`
	Workloads             riskScoreWorkloads `json:"workloads"`
	Groups                riskScoreGroups    `json:"groups"`
	CVEs                  riskScoreCVEs      `json:"cves"`
}

type scoreMetricsWrap struct {
	Metrics scoreMetrics `json:"metrics"`
}

type Handler struct {
	transfer  *transfer.Proxy
	maxBytes  int64
	summaries *managerCache.Store[string]
}

func NewHandler(client *controller.Client, resolver *controller.TargetResolver, sessions *session.Store, limits ...int64) *Handler {
	maxBytes := int64(64 << 20)
	if len(limits) > 0 && limits[0] > 0 {
		maxBytes = limits[0]
	}
	return NewHandlerWithCache(client, resolver, sessions, maxBytes, NewCache(1000, maxBytes, 5*time.Minute))
}

func NewHandlerWithCache(client *controller.Client, resolver *controller.TargetResolver, sessions *session.Store, maxBytes int64, summaries *managerCache.Store[string]) *Handler {
	return &Handler{transfer: transfer.New(client, resolver, sessions), maxBytes: maxBytes, summaries: summaries}
}

func NewCache(maxEntries int, maxBytes int64, ttl time.Duration) *managerCache.Store[string] {
	return managerCache.New[string](maxEntries, maxBytes, ttl)
}

func (h *Handler) GetAlerts(c *gin.Context) {
	headers := make(http.Header)
	headers.Set("X-Nv-Page", "globalAlerts")
	h.transfer.Request(c, http.MethodGet, nil, headers, nil, "system", "alerts")
}

func (h *Handler) GetScores(c *gin.Context) {
	global := c.Query("isGlobalUser") == "true"
	if _, present := c.GetQuery("isGlobalUser"); !present {
		global = true
	}
	query := url.Values{"isGlobalUser": {"false"}}
	if global {
		query.Set("isGlobalUser", "true")
	}
	if domain, present := c.GetQuery("domain"); present {
		query.Set("f_domain", domain)
	}
	h.transfer.Request(c, http.MethodGet, query, nil, nil, "system", "score", "metrics")
}

func (h *Handler) PostScores(c *gin.Context) {
	var input scoreMetricsWrap
	if err := json.NewDecoder(c.Request.Body).Decode(&input); err != nil {
		transfer.WriteBodyError(c, err)
		return
	}
	body, err := json.Marshal(input)
	if err != nil {
		c.String(http.StatusInternalServerError, "Internal server error")
		return
	}
	h.transfer.Request(c, http.MethodPost, nil, nil, body, "system", "score", "metrics")
}

// GetDetails is implemented in details.go because it composes six Controller resources.
