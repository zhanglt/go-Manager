package workload

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	managerCache "github.com/neuvector/manager/admin-go/internal/cache"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/scanreport"
	"github.com/neuvector/manager/admin-go/internal/session"
)

type Handler struct {
	controller *controller.Client
	resolver   *controller.TargetResolver
	sessions   *session.Store
	scanned    *managerCache.Store[WorkloadV2]
}

type quarantineRequest struct {
	ID         string `json:"id"`
	Quarantine bool   `json:"quarantine"`
}

type namespaceConfig struct {
	Name string    `json:"name"`
	Tags *[]string `json:"tags,omitempty"`
}

type domainConfig struct {
	TagPerDomain *bool `json:"tag_per_domain,omitempty"`
}

type snifferParam struct {
	FileNumber *int    `json:"file_number,omitempty"`
	Duration   *int    `json:"duration,omitempty"`
	Filter     *string `json:"filter,omitempty"`
}

type snifferParamWrap struct {
	Sniffer snifferParam `json:"sniffer"`
}

type snifferData struct {
	WorkloadID       string           `json:"workloadId"`
	SnifferParamWrap snifferParamWrap `json:"snifferParamWarp"`
}

func NewHandler(client *controller.Client, resolver *controller.TargetResolver, sessions *session.Store, stores ...*managerCache.Store[WorkloadV2]) *Handler {
	var scanned *managerCache.Store[WorkloadV2]
	if len(stores) > 0 {
		scanned = stores[0]
	} else {
		scanned = managerCache.New[WorkloadV2](1000, 64*1024*1024, 5*time.Minute)
	}
	return &Handler{controller: client, resolver: resolver, sessions: sessions, scanned: scanned}
}

func (h *Handler) GetWorkloads(c *gin.Context) {
	if id, present := c.GetQuery("id"); present {
		h.proxy(c, http.MethodGet, nil, nil, "workload", id, "stats")
		return
	}
	h.proxy(c, http.MethodGet, nil, url.Values{"view": {"pod"}}, "workload")
}

func (h *Handler) UpdateWorkload(c *gin.Context) {
	var input quarantineRequest
	if !decodeBody(c, &input) {
		return
	}
	if input.ID == "" {
		writeBadRequest(c, "workload id is required")
		return
	}
	body, _ := json.Marshal(struct {
		Config struct {
			Quarantine bool `json:"quarantine"`
		} `json:"config"`
	}{Config: struct {
		Quarantine bool `json:"quarantine"`
	}{Quarantine: input.Quarantine}})
	h.proxy(c, http.MethodPatch, body, nil, "workload", input.ID)
}

func (h *Handler) GetWorkloadByID(c *gin.Context) {
	id, ok := requiredQuery(c, "id")
	if ok {
		h.proxy(c, http.MethodGet, nil, nil, "workload", id)
	}
}

func (h *Handler) UpdateMonitor(c *gin.Context) {
	id, ok := requiredQuery(c, "id")
	if !ok {
		return
	}
	monitor, ok := requiredQuery(c, "monitor")
	if !ok {
		return
	}
	body := []byte(`{"config": {"monitor": ` + monitor + `}}`)
	h.proxy(c, http.MethodPatch, body, nil, "workload", id)
}

func (h *Handler) GetCompliance(c *gin.Context) {
	id, ok := requiredQuery(c, "id")
	if ok {
		h.proxy(c, http.MethodGet, nil, nil, "workload", id, "compliance")
	}
}

func (h *Handler) GetScanReport(c *gin.Context) {
	var input scanreport.Request
	if !decodeBody(c, &input) {
		return
	}
	body, _ := json.Marshal(input)
	h.proxy(c, http.MethodPost, body, nil, "scan", "workloads", "scan_report")
}

func (h *Handler) GetContainers(c *gin.Context) {
	query := url.Values{"view": {"pod"}}
	segments := []string{"workload"}
	if id, present := c.GetQuery("id"); present {
		segments = append(segments, id)
	}
	h.proxy(c, http.MethodGet, nil, query, segments...)
}

func (h *Handler) GetProcesses(c *gin.Context) {
	h.getWorkloadResource(c, "process")
}

func (h *Handler) GetProcessHistory(c *gin.Context) {
	h.getWorkloadResource(c, "process_history")
}

func (h *Handler) getWorkloadResource(c *gin.Context, resource string) {
	id, ok := requiredQuery(c, "id")
	if ok {
		h.proxy(c, http.MethodGet, nil, nil, "workload", id, resource)
	}
}

func (h *Handler) GetDomains(c *gin.Context) {
	h.proxy(c, http.MethodGet, nil, nil, "domain")
}

func (h *Handler) UpdateDomain(c *gin.Context) {
	var input namespaceConfig
	if !decodeBody(c, &input) {
		return
	}
	if input.Name == "" {
		writeBadRequest(c, "domain name is required")
		return
	}
	body, _ := json.Marshal(struct {
		Config namespaceConfig `json:"config"`
	}{Config: input})
	h.proxy(c, http.MethodPatch, body, nil, "domain", input.Name)
}

func (h *Handler) UpdateDomainSettings(c *gin.Context) {
	var input domainConfig
	if !decodeBody(c, &input) {
		return
	}
	body, _ := json.Marshal(struct {
		Config domainConfig `json:"config"`
	}{Config: input})
	h.proxy(c, http.MethodPatch, body, nil, "domain")
}

func (h *Handler) GetSniffers(c *gin.Context) {
	id, ok := requiredQuery(c, "id")
	if ok {
		h.proxy(c, http.MethodGet, nil, url.Values{"f_workload": {id}}, "sniffer")
	}
}

func (h *Handler) CreateSniffer(c *gin.Context) {
	var input snifferData
	if !decodeBody(c, &input) {
		return
	}
	if input.WorkloadID == "" {
		writeBadRequest(c, "sniffer workloadId is required")
		return
	}
	body, _ := json.Marshal(input.SnifferParamWrap)
	h.proxy(c, http.MethodPost, body, url.Values{"f_workload": {input.WorkloadID}}, "sniffer")
}

func (h *Handler) StopSniffer(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			c.Data(http.StatusRequestEntityTooLarge, "text/plain; charset=UTF-8", []byte("Request entity too large"))
		} else {
			writeInternalError(c)
		}
		return
	}
	id := strings.TrimSpace(string(body))
	if id == "" {
		writeBadRequest(c, "sniffer id is required")
		return
	}
	h.proxy(c, http.MethodPatch, nil, nil, "sniffer", "stop", id)
}

func (h *Handler) DeleteSniffer(c *gin.Context) {
	id, ok := requiredQuery(c, "id")
	if ok {
		h.proxy(c, http.MethodDelete, nil, nil, "sniffer", id)
	}
}

func (h *Handler) GetPCAP(c *gin.Context) {
	id, ok := requiredQuery(c, "id")
	if ok {
		h.proxy(c, http.MethodGet, nil, url.Values{"limit": {"104857600"}}, "sniffer", id, "pcap")
	}
}

func (h *Handler) proxy(c *gin.Context, method string, body []byte, query url.Values, segments ...string) {
	target, err := h.resolver.Resolve(c.GetHeader("Token"), controller.V1, segments...)
	if err != nil {
		writeInternalError(c)
		return
	}
	if query != nil {
		target.RawQuery = query.Encode()
	}
	token := c.GetHeader("Token")
	headers := make(http.Header)
	headers.Set("X-Auth-Token", token)
	headers.Set("Accept-Encoding", "gzip")
	headers.Set("Cache-Control", "no-cache")
	if entry, ok := h.sessions.Get(token); ok {
		headers.Set("X-R-Sess", entry.SUSEToken)
	} else {
		headers.Set("X-R-Sess", "")
	}
	if body != nil {
		headers.Set("Content-Type", "text/plain; charset=UTF-8")
	}
	response, err := h.controller.DoTarget(c.Request.Context(), method, target, bytes.NewReader(body), headers)
	if err != nil {
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	copyResponse(c, response)
}

func decodeBody(c *gin.Context, target any) bool {
	if err := json.NewDecoder(c.Request.Body).Decode(target); err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			c.Data(http.StatusRequestEntityTooLarge, "text/plain; charset=UTF-8", []byte("Request entity too large"))
		} else {
			writeBadRequest(c, "invalid JSON request body")
		}
		return false
	}
	return true
}

func requiredQuery(c *gin.Context, name string) (string, bool) {
	value, present := c.GetQuery(name)
	if !present {
		writeBadRequest(c, "query parameter '"+name+"' is required")
	}
	return value, present
}

func copyResponse(c *gin.Context, response *http.Response) {
	for name, values := range response.Header {
		for _, value := range values {
			if strings.EqualFold(name, "Content-Disposition") {
				value = quoteDispositionFilename(value)
			}
			c.Writer.Header().Add(name, value)
		}
	}
	c.Status(response.StatusCode)
	_, _ = io.Copy(c.Writer, response.Body)
}

func quoteDispositionFilename(value string) string {
	disposition, parameters, err := mime.ParseMediaType(value)
	if err != nil || parameters["filename"] == "" {
		return value
	}
	return disposition + "; filename=" + strconv.Quote(parameters["filename"])
}

func writeBadRequest(c *gin.Context, message string) {
	c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte(message))
}

func writeInternalError(c *gin.Context) {
	c.Data(http.StatusInternalServerError, "application/json", []byte("Internal server error"))
}
