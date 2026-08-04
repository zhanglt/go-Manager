package group

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
)

type fileMonitorFilter struct {
	Filter       string    `json:"filter"`
	Recursive    bool      `json:"recursive"`
	Behavior     string    `json:"behavior"`
	Applications *[]string `json:"applications,omitempty"`
}

type fileMonitorConfig struct {
	AddFilters    *[]fileMonitorFilter `json:"add_filters,omitempty"`
	DeleteFilters *[]fileMonitorFilter `json:"delete_filters,omitempty"`
	UpdateFilters *[]fileMonitorFilter `json:"update_filters,omitempty"`
}

type fileMonitorConfigData struct {
	Config *fileMonitorConfig `json:"config"`
}

type fileMonitorConfigDTO struct {
	Group                 string                 `json:"group"`
	FileMonitorConfigData *fileMonitorConfigData `json:"fileMonitorConfigData"`
}

func (h *Handler) GetFileProfile(c *gin.Context) {
	if name, present := c.GetQuery("name"); present {
		h.transfer.Request(c, http.MethodGet, nil, nil, nil, "file_monitor", name)
		return
	}
	scope, hasScope := c.GetQuery("scope")
	query := url.Values{"start": {"0"}, "limit": {"1000"}}
	if hasScope {
		query.Set("scope", scope)
	}
	if hasScope && scope == "fed" {
		h.transfer.RequestLocal(c, http.MethodGet, query, nil, nil, "file_monitor")
		return
	}
	h.transfer.Request(c, http.MethodGet, query, nil, nil, "file_monitor")
}

func (h *Handler) UpdateFileProfile(c *gin.Context) {
	var input fileMonitorConfigDTO
	if !decodeServiceBody(c, &input) {
		return
	}
	if input.Group == "" || input.FileMonitorConfigData == nil || input.FileMonitorConfigData.Config == nil {
		c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("group and fileMonitorConfigData.config are required"))
		return
	}
	body, _ := json.Marshal(*input.FileMonitorConfigData)
	scope, hasScope := c.GetQuery("scope")
	var query url.Values
	if hasScope {
		query = url.Values{"scope": {scope}}
	}
	if hasScope && scope == "fed" {
		h.transfer.RequestLocal(c, http.MethodPatch, query, nil, body, "file_monitor", input.Group)
		return
	}
	h.transfer.Request(c, http.MethodPatch, query, nil, body, "file_monitor", input.Group)
}

func (h *Handler) GetPredefinedFileProfile(c *gin.Context) {
	name, present := c.GetQuery("name")
	if !present {
		writeRequiredQuery(c, "name")
		return
	}
	h.transfer.Request(c, http.MethodGet, url.Values{"predefined": {""}}, nil, nil, "file_monitor", name)
}
