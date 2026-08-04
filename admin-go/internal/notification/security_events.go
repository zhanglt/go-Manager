package notification

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"
)

type securityEventEndpoint struct {
	DomainName           *string `json:"domain_name,omitempty"`
	WorkloadID           *string `json:"workload_id,omitempty"`
	WorkloadName         *string `json:"workload_name,omitempty"`
	IP                   *string `json:"ip,omitempty"`
	Port                 *int    `json:"port,omitempty"`
	ServerConnectionPort *int    `json:"server_conn_port,omitempty"`
}

type securityEvent struct {
	Name              string                `json:"name"`
	SecurityEventType string                `json:"security_event_type"`
	Level             string                `json:"level"`
	Source            securityEventEndpoint `json:"source"`
	Destination       securityEventEndpoint `json:"destination"`
	HostName          *string               `json:"host_name,omitempty"`
	Applications      []string              `json:"applications"`
	Details           string                `json:"details"`
	ReportedTimestamp int64                 `json:"reported_timestamp"`
	ReportedAt        isoDateTime           `json:"reported_at"`
}

type securityEventDTO struct {
	SecurityEvents []securityEvent `json:"securityEvents"`
}

type threatSecurityInput struct {
	Name                 string      `json:"name"`
	ReportedTimestamp    int64       `json:"reported_timestamp"`
	ReportedAt           isoDateTime `json:"reported_at"`
	Level                string      `json:"level"`
	ClientWorkloadID     string      `json:"client_workload_id"`
	ClientWorkloadName   string      `json:"client_workload_name"`
	ClientWorkloadDomain string      `json:"client_workload_domain"`
	ServerWorkloadID     string      `json:"server_workload_id"`
	ServerWorkloadName   string      `json:"server_workload_name"`
	ServerWorkloadDomain string      `json:"server_workload_domain"`
	ClientPort           int         `json:"client_port"`
	ServerPort           int         `json:"server_port"`
	ServerConnectionPort int         `json:"server_conn_port"`
	ClientIP             string      `json:"client_ip"`
	ServerIP             string      `json:"server_ip"`
	Target               string      `json:"target"`
	Application          string      `json:"application"`
	ID                   string      `json:"id"`
	HostName             string      `json:"host_name"`
	ClusterName          string      `json:"cluster_name"`
	Count                int         `json:"count"`
	Severity             string      `json:"severity"`
	Action               string      `json:"action"`
	CaptureLength        *int        `json:"cap_len"`
	Message              string      `json:"message"`
}

type threatSecurityDetails struct {
	ID            string `json:"id"`
	HostName      string `json:"host_name"`
	ClusterName   string `json:"cluster_name"`
	Count         int    `json:"count"`
	Severity      string `json:"severity"`
	Action        string `json:"action"`
	CaptureLength *int   `json:"cap_len"`
	Message       string `json:"message"`
}

type violationSecurityInput struct {
	PolicyID          int         `json:"policy_id"`
	ReportedTimestamp int64       `json:"reported_timestamp"`
	ReportedAt        isoDateTime `json:"reported_at"`
	Level             string      `json:"level"`
	ClientID          string      `json:"client_id"`
	ClientName        string      `json:"client_name"`
	ClientDomain      string      `json:"client_domain"`
	ServerID          string      `json:"server_id"`
	ServerName        string      `json:"server_name"`
	ServerDomain      string      `json:"server_domain"`
	ClientIP          string      `json:"client_ip"`
	ServerIP          string      `json:"server_ip"`
	ServerPort        int         `json:"server_port"`
	Applications      []string    `json:"applications"`
	ClusterName       string      `json:"cluster_name"`
	ClientImage       string      `json:"client_image"`
	ServerImage       string      `json:"server_image"`
	IPProtocol        int         `json:"ip_proto"`
	Servers           *[]string   `json:"servers"`
	Sessions          int         `json:"sessions"`
	PolicyAction      string      `json:"policy_action"`
}

type violationSecurityDetails struct {
	ClusterName  string    `json:"cluster_name"`
	ClientImage  string    `json:"client_image"`
	ServerImage  string    `json:"server_image"`
	ServerPort   int       `json:"server_port"`
	IPProtocol   int       `json:"ip_proto"`
	Servers      *[]string `json:"servers"`
	Sessions     int       `json:"sessions"`
	PolicyAction string    `json:"policy_action"`
}

type incidentSecurityInput struct {
	Name                 string      `json:"name"`
	Level                string      `json:"level"`
	HostName             *string     `json:"host_name"`
	WorkloadID           *string     `json:"workload_id"`
	WorkloadName         *string     `json:"workload_name"`
	WorkloadDomain       *string     `json:"workload_domain"`
	RemoteWorkloadID     *string     `json:"remote_workload_id"`
	RemoteWorkloadName   *string     `json:"remote_workload_name"`
	RemoteWorkloadDomain *string     `json:"remote_workload_domain"`
	ClientIP             *string     `json:"client_ip"`
	ServerIP             *string     `json:"server_ip"`
	ClientPort           *int        `json:"client_port"`
	ServerPort           *int        `json:"server_port"`
	ServerConnectionPort *int        `json:"server_conn_port"`
	ConnectionIngress    *bool       `json:"conn_ingress"`
	ProcessPath          *string     `json:"proc_path"`
	ReportedTimestamp    int64       `json:"reported_timestamp"`
	ReportedAt           isoDateTime `json:"reported_at"`
	ClusterName          string      `json:"cluster_name"`
	EtherType            int         `json:"ether_type"`
	IPProtocol           int         `json:"ip_proto"`
	ProcessName          *string     `json:"proc_name"`
	ProcessCommand       *string     `json:"proc_cmd"`
	ProcessRealUID       *int        `json:"proc_real_uid"`
	ProcessEffectiveUID  *int        `json:"proc_effective_uid"`
	ProcessRealUser      *string     `json:"proc_real_user"`
	ProcessEffectiveUser *string     `json:"proc_effective_user"`
	FilePath             *string     `json:"file_path"`
	FileName             *[]string   `json:"file_name"`
	Message              string      `json:"message"`
}

type incidentSecurityDetails struct {
	HostName             string    `json:"host_name"`
	ClusterName          string    `json:"cluster_name"`
	EtherType            int       `json:"ether_type"`
	IPProtocol           int       `json:"ip_proto"`
	ProcessName          *string   `json:"proc_name"`
	ProcessPath          *string   `json:"proc_path"`
	ProcessCommand       *string   `json:"proc_cmd"`
	ProcessRealUID       *int      `json:"proc_real_uid"`
	ProcessEffectiveUID  *int      `json:"proc_effective_uid"`
	ProcessRealUser      *string   `json:"proc_real_user"`
	ProcessEffectiveUser *string   `json:"proc_effective_user"`
	FilePath             *string   `json:"file_path"`
	FileName             *[]string `json:"file_name"`
	Message              string    `json:"message"`
}

func (h *Handler) GetSecurityEvents(c *gin.Context) {
	payloads, ok := h.fetchSecurityEventPayloads(c)
	if !ok {
		return
	}
	var threats struct {
		Threats []threatSecurityInput `json:"threats"`
	}
	var violations struct {
		Violations []violationSecurityInput `json:"violations"`
	}
	var incidents struct {
		Incidents []incidentSecurityInput `json:"incidents"`
	}
	if json.Unmarshal(payloads[0], &threats) != nil || json.Unmarshal(payloads[1], &violations) != nil || json.Unmarshal(payloads[2], &incidents) != nil {
		writeNotificationError(c)
		return
	}

	events := make([]securityEvent, 0, len(threats.Threats)+len(violations.Violations)+len(incidents.Incidents))
	for _, item := range threats.Threats {
		events = append(events, threatSecurityEvent(item))
	}
	for _, item := range violations.Violations {
		events = append(events, violationSecurityEvent(item))
	}
	for _, item := range incidents.Incidents {
		events = append(events, incidentSecurityEvent(item))
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].ReportedTimestamp > events[j].ReportedTimestamp })
	writeJSON(c, securityEventDTO{SecurityEvents: events})
}

func threatSecurityEvent(item threatSecurityInput) securityEvent {
	client := securityEndpoint(item.ClientWorkloadDomain, item.ClientWorkloadID, item.ClientWorkloadName, item.ClientIP, item.ClientPort, nil)
	serverConnectionPort := item.ServerConnectionPort
	server := securityEndpoint(item.ServerWorkloadDomain, item.ServerWorkloadID, item.ServerWorkloadName, item.ServerIP, item.ServerPort, &serverConnectionPort)
	source, destination := client, server
	if item.Target == "client" {
		source, destination = server, client
	}
	applications := make([]string, 0, 1)
	if item.Application != "" {
		applications = append(applications, item.Application)
	}
	detailValues := map[string]any{
		"action": item.Action, "cluster_name": item.ClusterName, "count": item.Count, "host_name": item.HostName,
		"id": item.ID, "message": item.Message, "severity": item.Severity,
	}
	if item.CaptureLength != nil {
		detailValues["cap_len"] = *item.CaptureLength
	}
	details, _ := json.Marshal(detailValues)
	return securityEvent{item.Name, "Threat", item.Level, source, destination, nil, applications, string(details), item.ReportedTimestamp, item.ReportedAt}
}

func violationSecurityEvent(item violationSecurityInput) securityEvent {
	client := securityEndpoint(item.ClientDomain, item.ClientID, item.ClientName, item.ClientIP, 0, nil)
	client.Port = nil
	server := securityEndpoint(item.ServerDomain, item.ServerID, item.ServerName, item.ServerIP, item.ServerPort, nil)
	detailValues := map[string]any{
		"client_image": item.ClientImage, "cluster_name": item.ClusterName, "ip_proto": item.IPProtocol,
		"policy_action": item.PolicyAction, "server_image": item.ServerImage, "server_port": item.ServerPort, "sessions": item.Sessions,
	}
	if item.Servers != nil {
		detailValues["servers"] = *item.Servers
	}
	details, _ := json.Marshal(detailValues)
	return securityEvent{strconv.Itoa(item.PolicyID), "Violation", item.Level, client, server, nil, item.Applications, string(details), item.ReportedTimestamp, item.ReportedAt}
}

func incidentSecurityEvent(item incidentSecurityInput) securityEvent {
	client := securityEndpoint(valueOrZero(item.WorkloadDomain), valueOrZero(item.WorkloadID), valueOrZero(item.WorkloadName), valueOrZero(item.ClientIP), intOrZero(item.ClientPort), nil)
	serverConnectionPort := intOrZero(item.ServerConnectionPort)
	server := securityEndpoint(valueOrZero(item.RemoteWorkloadDomain), valueOrZero(item.RemoteWorkloadID), valueOrZero(item.RemoteWorkloadName), valueOrZero(item.ServerIP), intOrZero(item.ServerPort), &serverConnectionPort)
	source, destination := client, server
	if item.ConnectionIngress != nil && *item.ConnectionIngress {
		source, destination = server, client
	}
	hostName := valueOrZero(item.HostName)
	applications := make([]string, 0, 1)
	if item.ProcessPath != nil {
		applications = append(applications, *item.ProcessPath)
	}
	detailValues := map[string]any{
		"cluster_name": item.ClusterName, "ether_type": item.EtherType, "host_name": hostName,
		"ip_proto": item.IPProtocol, "message": item.Message,
	}
	addOptionalDetail(detailValues, "proc_name", item.ProcessName)
	addOptionalDetail(detailValues, "proc_path", item.ProcessPath)
	addOptionalDetail(detailValues, "proc_cmd", item.ProcessCommand)
	addOptionalDetail(detailValues, "proc_real_uid", item.ProcessRealUID)
	addOptionalDetail(detailValues, "proc_effective_uid", item.ProcessEffectiveUID)
	addOptionalDetail(detailValues, "proc_real_user", item.ProcessRealUser)
	addOptionalDetail(detailValues, "proc_effective_user", item.ProcessEffectiveUser)
	addOptionalDetail(detailValues, "file_path", item.FilePath)
	addOptionalDetail(detailValues, "file_name", item.FileName)
	details, _ := json.Marshal(detailValues)
	return securityEvent{item.Name, "Incident", item.Level, source, destination, &hostName, applications, string(details), item.ReportedTimestamp, item.ReportedAt}
}

func securityEndpoint(domain, id, name, ip string, port int, connectionPort *int) securityEventEndpoint {
	return securityEventEndpoint{&domain, &id, &name, &ip, &port, connectionPort}
}

func valueOrZero(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func intOrZero(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func addOptionalDetail[T any](details map[string]any, key string, value *T) {
	if value != nil {
		details[key] = *value
	}
}

func (h *Handler) fetchSecurityEventPayloads(c *gin.Context) ([3][]byte, bool) {
	var payloads [3][]byte
	var statuses [3]int
	var fetchErrors [3]error
	var wait sync.WaitGroup
	for index, resource := range []string{"threat", "violation", "incident"} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			payloads[index], statuses[index], fetchErrors[index] = h.fetchNotificationRaw(c, http.MethodGet, "log", resource)
		}()
	}
	wait.Wait()
	for index := range payloads {
		if fetchErrors[index] != nil || statuses[index] != http.StatusOK {
			writeNotificationError(c)
			return payloads, false
		}
	}
	return payloads, true
}
