package workload

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

type ScanSummary struct {
	Status           string `json:"status"`
	Critical         int32  `json:"critical"`
	High             int32  `json:"high"`
	Medium           int32  `json:"medium"`
	HiddenCritical   *int32 `json:"hidden_critical,omitempty"`
	HiddenHigh       *int32 `json:"hidden_high,omitempty"`
	HiddenMedium     *int32 `json:"hidden_medium,omitempty"`
	Result           string `json:"result"`
	ScannedTimestamp int64  `json:"scanned_timestamp"`
	ScannedAt        string `json:"scanned_at"`
	BaseOS           string `json:"base_os"`
	ScannerVersion   string `json:"scanner_version"`
	CVEDBCreateTime  string `json:"cvedb_create_time"`
}

type IPAddress struct {
	IP       string `json:"ip"`
	IPPrefix int32  `json:"ip_prefix"`
	Gateway  string `json:"gateway"`
}

type ProtoPort struct {
	IPProto  int32  `json:"ip_proto"`
	Port     int64  `json:"port"`
	HostIP   string `json:"host_ip"`
	HostPort int64  `json:"host_port"`
}

type WorkloadBriefV2 struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	DisplayName     string  `json:"display_name"`
	HostName        string  `json:"host_name"`
	HostID          string  `json:"host_id"`
	Image           string  `json:"image"`
	ImageID         string  `json:"image_id"`
	ImageCreatedAt  *string `json:"image_created_at,omitempty"`
	ImageRegScanned *bool   `json:"image_reg_scanned,omitempty"`
	Domain          string  `json:"domain"`
	State           string  `json:"state"`
	Service         string  `json:"service"`
	Author          string  `json:"author"`
	ServiceGroup    string  `json:"service_group"`
}

type WorkloadSecurityV2 struct {
	CapSniff           bool        `json:"cap_sniff"`
	CapQuarantine      bool        `json:"cap_quarantine"`
	CapChangeMode      bool        `json:"cap_change_mode"`
	ServiceMesh        bool        `json:"service_mesh"`
	ServiceMeshSidecar bool        `json:"service_mesh_sidecar"`
	PolicyMode         string      `json:"policy_mode"`
	ProfileMode        string      `json:"profile_mode"`
	BaselineProfile    string      `json:"baseline_profile"`
	QuarantineReason   *string     `json:"quarantine_reason,omitempty"`
	ScanSummary        ScanSummary `json:"scan_summary"`
}

type WorkloadRuntimeV2 struct {
	PodName        string                  `json:"pod_name"`
	ShareNSWith    *string                 `json:"share_ns_with,omitempty"`
	Privileged     bool                    `json:"privileged"`
	RunAsRoot      bool                    `json:"run_as_root"`
	Labels         *map[string]string      `json:"labels,omitempty"`
	MemoryLimit    int64                   `json:"memory_limit"`
	CPUs           string                  `json:"cpus"`
	ServiceAccount string                  `json:"service_account"`
	NetworkMode    string                  `json:"network_mode"`
	Interfaces     *map[string][]IPAddress `json:"interfaces,omitempty"`
	Ports          *[]ProtoPort            `json:"ports,omitempty"`
	Applications   *[]string               `json:"applications,omitempty"`
}

type WorkloadV2Child struct {
	Brief        WorkloadBriefV2    `json:"brief"`
	Security     WorkloadSecurityV2 `json:"security"`
	Runtime      WorkloadRuntimeV2  `json:"rt_attributes"`
	EnforcerID   string             `json:"enforcer_id"`
	EnforcerName string             `json:"enforcer_name"`
	PlatformRole string             `json:"platform_role"`
	CreatedAt    string             `json:"created_at"`
	StartedAt    string             `json:"started_at"`
	FinishedAt   string             `json:"finished_at"`
	SecuredAt    string             `json:"secured_at"`
	Running      bool               `json:"running"`
	ExitCode     int32              `json:"exit_code"`
}

type WorkloadV2 struct {
	Brief        WorkloadBriefV2    `json:"brief"`
	Security     WorkloadSecurityV2 `json:"security"`
	Runtime      WorkloadRuntimeV2  `json:"rt_attributes"`
	Children     []WorkloadV2Child  `json:"children"`
	EnforcerID   string             `json:"enforcer_id"`
	EnforcerName string             `json:"enforcer_name"`
	PlatformRole string             `json:"platform_role"`
	CreatedAt    string             `json:"created_at"`
	StartedAt    string             `json:"started_at"`
	FinishedAt   string             `json:"finished_at"`
	SecuredAt    string             `json:"secured_at"`
	Running      bool               `json:"running"`
	ExitCode     int32              `json:"exit_code"`
}

type workloadsWrapV2 struct {
	Workloads []WorkloadV2 `json:"workloads"`
}

func (h *Handler) GetScannedWorkloads(c *gin.Context) {
	start, hasStart, ok := optionalInteger(c, "start")
	if !ok {
		return
	}
	var workloads []WorkloadV2
	fetched := !hasStart || start == 0
	if fetched {
		var err error
		workloads, err = h.fetchScannedWorkloads(c)
		if err != nil {
			writeScannedError(c)
			return
		}
	}

	if !hasStart {
		writeScannedJSON(c, workloadsWrapV2{Workloads: workloads})
		return
	}
	limit, hasLimit, ok := optionalInteger(c, "limit")
	if !ok {
		return
	}
	if !hasLimit {
		if fetched {
			writeScannedJSON(c, workloadsWrapV2{Workloads: workloads})
		} else {
			writeScannedJSON(c, nil)
		}
		return
	}

	key := h.scannedCacheKey(c.GetHeader("Token"))
	if fetched {
		encoded, _ := json.Marshal(workloads)
		h.scanned.Set(key, workloads, int64(len(encoded)))
	} else if cached, found := h.scanned.Get(key); found {
		workloads = cached
	} else {
		workloads = make([]WorkloadV2, 0)
	}
	page := sliceWorkloads(workloads, start, limit)
	if len(page) < limit {
		h.scanned.Delete(key)
	}
	writeScannedJSON(c, page)
}

func (h *Handler) fetchScannedWorkloads(c *gin.Context) ([]WorkloadV2, error) {
	target, err := h.resolver.Resolve(c.GetHeader("Token"), controller.V2, "workload")
	if err != nil {
		return nil, err
	}
	target.RawQuery = url.Values{"view": {"pod"}}.Encode()
	headers := make(http.Header)
	token := c.GetHeader("Token")
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
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("controller status %d", response.StatusCode)
	}
	reader := io.Reader(response.Body)
	if strings.EqualFold(response.Header.Get("Content-Encoding"), "gzip") {
		compressed, err := gzip.NewReader(response.Body)
		if err != nil {
			return nil, err
		}
		defer compressed.Close()
		reader = compressed
	}
	limited := io.LimitReader(reader, h.scanned.MaxBytes()+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > h.scanned.MaxBytes() {
		return nil, errors.New("controller workload response exceeds cache budget")
	}
	var output workloadsWrapV2
	if err := json.Unmarshal(payload, &output); err != nil {
		return nil, err
	}
	for index := range output.Workloads {
		addHiddenVulnerabilities(&output.Workloads[index])
	}
	return output.Workloads, nil
}

func addHiddenVulnerabilities(workload *WorkloadV2) {
	critical := workload.Security.ScanSummary.Critical
	high := workload.Security.ScanSummary.High
	medium := workload.Security.ScanSummary.Medium
	for index := range workload.Children {
		critical += workload.Children[index].Security.ScanSummary.Critical
		high += workload.Children[index].Security.ScanSummary.High
		medium += workload.Children[index].Security.ScanSummary.Medium
	}
	workload.Security.ScanSummary.HiddenCritical = &critical
	workload.Security.ScanSummary.HiddenHigh = &high
	workload.Security.ScanSummary.HiddenMedium = &medium
}

func (h *Handler) scannedCacheKey(token string) managerCache.Key {
	cluster, _ := h.sessions.Cluster(token)
	return managerCache.Key{Token: token, Cluster: cluster, Domain: "workload-scanned"}
}

func optionalInteger(c *gin.Context, name string) (int, bool, bool) {
	value, present := c.GetQuery(name)
	if !present {
		return 0, false, true
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		writeScannedError(c)
		return 0, true, false
	}
	return parsed, true, true
}

func sliceWorkloads(workloads []WorkloadV2, start, limit int) []WorkloadV2 {
	from := max(start, 0)
	if from > len(workloads) {
		from = len(workloads)
	}
	to := from
	if limit > 0 && limit <= len(workloads)-from {
		to = from + limit
	} else if limit > 0 {
		to = len(workloads)
	}
	return workloads[from:to]
}

func writeScannedError(c *gin.Context) {
	c.Data(http.StatusInternalServerError, "text/plain; charset=UTF-8", []byte("Internal server error"))
}

func writeScannedJSON(c *gin.Context, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		writeScannedError(c)
		return
	}
	c.Header("Content-Length", strconv.Itoa(len(body)))
	c.Data(http.StatusOK, "application/json", body)
}
