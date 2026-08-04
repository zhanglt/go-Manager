package notification

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type isoDateTime struct {
	time.Time
}

func (value *isoDateTime) UnmarshalJSON(data []byte) error {
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

func (value isoDateTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(value.Format("2006-01-02T15:04:05Z07:00"))
}

type violationBrief struct {
	ClientName string      `json:"client_name"`
	ServerName string      `json:"server_name"`
	ReportedAt isoDateTime `json:"reported_at"`
}

type violation struct {
	ClientID     string      `json:"client_id"`
	ClientName   string      `json:"client_name"`
	ServerID     string      `json:"server_id"`
	ServerName   string      `json:"server_name"`
	ServerPort   int         `json:"server_port"`
	Applications []string    `json:"applications"`
	ReportedAt   isoDateTime `json:"reported_at"`
	PolicyID     int         `json:"policy_id"`
	ClientIP     string      `json:"client_ip"`
	ServerIP     string      `json:"server_ip"`
}

type violationWrap struct {
	Violations []violation `json:"violations"`
}

type threatDTO struct {
	Name            string      `json:"name"`
	ReportedAt      isoDateTime `json:"reported_at"`
	WorkloadID      string      `json:"workload_id"`
	WorkloadName    string      `json:"workload_name"`
	Count           int         `json:"count"`
	Severity        string      `json:"severity"`
	Action          string      `json:"action"`
	SourceIP        string      `json:"src_ip"`
	DestinationIP   string      `json:"dst_ip"`
	SourcePort      int         `json:"src_port"`
	DestinationPort int         `json:"dst_port"`
	Application     string      `json:"application"`
	SessionIngress  bool        `json:"sess_ingress"`
}

type threatDTOWrap struct {
	Threats []threatDTO `json:"threats"`
}

type threatEndpoint struct {
	ID                   string      `json:"id"`
	Name                 string      `json:"name"`
	ReportedTimestamp    int64       `json:"reported_timestamp"`
	ReportedAt           isoDateTime `json:"reported_at"`
	Count                int         `json:"count"`
	ClientWorkloadID     string      `json:"client_workload_id"`
	ClientWorkloadName   string      `json:"client_workload_name"`
	ClientWorkloadDomain *string     `json:"client_workload_domain"`
	ServerWorkloadID     string      `json:"server_workload_id"`
	ServerWorkloadName   string      `json:"server_workload_name"`
	ServerWorkloadDomain *string     `json:"server_workload_domain"`
	Severity             string      `json:"severity"`
	Action               string      `json:"action"`
	ClientPort           int         `json:"client_port"`
	ServerPort           int         `json:"server_port"`
	ServerConnectionPort int         `json:"server_conn_port"`
	ClientIP             string      `json:"client_ip"`
	ServerIP             string      `json:"server_ip"`
	Application          string      `json:"application"`
	Target               string      `json:"target"`
	CaptureLength        *int        `json:"cap_len"`
	Message              string      `json:"message"`
}

type threatEndpointWrap struct {
	Threats []threatEndpoint `json:"threats"`
}

type domain struct {
	Source      *string `json:"source,omitempty"`
	Destination *string `json:"destination,omitempty"`
}

type convertedThreat struct {
	ID                        string      `json:"id"`
	Name                      string      `json:"name"`
	ReportedTimestamp         int64       `json:"reported_timestamp"`
	ReportedAt                isoDateTime `json:"reported_at"`
	Count                     int         `json:"count"`
	SourceWorkloadID          string      `json:"source_workload_id"`
	SourceWorkloadName        string      `json:"source_workload_name"`
	DestinationWorkloadID     string      `json:"destination_workload_id"`
	DestinationWorkloadName   string      `json:"destination_workload_name"`
	Domain                    domain      `json:"domain"`
	Severity                  string      `json:"severity"`
	Action                    string      `json:"action"`
	SourcePort                int         `json:"source_port"`
	DestinationPort           int         `json:"destination_port"`
	SourceConnectionPort      *int        `json:"source_conn_port,omitempty"`
	DestinationConnectionPort *int        `json:"destination_conn_port,omitempty"`
	SourceIP                  string      `json:"source_ip"`
	DestinationIP             string      `json:"destination_ip"`
	Application               string      `json:"application"`
	Target                    string      `json:"target"`
	CaptureLength             *int        `json:"cap_len,omitempty"`
	Message                   string      `json:"message"`
}

type convertedThreatWrap struct {
	Threats []convertedThreat `json:"threats"`
}

type threatBrief struct {
	Name        string `json:"name"`
	Severity    string `json:"severity"`
	Application string `json:"application"`
}

type threatBriefWrap struct {
	Threats []threatBrief `json:"threats"`
}

type threatBriefDTO struct {
	Name        string `json:"name"`
	Severity    string `json:"severity"`
	SeverityID  int    `json:"severityId"`
	Application string `json:"application"`
}

func (h *Handler) GetThreat(c *gin.Context) {
	if id, present := c.GetQuery("id"); present {
		h.transfer.Request(c, http.MethodGet, nil, nil, nil, "log", "threat", id)
		return
	}
	var input threatEndpointWrap
	if !h.fetchJSON(c, &input, "log", "threat") {
		return
	}
	output := convertedThreatWrap{Threats: make([]convertedThreat, 0, len(input.Threats))}
	for _, item := range input.Threats {
		output.Threats = append(output.Threats, convertThreat(item))
	}
	writeJSON(c, output)
}

func (h *Handler) GetThreatTop(c *gin.Context) {
	var input threatBriefWrap
	if !h.fetchJSON(c, &input, "log", "threat") {
		return
	}
	seen := make(map[threatBrief]struct{}, len(input.Threats))
	output := make([]threatBriefDTO, 0, 5)
	for _, item := range input.Threats {
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		output = append(output, threatBriefDTO{item.Name, item.Severity, severityCode(item.Severity), item.Application})
	}
	sort.SliceStable(output, func(i, j int) bool { return output[i].SeverityID > output[j].SeverityID })
	if len(output) > 5 {
		output = output[:5]
	}
	for len(output) < 5 {
		output = append(output, threatBriefDTO{Name: " "})
	}
	writeJSON(c, output)
}

func (h *Handler) TrackThreat(c *gin.Context) {
	var brief violationBrief
	if err := json.NewDecoder(c.Request.Body).Decode(&brief); err != nil {
		writeInvalidBody(c)
		return
	}
	var input threatDTOWrap
	if !h.fetchJSON(c, &input, "log", "threat") {
		return
	}
	output := make([]threatDTO, 0)
	for _, item := range input.Threats {
		workload := brief.ClientName
		if item.SessionIngress {
			workload = brief.ServerName
		}
		if item.WorkloadID == workload && withinTwoHours(item.ReportedAt.Time, brief.ReportedAt.Time) {
			output = append(output, item)
		}
	}
	writeJSON(c, output)
}

func (h *Handler) TrackViolation(c *gin.Context) {
	var brief violationBrief
	if err := json.NewDecoder(c.Request.Body).Decode(&brief); err != nil {
		writeInvalidBody(c)
		return
	}
	var input violationWrap
	if !h.fetchJSON(c, &input, "log", "violation") {
		return
	}
	output := make([]violation, 0)
	for _, item := range input.Violations {
		if item.ClientID == brief.ClientName && item.ServerID == brief.ServerName && withinTwoHours(item.ReportedAt.Time, brief.ReportedAt.Time) {
			output = append(output, item)
		}
	}
	writeJSON(c, output)
}

func (h *Handler) fetchJSON(c *gin.Context, output any, segments ...string) bool {
	response, err := h.transfer.Fetch(c, http.MethodGet, nil, nil, nil, segments...)
	if err != nil {
		writeNotificationError(c)
		return false
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		writeNotificationError(c)
		return false
	}
	var reader io.Reader = response.Body
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Encoding")), "gzip") {
		compressed, gzipErr := gzip.NewReader(response.Body)
		if gzipErr != nil {
			writeNotificationError(c)
			return false
		}
		defer compressed.Close()
		reader = compressed
	}
	if err := json.NewDecoder(reader).Decode(output); err != nil {
		writeNotificationError(c)
		return false
	}
	return true
}

func convertThreat(input threatEndpoint) convertedThreat {
	reverse := input.Target == "client"
	sourceID, sourceName, sourceIP, sourceDomain, sourcePort := input.ClientWorkloadID, input.ClientWorkloadName, input.ClientIP, input.ClientWorkloadDomain, input.ClientPort
	destinationID, destinationName, destinationIP, destinationDomain, destinationPort := input.ServerWorkloadID, input.ServerWorkloadName, input.ServerIP, input.ServerWorkloadDomain, input.ServerPort
	var sourceConnectionPort, destinationConnectionPort *int
	if reverse {
		sourceID, destinationID = destinationID, sourceID
		sourceName, destinationName = destinationName, sourceName
		sourceIP, destinationIP = destinationIP, sourceIP
		sourceDomain, destinationDomain = destinationDomain, sourceDomain
		sourcePort, destinationPort = destinationPort, sourcePort
		sourceConnectionPort = intPointer(input.ServerConnectionPort)
	} else {
		destinationConnectionPort = intPointer(input.ServerConnectionPort)
	}
	return convertedThreat{
		ID: input.ID, Name: input.Name, ReportedTimestamp: input.ReportedTimestamp, ReportedAt: input.ReportedAt,
		Count: input.Count, SourceWorkloadID: sourceID, SourceWorkloadName: endpointName(sourceName, sourceIP, sourceID),
		DestinationWorkloadID: destinationID, DestinationWorkloadName: endpointName(destinationName, destinationIP, destinationID),
		Domain: domain{Source: sourceDomain, Destination: destinationDomain}, Severity: input.Severity, Action: input.Action,
		SourcePort: sourcePort, DestinationPort: destinationPort, SourceConnectionPort: sourceConnectionPort,
		DestinationConnectionPort: destinationConnectionPort, SourceIP: sourceIP, DestinationIP: destinationIP,
		Application: input.Application, Target: input.Target, CaptureLength: input.CaptureLength, Message: input.Message,
	}
}

func endpointName(name, address, id string) string {
	if name != "" {
		return name
	}
	if address != "" {
		return address
	}
	return id
}

func intPointer(value int) *int { return &value }

func severityCode(value string) int {
	if code, exists := map[string]int{"Critical": 5, "high": 4, "medium": 3, "low": 2, "info": 1}[value]; exists {
		return code
	}
	return 1
}

func withinTwoHours(value, center time.Time) bool {
	return value.After(center.Add(-2*time.Hour)) && value.Before(center.Add(2*time.Hour))
}

func writeJSON(c *gin.Context, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		writeNotificationError(c)
		return
	}
	c.Data(http.StatusOK, "application/json", payload)
}

func writeInvalidBody(c *gin.Context) {
	c.String(http.StatusBadRequest, "The request content was malformed")
}

func writeNotificationError(c *gin.Context) {
	c.String(http.StatusInternalServerError, "Internal server error")
}
