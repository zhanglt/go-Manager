package group

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
)

type processProfileEntry struct {
	Name   string  `json:"name"`
	Path   *string `json:"path,omitempty"`
	User   *string `json:"user,omitempty"`
	UID    *int64  `json:"uid,omitempty"`
	Action string  `json:"action"`
}

type processProfileConfig struct {
	Group              string                 `json:"group"`
	AlertDisabled      *bool                  `json:"alert_disabled,omitempty"`
	HashEnabled        *bool                  `json:"hash_enabled,omitempty"`
	ProcessDeleteList  *[]processProfileEntry `json:"process_delete_list,omitempty"`
	ProcessChangeList  *[]processProfileEntry `json:"process_change_list,omitempty"`
	ProcessReplaceList *[]processProfileEntry `json:"process_replace_list,omitempty"`
}

type processProfileConfigData struct {
	ProcessProfileConfig *processProfileConfig `json:"process_profile_config"`
}

func (h *Handler) GetProcessProfile(c *gin.Context) {
	if name, present := c.GetQuery("name"); present {
		h.transfer.Request(c, http.MethodGet, nil, nil, nil, "process_profile", name)
		return
	}
	scope, hasScope := c.GetQuery("scope")
	query := url.Values{"start": {"0"}, "limit": {"1000"}}
	if hasScope {
		query.Set("scope", scope)
	}
	if hasScope && scope == "fed" {
		h.transfer.RequestLocal(c, http.MethodGet, query, nil, nil, "process_profile")
		return
	}
	h.transfer.Request(c, http.MethodGet, query, nil, nil, "process_profile")
}

func (h *Handler) UpdateProcessProfile(c *gin.Context) {
	var input processProfileConfigData
	if !decodeServiceBody(c, &input) {
		return
	}
	if input.ProcessProfileConfig == nil || input.ProcessProfileConfig.Group == "" {
		c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("process_profile_config.group is required"))
		return
	}
	body, _ := json.Marshal(input)
	scope, hasScope := c.GetQuery("scope")
	var query url.Values
	if hasScope {
		query = url.Values{"scope": {scope}}
	}
	if hasScope && scope == "fed" {
		h.transfer.RequestLocal(c, http.MethodPatch, query, nil, body, "process_profile", input.ProcessProfileConfig.Group)
		return
	}
	h.transfer.Request(c, http.MethodPatch, query, nil, body, "process_profile", input.ProcessProfileConfig.Group)
}
