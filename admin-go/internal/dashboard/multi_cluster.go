package dashboard

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	managerCache "github.com/neuvector/manager/admin-go/internal/cache"
)

type securityScores struct {
	AdmissionRuleScore       int `json:"admission_rule_score"`
	ExposureScore            int `json:"exposure_score"`
	ExposureScoreBy100       int `json:"exposure_score_by_100"`
	NewServiceModeScore      int `json:"new_service_mode_score"`
	PrivilegedContainerScore int `json:"privileged_container_score"`
	RunAsRootScore           int `json:"run_as_root_score"`
	SecurityRiskScore        int `json:"security_risk_score"`
	ServiceModeScore         int `json:"service_mode_score"`
	ServiceModeScoreBy100    int `json:"service_mode_score_by_100"`
	VulnerabilityScore       int `json:"vulnerability_score"`
	VulnerabilityScoreBy100  int `json:"vulnerability_score_by_100"`
}

type multiClusterSummary struct {
	Score       securityScores `json:"score"`
	SummaryJSON string         `json:"summaryJson"`
}

func (h *Handler) GetMultiClusterSummary(c *gin.Context) {
	owner := "master"
	prefix := []string{}
	if clusterID, present := c.GetQuery("clusterId"); present {
		owner = clusterID
		prefix = []string{"fed", "cluster", clusterID, "v1"}
	}
	summaryPath := append(append([]string{}, prefix...), "system", "summary")
	scorePath := append(append([]string{}, prefix...), "system", "score", "metrics")

	var summaryPayload, scorePayload []byte
	var summaryOK, scoreOK bool
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		summaryPayload, summaryOK = h.fetchDashboardRaw(c, summaryPath...)
	}()
	go func() {
		defer wait.Done()
		scorePayload, scoreOK = h.fetchDashboardRaw(c, scorePath...)
	}()
	wait.Wait()

	summary := "error"
	key := managerCache.Key{Domain: "dashboard-summary", Cluster: owner}
	if summaryOK {
		summary = string(summaryPayload)
	} else if cached, found := h.summaries.Get(key); found && len(cached) > 0 {
		summary = cached[0]
	}
	h.summaries.Set(key, []string{summary}, int64(len(summary)))

	var score struct {
		SecurityScores securityScores `json:"security_scores"`
	}
	if !scoreOK {
		score.SecurityScores = securityScores{}
	} else if json.Unmarshal(scorePayload, &score) != nil {
		c.String(http.StatusInternalServerError, "Internal server error")
		return
	}
	payload, err := json.Marshal(multiClusterSummary{Score: score.SecurityScores, SummaryJSON: summary})
	if err != nil {
		c.String(http.StatusInternalServerError, "Internal server error")
		return
	}
	c.Data(http.StatusOK, "application/json", payload)
}

func (h *Handler) fetchDashboardRaw(c *gin.Context, segments ...string) ([]byte, bool) {
	response, err := h.transfer.Fetch(c, http.MethodGet, nil, nil, nil, segments...)
	if err != nil {
		return nil, false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, false
	}
	var reader io.Reader = response.Body
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Encoding")), "gzip") {
		compressed, gzipErr := gzip.NewReader(response.Body)
		if gzipErr != nil {
			return nil, false
		}
		defer compressed.Close()
		reader = compressed
	}
	payload, err := io.ReadAll(io.LimitReader(reader, h.maxBytes+1))
	if err != nil || int64(len(payload)) > h.maxBytes {
		return nil, false
	}
	return payload, true
}
