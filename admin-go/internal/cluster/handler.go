package cluster

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/session"
)

type Handler struct {
	controller *controller.Client
	resolver   *controller.TargetResolver
	sessions   *session.Store
}

type serverInfo struct {
	Server string `json:"server"`
	Port   int    `json:"port"`
}

type masterCluster struct {
	Disabled    *bool      `json:"disabled,omitempty"`
	Name        string     `json:"name"`
	ID          string     `json:"id"`
	Secret      string     `json:"secret"`
	User        *string    `json:"user,omitempty"`
	Status      *string    `json:"status,omitempty"`
	RestVersion *string    `json:"rest_version,omitempty"`
	RestInfo    serverInfo `json:"rest_info"`
}

type jointCluster struct {
	Disabled      *bool      `json:"disabled,omitempty"`
	Name          string     `json:"name"`
	ID            string     `json:"id"`
	Secret        string     `json:"secret"`
	User          *string    `json:"user,omitempty"`
	Status        *string    `json:"status,omitempty"`
	RestVersion   *string    `json:"rest_version,omitempty"`
	RestInfo      serverInfo `json:"rest_info"`
	ProxyRequired *bool      `json:"proxy_required,omitempty"`
}

type membership struct {
	FedRole            string          `json:"fed_role"`
	LocalRestInfo      *serverInfo     `json:"local_rest_info,omitempty"`
	MasterCluster      *masterCluster  `json:"master_cluster,omitempty"`
	JointClusters      *[]jointCluster `json:"joint_clusters,omitempty"`
	UseProxy           *string         `json:"use_proxy,omitempty"`
	DeployRepoScanData *bool           `json:"deploy_repo_scan_data,omitempty"`
}

type clusterServer struct {
	Disabled      *bool   `json:"disabled,omitempty"`
	Name          string  `json:"name"`
	ID            string  `json:"id"`
	Secret        string  `json:"secret"`
	APIServer     string  `json:"api_server"`
	APIPort       *int    `json:"api_port,omitempty"`
	Status        *string `json:"status,omitempty"`
	Username      *string `json:"username,omitempty"`
	RestVersion   *string `json:"rest_version,omitempty"`
	ClusterType   string  `json:"clusterType"`
	ProxyRequired *bool   `json:"proxy_required,omitempty"`
}

type memberData struct {
	FedRole            string           `json:"fed_role"`
	LocalRestInfo      *serverInfo      `json:"local_rest_info,omitempty"`
	Clusters           *[]clusterServer `json:"clusters,omitempty"`
	UseProxy           *string          `json:"use_proxy,omitempty"`
	DeployRepoScanData *bool            `json:"deploy_repo_scan_data,omitempty"`
}

type promptRequest struct {
	Name               string      `json:"name"`
	MasterRestInfo     *serverInfo `json:"master_rest_info,omitempty"`
	UseProxy           *string     `json:"use_proxy,omitempty"`
	DeployRepoScanData *bool       `json:"deploy_repo_scan_data,omitempty"`
}

type configData struct {
	PollInterval       int         `json:"poll_interval"`
	Name               *string     `json:"name,omitempty"`
	UseProxy           *string     `json:"use_proxy,omitempty"`
	RestInfo           *serverInfo `json:"rest_info,omitempty"`
	DeployRepoScanData *bool       `json:"deploy_repo_scan_data,omitempty"`
}

type joinRequest struct {
	Name          string      `json:"name"`
	Server        string      `json:"server"`
	Port          int         `json:"port"`
	JoinToken     string      `json:"join_token"`
	JointRestInfo *serverInfo `json:"joint_rest_info,omitempty"`
	UseProxy      *string     `json:"use_proxy,omitempty"`
}

type leaveRequest struct {
	Force bool `json:"force"`
}

type deployRequest struct {
	IDs *[]string `json:"ids,omitempty"`
}

func NewHandler(client *controller.Client, resolver *controller.TargetResolver, sessions *session.Store) *Handler {
	return &Handler{controller: client, resolver: resolver, sessions: sessions}
}

func (h *Handler) GetMember(c *gin.Context) {
	response, err := h.do(c, http.MethodGet, nil, "fed", "member")
	if err != nil {
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		writeInternalError(c)
		return
	}
	var source membership
	if err := decodeResponse(response, &source); err != nil {
		writeInternalError(c)
		return
	}
	writeJSON(c, http.StatusOK, transformMember(source))
}

func (h *Handler) GetSummary(c *gin.Context) {
	id, ok := requiredQuery(c, "id")
	if ok {
		h.proxy(c, http.MethodGet, nil, "fed", "cluster", id, "v1", "system", "summary")
	}
}

func (h *Handler) Promote(c *gin.Context) {
	proxyBody[promptRequest](h, c, http.MethodPost, "fed", "promote")
}
func (h *Handler) Demote(c *gin.Context) { h.proxy(c, http.MethodPost, nil, "fed", "demote") }
func (h *Handler) GetJoinToken(c *gin.Context) {
	h.proxy(c, http.MethodGet, nil, "fed", "join_token")
}
func (h *Handler) Join(c *gin.Context) { proxyBody[joinRequest](h, c, http.MethodPost, "fed", "join") }
func (h *Handler) Leave(c *gin.Context) {
	proxyBody[leaveRequest](h, c, http.MethodPost, "fed", "leave")
}
func (h *Handler) Config(c *gin.Context) {
	proxyBody[configData](h, c, http.MethodPatch, "fed", "config")
}
func (h *Handler) Deploy(c *gin.Context) {
	proxyBody[deployRequest](h, c, http.MethodPost, "fed", "deploy")
}

func (h *Handler) Delete(c *gin.Context) {
	id, ok := requiredQuery(c, "id")
	if ok {
		h.proxy(c, http.MethodDelete, nil, "fed", "cluster", id)
	}
}

func proxyBody[T any](h *Handler, c *gin.Context, method string, segments ...string) {
	var value T
	if !decodeBody(c, &value) {
		return
	}
	body, err := json.Marshal(value)
	if err != nil {
		writeInternalError(c)
		return
	}
	h.proxy(c, method, body, segments...)
}

func (h *Handler) proxy(c *gin.Context, method string, body []byte, segments ...string) {
	response, err := h.do(c, method, body, segments...)
	if err != nil {
		writeInternalError(c)
		return
	}
	defer response.Body.Close()
	copyResponse(c, response)
}

func (h *Handler) do(c *gin.Context, method string, body []byte, segments ...string) (*http.Response, error) {
	target, err := h.resolver.ResolveLocal(controller.V1, segments...)
	if err != nil {
		return nil, err
	}
	headers := make(http.Header)
	token := c.GetHeader("Token")
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
	return h.controller.DoTarget(c.Request.Context(), method, target, bytes.NewReader(body), headers)
}

func transformMember(source membership) memberData {
	if source.FedRole == "" {
		value := false
		return memberData{FedRole: "", DeployRepoScanData: &value}
	}
	clusters := make([]clusterServer, 0)
	if source.MasterCluster != nil {
		master := source.MasterCluster
		port := master.RestInfo.Port
		proxyRequired := false
		clusters = append(clusters, clusterServer{
			Disabled: master.Disabled, Name: master.Name, ID: master.ID, Secret: master.Secret,
			APIServer: master.RestInfo.Server, APIPort: &port, Status: master.Status,
			Username: master.User, RestVersion: master.RestVersion, ClusterType: "master",
			ProxyRequired: &proxyRequired,
		})
	}
	if source.JointClusters != nil {
		for _, joint := range *source.JointClusters {
			port := joint.RestInfo.Port
			clusters = append(clusters, clusterServer{
				Disabled: joint.Disabled, Name: joint.Name, ID: joint.ID, Secret: joint.Secret,
				APIServer: joint.RestInfo.Server, APIPort: &port, Status: joint.Status,
				Username: joint.User, RestVersion: joint.RestVersion, ClusterType: "joint",
				ProxyRequired: joint.ProxyRequired,
			})
		}
	}
	useProxy := ""
	if source.UseProxy != nil {
		useProxy = *source.UseProxy
	}
	return memberData{
		FedRole: source.FedRole, LocalRestInfo: source.LocalRestInfo, Clusters: &clusters,
		UseProxy: &useProxy, DeployRepoScanData: source.DeployRepoScanData,
	}
}

func decodeBody(c *gin.Context, target any) bool {
	if err := json.NewDecoder(c.Request.Body).Decode(target); err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			c.Data(http.StatusRequestEntityTooLarge, "text/plain; charset=UTF-8", []byte("Request entity too large"))
		} else {
			c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("invalid JSON request body"))
		}
		return false
	}
	return true
}

func decodeResponse(response *http.Response, target any) error {
	var reader io.Reader = response.Body
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Encoding")), "gzip") {
		gzipReader, err := gzip.NewReader(response.Body)
		if err != nil {
			return err
		}
		defer gzipReader.Close()
		reader = gzipReader
	}
	return json.NewDecoder(io.LimitReader(reader, 50*1024*1024+1)).Decode(target)
}

func requiredQuery(c *gin.Context, name string) (string, bool) {
	value, present := c.GetQuery(name)
	if !present {
		c.Data(http.StatusBadRequest, "text/plain; charset=UTF-8", []byte("query parameter '"+name+"' is required"))
	}
	return value, present
}

func copyResponse(c *gin.Context, response *http.Response) {
	for name, values := range response.Header {
		for _, value := range values {
			c.Writer.Header().Add(name, value)
		}
	}
	c.Status(response.StatusCode)
	_, _ = io.Copy(c.Writer, response.Body)
}

func writeJSON(c *gin.Context, status int, value any) {
	c.Header("Content-Type", "application/json")
	c.JSON(status, value)
}

func writeInternalError(c *gin.Context) {
	c.Data(http.StatusInternalServerError, "application/json", []byte("Internal server error"))
}
