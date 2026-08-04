package dashboard

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type dashboardDateTime struct{ time.Time }

func (value *dashboardDateTime) UnmarshalJSON(data []byte) error {
	var input string
	if err := json.Unmarshal(data, &input); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339, input)
	if err != nil {
		return fmt.Errorf("invalid compact ISO-8601 date: %w", err)
	}
	value.Time = parsed
	return nil
}

func (value dashboardDateTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(value.Format("2006-01-02T15:04:05Z07:00"))
}

type dashboardThreat struct {
	Name                 string            `json:"name"`
	HostName             string            `json:"host_name"`
	Level                string            `json:"level"`
	ClientWorkloadID     string            `json:"client_workload_id"`
	ClientWorkloadName   *string           `json:"client_workload_name"`
	ClientWorkloadDomain *string           `json:"client_workload_domain"`
	ClientIP             *string           `json:"client_ip"`
	ClientPort           *int              `json:"client_port"`
	ServerWorkloadID     string            `json:"server_workload_id"`
	ServerWorkloadName   *string           `json:"server_workload_name"`
	ServerWorkloadDomain *string           `json:"server_workload_domain"`
	ServerIP             *string           `json:"server_ip"`
	ServerPort           *int              `json:"server_port"`
	ServerConnectionPort *int              `json:"server_conn_port"`
	Application          *string           `json:"application"`
	Target               *string           `json:"target"`
	ReportedAt           dashboardDateTime `json:"reported_at"`
}

type dashboardViolation struct {
	PolicyID     int               `json:"policy_id"`
	HostName     string            `json:"host_name"`
	Level        string            `json:"level"`
	ClientID     string            `json:"client_id"`
	ClientIP     *string           `json:"client_ip"`
	ClientName   *string           `json:"client_name"`
	ClientDomain *string           `json:"client_domain"`
	ServerID     string            `json:"server_id"`
	ServerIP     *string           `json:"server_ip"`
	ServerName   *string           `json:"server_name"`
	ServerDomain *string           `json:"server_domain"`
	ServerPort   *int              `json:"server_port"`
	Application  *string           `json:"application"`
	ReportedAt   dashboardDateTime `json:"reported_at"`
}

type dashboardIncident struct {
	Name                 string            `json:"name"`
	HostName             string            `json:"host_name"`
	Level                string            `json:"level"`
	WorkloadID           *string           `json:"workload_id"`
	WorkloadName         *string           `json:"workload_name"`
	WorkloadDomain       *string           `json:"workload_domain"`
	ClientIP             *string           `json:"client_ip"`
	ClientPort           *int              `json:"client_port"`
	RemoteWorkloadID     *string           `json:"remote_workload_id"`
	RemoteWorkloadName   *string           `json:"remote_workload_name"`
	RemoteWorkloadDomain *string           `json:"remote_workload_domain"`
	ServerIP             *string           `json:"server_ip"`
	ServerPort           *int              `json:"server_port"`
	ServerConnectionPort *int              `json:"server_conn_port"`
	ConnectionIngress    *bool             `json:"conn_ingress"`
	ReportedAt           dashboardDateTime `json:"reported_at"`
}

type convertedDashboardEvent struct {
	PolicyID                *int              `json:"policy_id,omitempty"`
	Name                    *string           `json:"name,omitempty"`
	SourceWorkloadID        *string           `json:"source_workload_id,omitempty"`
	SourceWorkloadName      *string           `json:"source_workload_name,omitempty"`
	DestinationWorkloadID   *string           `json:"destination_workload_id,omitempty"`
	DestinationWorkloadName *string           `json:"destination_workload_name,omitempty"`
	SourceDomain            *string           `json:"source_domain,omitempty"`
	DestinationDomain       *string           `json:"destination_domain,omitempty"`
	Level                   string            `json:"level"`
	HostName                string            `json:"host_name"`
	SourcePort              *int              `json:"source_port,omitempty"`
	DestinationPort         *int              `json:"destination_port,omitempty"`
	ServerConnectionPort    *int              `json:"server_conn_port,omitempty"`
	SourceIP                *string           `json:"source_ip,omitempty"`
	DestinationIP           *string           `json:"destination_ip,omitempty"`
	Application             *string           `json:"application,omitempty"`
	ReportedAt              dashboardDateTime `json:"reported_at"`
}

type criticalDashboardEvents struct {
	Summary           map[string][][]any `json:"summary"`
	TopSecurityEvents struct {
		Source      [][]convertedDashboardEvent `json:"source"`
		Destination [][]convertedDashboardEvent `json:"destination"`
	} `json:"top_security_events"`
}

func (h *Handler) GetNotifications(c *gin.Context) {
	resources := []string{"threat", "violation", "incident"}
	if domain, present := c.GetQuery("domain"); present && domain != "" {
		resources = []string{"threatf_domain=" + domain, "violationf_domain=" + domain, "incidentf_domain=" + domain}
	}
	payloads, ok := h.fetchDashboardNotifications(c, resources)
	if !ok {
		return
	}
	var threats struct {
		Threats []dashboardThreat `json:"threats"`
	}
	var violations struct {
		Violations []dashboardViolation `json:"violations"`
	}
	var incidents struct {
		Incidents []dashboardIncident `json:"incidents"`
	}
	if json.Unmarshal(payloads[0], &threats) != nil || json.Unmarshal(payloads[1], &violations) != nil || json.Unmarshal(payloads[2], &incidents) != nil {
		c.String(http.StatusInternalServerError, "Internal server error")
		return
	}
	events := make([]convertedDashboardEvent, 0, len(threats.Threats)+len(violations.Violations)+len(incidents.Incidents))
	for _, item := range threats.Threats {
		events = append(events, convertDashboardThreat(item))
	}
	for _, item := range violations.Violations {
		events = append(events, convertDashboardViolation(item))
	}
	for _, item := range incidents.Incidents {
		events = append(events, convertDashboardIncident(item))
	}
	result := criticalDashboardEvents{Summary: dashboardEventSummary(events)}
	result.TopSecurityEvents.Source = topDashboardEvents(events, func(event convertedDashboardEvent) *string { return event.SourceWorkloadName })
	result.TopSecurityEvents.Destination = topDashboardEvents(events, func(event convertedDashboardEvent) *string { return event.DestinationWorkloadName })
	payload, err := json.Marshal(map[string]any{"criticalSecurityEvents": result})
	if err != nil {
		c.String(http.StatusInternalServerError, "Internal server error")
		return
	}
	c.Data(http.StatusOK, "application/json", payload)
}

func (h *Handler) fetchDashboardNotifications(c *gin.Context, resources []string) ([3][]byte, bool) {
	var payloads [3][]byte
	var statuses [3]int
	var errs [3]error
	var wait sync.WaitGroup
	for index, resource := range resources {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, err := h.transfer.Fetch(c, http.MethodGet, nil, http.Header{"X-Nv-Page": {"dashboard"}}, nil, "log", resource)
			if err != nil {
				errs[index] = err
				return
			}
			defer response.Body.Close()
			statuses[index] = response.StatusCode
			var reader io.Reader = response.Body
			if strings.Contains(strings.ToLower(response.Header.Get("Content-Encoding")), "gzip") {
				compressed, gzipErr := gzip.NewReader(response.Body)
				if gzipErr != nil {
					errs[index] = gzipErr
					return
				}
				defer compressed.Close()
				reader = compressed
			}
			payloads[index], errs[index] = io.ReadAll(io.LimitReader(reader, h.maxBytes+1))
			if int64(len(payloads[index])) > h.maxBytes {
				errs[index] = fmt.Errorf("dashboard notification response exceeds decode budget")
			}
		}()
	}
	wait.Wait()
	for index := range payloads {
		if errs[index] != nil || statuses[index] != http.StatusOK {
			c.String(http.StatusInternalServerError, "Internal server error")
			return payloads, false
		}
	}
	return payloads, true
}

func convertDashboardThreat(item dashboardThreat) convertedDashboardEvent {
	isClient := item.Target != nil && *item.Target == "client"
	sourceName := displayName(item.ClientWorkloadName, item.ClientIP, item.ClientWorkloadID)
	destinationName := displayName(item.ServerWorkloadName, item.ServerIP, item.ServerWorkloadID)
	sourceDomain, destinationDomain := item.ClientWorkloadDomain, item.ServerWorkloadDomain
	sourcePort, destinationPort := item.ClientPort, item.ServerPort
	sourceIP, destinationIP := item.ClientIP, item.ServerIP
	if isClient {
		sourceName = displayName(item.ServerWorkloadName, item.ServerIP, item.ServerWorkloadID)
		destinationName = displayName(item.ClientWorkloadName, item.ClientIP, item.ClientWorkloadID)
		sourceDomain, destinationDomain = item.ServerWorkloadDomain, item.ClientWorkloadDomain
		sourcePort, destinationPort = item.ServerPort, item.ClientPort
		sourceIP, destinationIP = item.ServerIP, item.ClientIP
	}
	name := item.Name
	sourceID, destinationID := item.ServerWorkloadID, item.ServerWorkloadID
	return convertedDashboardEvent{nil, &name, &sourceID, sourceName, &destinationID, destinationName, sourceDomain, destinationDomain, item.Level, item.HostName, sourcePort, destinationPort, item.ServerConnectionPort, sourceIP, destinationIP, item.Application, item.ReportedAt}
}

func convertDashboardViolation(item dashboardViolation) convertedDashboardEvent {
	sourceName := displayName(item.ClientName, item.ClientIP, item.ClientID)
	destinationName := displayName(item.ServerName, item.ServerIP, item.ServerID)
	sourceID, destinationID := item.ClientID, item.ServerID
	return convertedDashboardEvent{&item.PolicyID, nil, &sourceID, sourceName, &destinationID, destinationName, item.ClientDomain, item.ServerDomain, item.Level, item.HostName, nil, item.ServerPort, nil, item.ClientIP, item.ServerIP, item.Application, item.ReportedAt}
}

func convertDashboardIncident(item dashboardIncident) convertedDashboardEvent {
	sourceName, destinationName := incidentNames(item)
	sourceDomain, destinationDomain := item.WorkloadDomain, item.RemoteWorkloadDomain
	sourcePort, destinationPort := firstInt(item.ClientPort, item.ServerPort), firstInt(item.ClientPort, item.ServerPort)
	sourceIP, destinationIP := firstString(item.ClientIP, item.ServerIP), firstString(item.ClientIP, item.ServerIP)
	if item.ConnectionIngress != nil {
		if *item.ConnectionIngress {
			sourceDomain, destinationDomain = item.RemoteWorkloadDomain, item.WorkloadDomain
			sourcePort, destinationPort = item.ServerPort, item.ClientPort
			sourceIP, destinationIP = item.ServerIP, item.ClientIP
		} else {
			sourcePort, destinationPort = item.ClientPort, item.ServerPort
			sourceIP, destinationIP = item.ClientIP, item.ServerIP
		}
	} else if item.WorkloadDomain == nil {
		sourceDomain = item.RemoteWorkloadDomain
		destinationDomain = nil
	} else {
		destinationDomain = nil
	}
	name := item.Name
	return convertedDashboardEvent{nil, &name, item.WorkloadID, sourceName, item.RemoteWorkloadID, destinationName, sourceDomain, destinationDomain, item.Level, item.HostName, sourcePort, destinationPort, item.ServerConnectionPort, sourceIP, destinationIP, nil, item.ReportedAt}
}

func incidentNames(item dashboardIncident) (*string, *string) {
	local := displayOptionalName(item.WorkloadID, item.WorkloadName, item.ClientIP)
	remote := displayOptionalName(item.RemoteWorkloadID, item.RemoteWorkloadName, item.ServerIP)
	if item.ConnectionIngress != nil {
		if *item.ConnectionIngress {
			return remote, local
		}
		return local, remote
	}
	fallback := local
	if fallback == nil {
		fallback = remote
	}
	if fallback == nil {
		value := "Host: " + item.HostName
		fallback = &value
	}
	return fallback, fallback
}

func displayName(name, ip *string, id string) *string {
	if name != nil && *name != "" {
		return name
	}
	if ip != nil && *ip != "" {
		return ip
	}
	return &id
}

func displayOptionalName(id, name, ip *string) *string {
	if id == nil {
		return nil
	}
	return displayName(name, ip, *id)
}

func firstString(first, second *string) *string {
	if first != nil {
		return first
	}
	return second
}

func firstInt(first, second *int) *int {
	if first != nil {
		return first
	}
	return second
}

func dashboardEventSummary(events []convertedDashboardEvent) map[string][][]any {
	dates := make(map[string]struct{})
	for _, event := range events {
		dates[event.ReportedAt.Format("2006-01-02")] = struct{}{}
	}
	ordered := make([]string, 0, len(dates))
	for date := range dates {
		ordered = append(ordered, date)
	}
	sort.Strings(ordered)
	result := map[string][][]any{"critical": {}, "warning": {}}
	for _, level := range []string{"critical", "warning"} {
		for _, date := range ordered {
			count := 0
			for _, event := range events {
				if event.ReportedAt.Format("2006-01-02") == date && strings.EqualFold(event.Level, level) {
					count++
				}
			}
			result[level] = append(result[level], []any{date, count})
		}
	}
	return result
}

func topDashboardEvents(events []convertedDashboardEvent, key func(convertedDashboardEvent) *string) [][]convertedDashboardEvent {
	groups := make(map[string][]convertedDashboardEvent)
	order := make([]string, 0)
	for _, event := range events {
		value := "<none>"
		if name := key(event); name != nil {
			value = "<some>" + *name
		}
		if _, exists := groups[value]; !exists {
			order = append(order, value)
		}
		groups[value] = append(groups[value], event)
	}
	sort.SliceStable(order, func(i, j int) bool { return len(groups[order[i]]) > len(groups[order[j]]) })
	if len(order) > 5 {
		order = order[:5]
	}
	result := make([][]convertedDashboardEvent, 0, len(order))
	for _, value := range order {
		result = append(result, groups[value])
	}
	return result
}
