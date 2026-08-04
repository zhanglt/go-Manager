package group

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/transfer"
)

type serviceConfigParam struct {
	PolicyMode      *string   `json:"policy_mode,omitempty"`
	ProfileMode     *string   `json:"profile_mode,omitempty"`
	BaselineProfile *string   `json:"baseline_profile,omitempty"`
	Services        *[]string `json:"services,omitempty"`
	NotScored       *bool     `json:"not_scored,omitempty"`
}

type serviceConfig struct {
	Config *serviceConfigParam `json:"config"`
}

type systemRequestContent struct {
	PolicyMode      *string `json:"policy_mode,omitempty"`
	ProfileMode     *string `json:"profile_mode,omitempty"`
	BaselineProfile *string `json:"baseline_profile,omitempty"`
}

func (h *Handler) GetService(c *gin.Context) {
	query := url.Values{"view": {"pod"}, "with_cap": {c.DefaultQuery("with_cap", "false")}}
	segments := []string{"service"}
	if name, present := c.GetQuery("name"); present {
		segments = append(segments, name)
	}
	h.transfer.Request(c, http.MethodGet, query, nil, nil, segments...)
}

func (h *Handler) UpdateService(c *gin.Context) {
	var input serviceConfig
	if !decodeServiceBody(c, &input) {
		return
	}
	if input.Config == nil {
		c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("config is required"))
		return
	}
	body, _ := json.Marshal(input)
	h.transfer.Request(c, http.MethodPatch, nil, nil, body, "service", "config")
}

func (h *Handler) CreateService(c *gin.Context) {
	var input serviceConfigParam
	if !decodeServiceBody(c, &input) {
		return
	}
	policyMode := "Discover"
	if input.PolicyMode != nil {
		policyMode = *input.PolicyMode
	}
	body, _ := json.Marshal(struct {
		Config struct {
			NewServicePolicyMode string `json:"new_service_policy_mode"`
		} `json:"config"`
	}{Config: struct {
		NewServicePolicyMode string `json:"new_service_policy_mode"`
	}{NewServicePolicyMode: policyMode}})
	h.transfer.Request(c, http.MethodPatch, nil, nil, body, "system", "config")
}

func (h *Handler) UpdateSystemRequest(c *gin.Context) {
	var input systemRequestContent
	if !decodeServiceBody(c, &input) {
		return
	}
	body, _ := json.Marshal(struct {
		Request systemRequestContent `json:"request"`
	}{Request: input})
	// The public endpoint remains PATCH while the Controller endpoint is POST.
	h.transfer.Request(c, http.MethodPost, nil, nil, body, "system", "request")
}

func decodeServiceBody(c *gin.Context, target any) bool {
	value, err := io.ReadAll(c.Request.Body)
	if err != nil {
		transfer.WriteBodyError(c, err)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	if err := decoder.Decode(target); err != nil {
		transfer.WriteBodyError(c, err)
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("invalid request body"))
		return false
	}
	return true
}
