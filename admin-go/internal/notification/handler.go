package notification

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	managerCache "github.com/neuvector/manager/admin-go/internal/cache"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/session"
	"github.com/neuvector/manager/admin-go/internal/transfer"
)

type Handler struct {
	transfer   *transfer.Proxy
	ipGeo      *ipGeoDatabase
	sessions   *session.Store
	audits     *managerCache.Store[json.RawMessage]
	layouts    *managerCache.Store[graphLayout]
	blacklists *managerCache.Store[graphBlacklist]
}

func NewHandler(client *controller.Client, resolver *controller.TargetResolver, sessions *session.Store, auditCaches ...*managerCache.Store[json.RawMessage]) *Handler {
	audits := NewCache(100, 16<<20, 5*time.Minute)
	if len(auditCaches) > 0 && auditCaches[0] != nil {
		audits = auditCaches[0]
	}
	return NewHandlerWithCaches(client, resolver, sessions, audits, NewGraphLayoutCache(100, 16<<20, 5*time.Minute), NewGraphBlacklistCache(100, 16<<20, 5*time.Minute))
}

// NewHandlerWithCaches lets the server share its configured cache limits with graph state.
func NewHandlerWithCaches(client *controller.Client, resolver *controller.TargetResolver, sessions *session.Store, audits *managerCache.Store[json.RawMessage], layouts *managerCache.Store[graphLayout], blacklists *managerCache.Store[graphBlacklist]) *Handler {
	return &Handler{transfer: transfer.New(client, resolver, sessions), ipGeo: newIPGeoDatabase(), sessions: sessions, audits: audits, layouts: layouts, blacklists: blacklists}
}

func NewCache(maxEntries int, maxBytes int64, ttl time.Duration) *managerCache.Store[json.RawMessage] {
	return managerCache.New[json.RawMessage](maxEntries, maxBytes, ttl)
}

func (h *Handler) PatchIPGeo(c *gin.Context) {
	var addresses []string
	if err := json.NewDecoder(c.Request.Body).Decode(&addresses); err != nil {
		transfer.WriteBodyError(c, err)
		return
	}
	result, err := h.ipGeo.lookup(addresses)
	if err != nil {
		c.String(http.StatusInternalServerError, "Internal server error")
		return
	}
	payload, err := json.Marshal(gin.H{"ip_map": result})
	if err != nil {
		c.String(http.StatusInternalServerError, "Internal server error")
		return
	}
	c.Data(http.StatusOK, "application/json", payload)
}

func (h *Handler) GetEvent(c *gin.Context)     { h.get(c, "event") }
func (h *Handler) GetIncident(c *gin.Context)  { h.get(c, "incident") }
func (h *Handler) GetAudit(c *gin.Context)     { h.get(c, "audit") }
func (h *Handler) GetViolation(c *gin.Context) { h.get(c, "violation") }

func (h *Handler) GetViolationTop(c *gin.Context) {
	category, present := c.GetQuery("category")
	if !present {
		c.String(http.StatusBadRequest, "query parameter 'category' is required")
		return
	}
	query := url.Values{"start": {"0"}, "limit": {"5"}}
	if category == "client" {
		query.Set("s_client", "desc")
	} else {
		query.Set("s_server", "desc")
	}
	h.transfer.Request(c, http.MethodGet, query, nil, nil, "log", "violation", "workload")
}

func (h *Handler) GetNetworkSession(c *gin.Context) {
	id, present := c.GetQuery("id")
	if !present {
		c.String(http.StatusBadRequest, "query parameter 'id' is required")
		return
	}
	h.transfer.Request(c, http.MethodGet, url.Values{"f_workload": {id}, "limit": {"256"}}, nil, nil, "session")
}

func (h *Handler) DeleteConversation(c *gin.Context)     { h.conversation(c, http.MethodDelete) }
func (h *Handler) GetConversationHistory(c *gin.Context) { h.conversation(c, http.MethodGet) }

func (h *Handler) DeleteConversationEndpoint(c *gin.Context) {
	id, present := c.GetQuery("id")
	if !present {
		c.String(http.StatusBadRequest, "query parameter 'id' is required")
		return
	}
	h.transfer.Request(c, http.MethodDelete, nil, nil, nil, "conversation_endpoint", id)
}

func (h *Handler) UpdateConversationEndpoint(c *gin.Context) {
	value, ok := decodeCleanObject(c)
	if !ok {
		return
	}
	config, ok := value["config"].(map[string]any)
	if !ok {
		c.String(http.StatusBadRequest, "config is required")
		return
	}
	id, ok := config["id"].(string)
	if !ok || id == "" {
		c.String(http.StatusBadRequest, "config.id is required")
		return
	}
	body, _ := json.Marshal(value)
	h.transfer.Request(c, http.MethodPatch, nil, nil, body, "conversation_endpoint", id)
}

func (h *Handler) AcceptNotification(c *gin.Context) {
	value, ok := decodeCleanObject(c)
	if !ok {
		return
	}
	body, _ := json.Marshal(value)
	h.transfer.RequestLocal(c, http.MethodPost, nil, nil, body, "internal", "alert")
}

func (h *Handler) conversation(c *gin.Context, method string) {
	from, hasFrom := c.GetQuery("from")
	to, hasTo := c.GetQuery("to")
	if !hasFrom || !hasTo {
		c.String(http.StatusBadRequest, "query parameters 'from' and 'to' are required")
		return
	}
	h.transfer.Request(c, method, nil, nil, nil, "conversation", from, to)
}

func (h *Handler) get(c *gin.Context, resource string) {
	h.transfer.Request(c, http.MethodGet, nil, nil, nil, "log", resource)
}

func decodeCleanObject(c *gin.Context) (map[string]any, bool) {
	var value any
	if err := json.NewDecoder(c.Request.Body).Decode(&value); err != nil {
		transfer.WriteBodyError(c, err)
		return nil, false
	}
	cleaned, ok := removeNulls(value).(map[string]any)
	if !ok {
		c.String(http.StatusBadRequest, "object body is required")
		return nil, false
	}
	return cleaned, true
}

func removeNulls(value any) any {
	switch current := value.(type) {
	case map[string]any:
		for key, item := range current {
			if item == nil {
				delete(current, key)
			} else {
				current[key] = removeNulls(item)
			}
		}
	case []any:
		for index, item := range current {
			current[index] = removeNulls(item)
		}
	}
	return value
}
