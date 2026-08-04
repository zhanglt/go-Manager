package group

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
)

type dlpPattern struct {
	Key     string  `json:"key"`
	Op      string  `json:"op"`
	Value   string  `json:"value"`
	Context *string `json:"context,omitempty"`
}

type dlpRule struct {
	Name     string       `json:"name"`
	ID       *int64       `json:"id,omitempty"`
	Patterns []dlpPattern `json:"patterns"`
}

type preRuleContext struct {
	Name    string `json:"name"`
	Context string `json:"context"`
}

type dlpSensorConfig struct {
	Name      string            `json:"name"`
	Comment   *string           `json:"comment,omitempty"`
	CfgType   *string           `json:"cfg_type,omitempty"`
	Change    *[]dlpRule        `json:"change,omitempty"`
	Delete    *[]dlpRule        `json:"delete,omitempty"`
	Rules     *[]dlpRule        `json:"rules,omitempty"`
	Predefine *bool             `json:"predefine,omitempty"`
	PreRules  *[]preRuleContext `json:"prerules,omitempty"`
}

type dlpSensorConfigData struct {
	Config *dlpSensorConfig `json:"config"`
}

type exportedDlpSensorList struct {
	Names               []string                  `json:"names"`
	RemoteExportOptions *remoteExportOptionsInput `json:"remote_export_options"`
}

type exportedDlpSensor struct {
	Names               []string             `json:"names"`
	RemoteExportOptions *remoteExportOptions `json:"remote_export_options,omitempty"`
}

type dlpSetting struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}

type dlpGroupConfig struct {
	Name    string        `json:"name"`
	Status  *bool         `json:"status,omitempty"`
	Delete  *[]string     `json:"delete,omitempty"`
	Sensors *[]dlpSetting `json:"sensors,omitempty"`
	Replace *[]dlpSetting `json:"replace,omitempty"`
}

type dlpGroupConfigData struct {
	Config *dlpGroupConfig `json:"config"`
}

type wafSensorConfig struct {
	Name    string     `json:"name"`
	Comment *string    `json:"comment,omitempty"`
	CfgType *string    `json:"cfg_type,omitempty"`
	Change  *[]dlpRule `json:"change,omitempty"`
	Delete  *[]dlpRule `json:"delete,omitempty"`
	Rules   *[]dlpRule `json:"rules,omitempty"`
}

type wafSensorConfigData struct {
	Config *wafSensorConfig `json:"config"`
}

type wafGroupConfig struct {
	Name    string        `json:"name"`
	Status  *bool         `json:"status,omitempty"`
	Delete  *[]string     `json:"delete,omitempty"`
	Sensors *[]dlpSetting `json:"sensors,omitempty"`
	Replace *[]dlpSetting `json:"replace,omitempty"`
}

type wafGroupConfigData struct {
	Config *wafGroupConfig `json:"config"`
}

func (h *Handler) GetDlpSensor(c *gin.Context) {
	segments := []string{"dlp", "sensor"}
	if name, present := c.GetQuery("name"); present {
		segments = append(segments, name)
		h.transfer.Request(c, http.MethodGet, nil, nil, nil, segments...)
		return
	}
	query := optionalQueryValue(c, "scope")
	h.transfer.Request(c, http.MethodGet, query, nil, nil, segments...)
}

func (h *Handler) CreateDlpSensor(c *gin.Context) {
	_, body, ok := decodeDlpSensor(c)
	if ok {
		h.transfer.Request(c, http.MethodPost, nil, nil, body, "dlp", "sensor")
	}
}

func (h *Handler) UpdateDlpSensor(c *gin.Context) {
	input, body, ok := decodeDlpSensor(c)
	if ok {
		h.transfer.Request(c, http.MethodPatch, nil, nil, body, "dlp", "sensor", input.Config.Name)
	}
}

func (h *Handler) DeleteDlpSensor(c *gin.Context) {
	name, present := c.GetQuery("name")
	if !present {
		writeRequiredQuery(c, "name")
		return
	}
	h.transfer.Request(c, http.MethodDelete, nil, nil, nil, "dlp", "sensor", name)
}

func (h *Handler) ExportDlpSensor(scope string) gin.HandlerFunc {
	return h.exportSensor(scope, "dlp")
}

func (h *Handler) exportSensor(scope, resource string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input exportedDlpSensorList
		if !decodeServiceBody(c, &input) {
			return
		}
		if input.Names == nil {
			c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("names is required"))
			return
		}
		var options *remoteExportOptions
		if input.RemoteExportOptions != nil {
			if input.RemoteExportOptions.RemoteRepositoryNickname == nil {
				c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("remote_repository_nickname is required"))
				return
			}
			options = &remoteExportOptions{
				RemoteRepositoryNickname: *input.RemoteExportOptions.RemoteRepositoryNickname,
				FilePath:                 input.RemoteExportOptions.FilePath, Comment: input.RemoteExportOptions.Comment,
			}
		}
		body, _ := json.Marshal(exportedDlpSensor{Names: input.Names, RemoteExportOptions: options})
		h.transfer.Post(c, scope, nil, body, "file", resource)
	}
}

func (h *Handler) ImportDlpSensor(scope string) gin.HandlerFunc {
	return h.transfer.Import(scope, "file", "dlp", "config")
}

func (h *Handler) GetDlpGroup(c *gin.Context) {
	name, present := c.GetQuery("name")
	if !present {
		writeRequiredQuery(c, "name")
		return
	}
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, "dlp", "group", name)
}

func (h *Handler) UpdateDlpGroup(c *gin.Context) {
	var input dlpGroupConfigData
	if !decodeServiceBody(c, &input) {
		return
	}
	if input.Config == nil || input.Config.Name == "" {
		c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("config.name is required"))
		return
	}
	body, _ := json.Marshal(input)
	h.transfer.Request(c, http.MethodPatch, nil, nil, body, "dlp", "group", input.Config.Name)
}

func (h *Handler) GetWafSensor(c *gin.Context) {
	segments := []string{"waf", "sensor"}
	if name, present := c.GetQuery("name"); present {
		segments = append(segments, name)
		h.transfer.Request(c, http.MethodGet, nil, nil, nil, segments...)
		return
	}
	h.transfer.Request(c, http.MethodGet, optionalQueryValue(c, "scope"), nil, nil, segments...)
}

func (h *Handler) CreateWafSensor(c *gin.Context) {
	_, body, ok := decodeWafSensor(c)
	if ok {
		h.transfer.Request(c, http.MethodPost, nil, nil, body, "waf", "sensor")
	}
}

func (h *Handler) UpdateWafSensor(c *gin.Context) {
	input, body, ok := decodeWafSensor(c)
	if ok {
		h.transfer.Request(c, http.MethodPatch, nil, nil, body, "waf", "sensor", input.Config.Name)
	}
}

func (h *Handler) DeleteWafSensor(c *gin.Context) {
	name, present := c.GetQuery("name")
	if !present {
		writeRequiredQuery(c, "name")
		return
	}
	h.transfer.Request(c, http.MethodDelete, nil, nil, nil, "waf", "sensor", name)
}

func (h *Handler) ExportWafSensor(scope string) gin.HandlerFunc {
	return h.exportSensor(scope, "waf")
}

func (h *Handler) ImportWafSensor(scope string) gin.HandlerFunc {
	return h.transfer.Import(scope, "file", "waf", "config")
}

func (h *Handler) GetWafGroup(c *gin.Context) {
	name, present := c.GetQuery("name")
	if !present {
		writeRequiredQuery(c, "name")
		return
	}
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, "waf", "group", name)
}

func (h *Handler) UpdateWafGroup(c *gin.Context) {
	var input wafGroupConfigData
	if !decodeServiceBody(c, &input) {
		return
	}
	if input.Config == nil || input.Config.Name == "" {
		c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("config.name is required"))
		return
	}
	body, _ := json.Marshal(input)
	h.transfer.Request(c, http.MethodPatch, nil, nil, body, "waf", "group", input.Config.Name)
}

func decodeDlpSensor(c *gin.Context) (dlpSensorConfigData, []byte, bool) {
	var input dlpSensorConfigData
	if !decodeServiceBody(c, &input) {
		return input, nil, false
	}
	if input.Config == nil || input.Config.Name == "" {
		c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("config.name is required"))
		return input, nil, false
	}
	body, _ := json.Marshal(input)
	return input, body, true
}

func decodeWafSensor(c *gin.Context) (wafSensorConfigData, []byte, bool) {
	var input wafSensorConfigData
	if !decodeServiceBody(c, &input) {
		return input, nil, false
	}
	if input.Config == nil || input.Config.Name == "" {
		c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("config.name is required"))
		return input, nil, false
	}
	body, _ := json.Marshal(input)
	return input, body, true
}

func optionalQueryValue(c *gin.Context, name string) url.Values {
	value, present := c.GetQuery(name)
	if !present {
		return nil
	}
	return url.Values{name: {value}}
}
