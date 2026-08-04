package notification

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	managerCache "github.com/neuvector/manager/admin-go/internal/cache"
)

type auditWrap struct {
	Audits []json.RawMessage `json:"audits"`
}

func (h *Handler) GetAudit2(c *gin.Context) {
	start, hasStart, ok := auditInteger(c, "start")
	if !ok {
		return
	}
	limit, hasLimit, ok := auditInteger(c, "limit")
	if !ok {
		return
	}
	fetched := !hasStart || start == 0
	var audits []json.RawMessage
	var original []byte
	if fetched {
		payload, status, err := h.fetchNotificationRaw(c, http.MethodGet, "log", "audit")
		if err != nil || status != http.StatusOK {
			h.writeAuditError(c, status)
			return
		}
		var wrapper auditWrap
		if err := json.Unmarshal(payload, &wrapper); err != nil {
			h.writeAuditError(c, 0)
			return
		}
		original, audits = payload, wrapper.Audits
	}
	if !hasStart || !hasLimit {
		if fetched {
			c.Data(http.StatusOK, "text/plain; charset=UTF-8", original)
		} else {
			c.Data(http.StatusOK, "text/plain; charset=UTF-8", []byte("null"))
		}
		return
	}
	if !h.validateAuditToken(c) {
		return
	}
	key := h.auditCacheKey(c.GetHeader("Token"))
	if fetched {
		h.audits.Set(key, audits, int64(len(original)))
	} else if cached, found := h.audits.Get(key); found {
		audits = cached
	} else {
		audits = make([]json.RawMessage, 0)
	}
	page := sliceAudits(audits, start, limit)
	if len(page) < limit {
		h.audits.Delete(key)
	}
	writeAuditJSON(c, page)
}

func (h *Handler) GetSecurityEvents2(c *gin.Context) {
	resources := []string{"threat", "violation", "incident"}
	output := make([]string, len(resources))
	errorsByIndex := make([]error, len(resources))
	statuses := make([]int, len(resources))
	var wait sync.WaitGroup
	for index, resource := range resources {
		wait.Add(1)
		go func() {
			defer wait.Done()
			payload, status, err := h.fetchNotificationRaw(c, http.MethodGet, "log", resource)
			statuses[index], errorsByIndex[index] = status, err
			output[index] = string(payload)
		}()
	}
	wait.Wait()
	for index := range resources {
		if errorsByIndex[index] != nil || statuses[index] != http.StatusOK {
			writeNotificationError(c)
			return
		}
	}
	writeJSON(c, output)
}

func (h *Handler) validateAuditToken(c *gin.Context) bool {
	response, err := h.transfer.Fetch(c, http.MethodPatch, nil, nil, []byte{}, "auth")
	if err != nil {
		h.writeAuditValidationError(c, 0)
		return false
	}
	status := response.StatusCode
	_, readErr := io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if readErr != nil || status < http.StatusOK || status >= http.StatusMultipleChoices {
		h.writeAuditValidationError(c, status)
		return false
	}
	return true
}

func (h *Handler) fetchNotificationRaw(c *gin.Context, method string, segments ...string) ([]byte, int, error) {
	response, err := h.transfer.Fetch(c, method, nil, nil, nil, segments...)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	var reader io.Reader = response.Body
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Encoding")), "gzip") {
		compressed, gzipErr := gzip.NewReader(response.Body)
		if gzipErr != nil {
			return nil, response.StatusCode, gzipErr
		}
		defer compressed.Close()
		reader = compressed
	}
	payload, err := io.ReadAll(io.LimitReader(reader, h.audits.MaxBytes()+1))
	if err != nil {
		return nil, response.StatusCode, err
	}
	if int64(len(payload)) > h.audits.MaxBytes() {
		return nil, response.StatusCode, errors.New("controller notification response exceeds cache budget")
	}
	return payload, response.StatusCode, nil
}

func (h *Handler) auditCacheKey(token string) managerCache.Key {
	cluster, _ := h.sessions.Cluster(token)
	return managerCache.Key{Token: token, Cluster: cluster, Domain: "audit"}
}

func auditInteger(c *gin.Context, name string) (int, bool, bool) {
	value, present := c.GetQuery(name)
	if !present {
		return 0, false, true
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		c.Data(http.StatusInternalServerError, "text/plain; charset=UTF-8", []byte("Controller unavailable!"))
		return 0, true, false
	}
	return parsed, true, true
}

func sliceAudits(audits []json.RawMessage, start, limit int) []json.RawMessage {
	from := max(start, 0)
	if from > len(audits) {
		from = len(audits)
	}
	to := from
	if limit > 0 {
		to = min(from+limit, len(audits))
	}
	return audits[from:to]
}

func (h *Handler) writeAuditError(c *gin.Context, status int) {
	h.audits.Delete(h.auditCacheKey(c.GetHeader("Token")))
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		c.Data(http.StatusUnauthorized, "text/plain; charset=UTF-8", []byte("Authentication failed!"))
		return
	}
	c.Data(http.StatusInternalServerError, "text/plain; charset=UTF-8", []byte("Controller unavailable!"))
}

func (h *Handler) writeAuditValidationError(c *gin.Context, status int) {
	h.audits.Delete(h.auditCacheKey(c.GetHeader("Token")))
	message := fmt.Sprintf("Status: %d", status)
	if status == http.StatusRequestTimeout {
		message = "Session expired!"
	}
	c.Data(http.StatusInternalServerError, "text/plain; charset=UTF-8", []byte(message))
}

func writeAuditJSON(c *gin.Context, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		c.Data(http.StatusInternalServerError, "text/plain; charset=UTF-8", []byte("Controller unavailable!"))
		return
	}
	c.Data(http.StatusOK, "text/plain; charset=UTF-8", payload)
}
