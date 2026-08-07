package server

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	webassets "github.com/neuvector/manager/admin-go/assets/web"
	"github.com/neuvector/manager/admin-go/internal/access"
	"github.com/neuvector/manager/admin-go/internal/account"
	"github.com/neuvector/manager/admin-go/internal/auth"
	"github.com/neuvector/manager/admin-go/internal/buildinfo"
	managerCache "github.com/neuvector/manager/admin-go/internal/cache"
	"github.com/neuvector/manager/admin-go/internal/cluster"
	"github.com/neuvector/manager/admin-go/internal/config"
	"github.com/neuvector/manager/admin-go/internal/controller"
	"github.com/neuvector/manager/admin-go/internal/dashboard"
	"github.com/neuvector/manager/admin-go/internal/device"
	managerGroup "github.com/neuvector/manager/admin-go/internal/group"
	managerMiddleware "github.com/neuvector/manager/admin-go/internal/middleware"
	"github.com/neuvector/manager/admin-go/internal/notification"
	"github.com/neuvector/manager/admin-go/internal/observability"
	"github.com/neuvector/manager/admin-go/internal/policy"
	"github.com/neuvector/manager/admin-go/internal/risk"
	"github.com/neuvector/manager/admin-go/internal/session"
	"github.com/neuvector/manager/admin-go/internal/sigstore"
	"github.com/neuvector/manager/admin-go/internal/workload"
)

type rebrandResponse struct {
	CustomLoginLogo         string `json:"customLoginLogo"`
	CustomPolicy            string `json:"customPolicy"`
	CustomPageHeaderContent string `json:"customPageHeaderContent"`
	CustomPageHeaderColor   string `json:"customPageHeaderColor"`
	CustomPageFooterContent string `json:"customPageFooterContent"`
	CustomPageFooterColor   string `json:"customPageFooterColor"`
}

func NewHandler(cfg config.Config, logger *slog.Logger, controllerClient *controller.Client) http.Handler {
	return newHandler(cfg, logger, controllerClient, webassets.Root())
}

func newHandler(cfg config.Config, logger *slog.Logger, controllerClient *controller.Client, webFiles fs.FS) http.Handler {
	metrics := observability.NewRegistry()
	controllerClient.SetObserver(metrics)
	sessions := session.NewStore(cfg.Session.MaxEntries)
	resolver := controller.NewTargetResolver(cfg.Controller.BaseURL, sessions)
	handler, _ := buildHandler(cfg, logger, controllerClient, webFiles, sessions, resolver, device.NewHandler(controllerClient, resolver, sessions), metrics, false)
	return handler
}

type managedHandler struct {
	http.Handler
	device  *device.Handler
	metrics *observability.Registry
}

func (h *managedHandler) Close() error { return h.device.Close() }

func newManagedHandler(cfg config.Config, logger *slog.Logger, controllerClient *controller.Client) (*managedHandler, error) {
	metrics := observability.NewRegistry()
	controllerClient.SetObserver(metrics)
	sessions := session.NewStore(cfg.Session.MaxEntries)
	resolver := controller.NewTargetResolver(cfg.Controller.BaseURL, sessions)
	deviceHandler, err := device.NewSupportHandler(controllerClient, resolver, sessions, device.SupportOptions{
		Command: cfg.Support.Command, TempDir: cfg.Support.TempDir, Timeout: cfg.Support.Timeout,
		MaxFileBytes: cfg.Support.MaxFileBytes, MaxConcurrent: cfg.Support.MaxConcurrent, Metrics: metrics,
	})
	if err != nil {
		return nil, err
	}
	handler, err := buildHandler(cfg, logger, controllerClient, webassets.Root(), sessions, resolver, deviceHandler, metrics, true)
	if err != nil {
		_ = deviceHandler.Close()
		return nil, err
	}
	return &managedHandler{Handler: handler, device: deviceHandler, metrics: metrics}, nil
}

func buildHandler(cfg config.Config, logger *slog.Logger, controllerClient *controller.Client, webFiles fs.FS, sessions *session.Store, resolver *controller.TargetResolver, deviceHandler *device.Handler, metrics *observability.Registry, loadLocalData bool) (http.Handler, error) {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.SetTrustedProxies(nil) //nolint:errcheck
	engine.Use(
		managerMiddleware.RequestID(),
		managerMiddleware.Recovery(logger),
		managerMiddleware.AccessLog(logger),
		managerMiddleware.Metrics(metrics),
		managerMiddleware.LimitBody(cfg.Server.MaxBodyBytes),
		managerMiddleware.SecurityHeaders(cfg.Server.TLS),
	)

	group := engine.Group(cfg.Server.PathPrefix)
	scannedCache := managerCache.New[workload.WorkloadV2](cfg.Cache.MaxEntries, cfg.Cache.MaxBytes, cfg.Cache.TTL)
	groupCache := managerGroup.NewCache(cfg.Cache.MaxEntries, cfg.Cache.MaxBytes, cfg.Cache.TTL)
	policyCache := policy.NewCache(cfg.Cache.MaxEntries, cfg.Cache.MaxBytes, cfg.Cache.TTL)
	auditCache := notification.NewCache(cfg.Cache.MaxEntries, cfg.Cache.MaxBytes, cfg.Cache.TTL)
	graphLayoutCache := notification.NewGraphLayoutCache(cfg.Cache.MaxEntries, cfg.Cache.MaxBytes, cfg.Cache.TTL)
	graphBlacklistCache := notification.NewGraphBlacklistCache(cfg.Cache.MaxEntries, cfg.Cache.MaxBytes, cfg.Cache.TTL)
	dashboardCache := dashboard.NewCache(cfg.Cache.MaxEntries, cfg.Cache.MaxBytes, cfg.Cache.TTL)
	registerCacheMetrics(metrics, "workload_scanned", scannedCache)
	registerCacheMetrics(metrics, "group", groupCache)
	registerCacheMetrics(metrics, "policy", policyCache)
	registerCacheMetrics(metrics, "audit", auditCache)
	registerCacheMetrics(metrics, "graph_layout", graphLayoutCache)
	registerCacheMetrics(metrics, "graph_blacklist", graphBlacklistCache)
	registerCacheMetrics(metrics, "dashboard", dashboardCache)
	metrics.RegisterSession(func() (int, int) { return sessions.Len(), sessions.Capacity() })
	notificationHandler := notification.NewHandlerWithCaches(controllerClient, resolver, sessions, auditCache, graphLayoutCache, graphBlacklistCache)
	riskHandler := risk.NewHandler(controllerClient, resolver, sessions)
	if loadLocalData {
		if err := errors.Join(notificationHandler.LoadLocalData(), riskHandler.LoadLocalData()); err != nil {
			return nil, fmt.Errorf("load local data: %w", err)
		}
	}
	invalidators := managerCache.Invalidators{scannedCache, groupCache, policyCache, auditCache, graphLayoutCache, graphBlacklistCache}
	registerCompatibilityRoutes(
		group,
		cfg,
		auth.NewHandlerWithOptions(controllerClient, sessions, auth.SSOOptions{
			PublicURL: cfg.SSO.PublicURL, PathPrefix: cfg.Server.PathPrefix,
			TTL: cfg.SSO.TTL, MaxEntries: cfg.SSO.MaxEntries, SecureCookies: cfg.Server.TLS,
		}, invalidators),
		access.NewHandler(controllerClient, resolver, sessions, invalidators),
		account.NewHandler(controllerClient, resolver, sessions),
		cluster.NewHandler(controllerClient, resolver, sessions),
		dashboard.NewHandlerWithCache(controllerClient, resolver, sessions, cfg.Cache.MaxBytes, dashboardCache),
		deviceHandler,
		managerGroup.NewHandler(controllerClient, resolver, sessions, groupCache),
		notificationHandler,
		policy.NewHandler(controllerClient, resolver, sessions, policyCache),
		riskHandler,
		sigstore.NewHandler(controllerClient, resolver, sessions),
		workload.NewHandler(controllerClient, resolver, sessions, scannedCache),
	)
	engine.NoRoute(newStaticHandler(webFiles, cfg.Server.PathPrefix, cfg.Server.Development, buildinfo.Version).handle)
	return engine, nil
}

func registerCacheMetrics[V any](registry *observability.Registry, name string, store *managerCache.Store[V]) {
	registry.RegisterCache(name, func() observability.CacheSnapshot {
		stats := store.Stats()
		return observability.CacheSnapshot{
			Entries: stats.Entries, Bytes: stats.Bytes, CapacityEntries: stats.CapacityEntries,
			CapacityBytes: stats.CapacityBytes, CapacityEvictions: stats.CapacityEvictions,
		}
	})
}

func registerCompatibilityRoutes(
	group *gin.RouterGroup,
	cfg config.Config,
	authHandler *auth.Handler,
	accessHandler *access.Handler,
	accountHandler *account.Handler,
	clusterHandler *cluster.Handler,
	dashboardHandler *dashboard.Handler,
	deviceHandler *device.Handler,
	groupHandler *managerGroup.Handler,
	notificationHandler *notification.Handler,
	policyHandler *policy.Handler,
	riskHandler *risk.Handler,
	sigstoreHandler *sigstore.Handler,
	workloadHandler *workload.Handler,
) {
	group.GET("/multi-cluster-summary", requireToken(), dashboardHandler.GetMultiClusterSummary)
	group.GET("/dashboard/alerts", requireToken(), dashboardHandler.GetAlerts)
	group.GET("/dashboard/scores", requireToken(), dashboardHandler.GetScores)
	group.POST("/dashboard/scores", requireToken(), dashboardHandler.PostScores)
	group.GET("/dashboard/notifications", requireToken(), dashboardHandler.GetNotifications)
	group.Any("/dashboard/details", requireToken(), dashboardHandler.GetDetails)
	group.GET("/gravatar", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain; charset=UTF-8")
		c.String(http.StatusOK, "%s", cfg.UI.GravatarEnabled)
	})
	group.GET("/rebrand", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.JSON(http.StatusOK, rebrandResponse{
			CustomLoginLogo: cfg.UI.CustomLoginLogo, CustomPolicy: cfg.UI.CustomPolicy,
			CustomPageHeaderContent: cfg.UI.CustomPageHeaderContent,
			CustomPageHeaderColor:   cfg.UI.CustomPageHeaderColor,
			CustomPageFooterContent: cfg.UI.CustomPageFooterContent,
			CustomPageFooterColor:   cfg.UI.CustomPageFooterColor,
		})
	})
	group.GET("/version", requireToken(), func(c *gin.Context) {
		c.Header("Content-Type", "text/plain; charset=UTF-8")
		c.String(http.StatusOK, "%s", buildinfo.Version)
	})
	group.PATCH("/ip-geo", requireToken(), notificationHandler.PatchIPGeo)
	group.GET("/event", requireToken(), notificationHandler.GetEvent)
	group.GET("/incident", requireToken(), notificationHandler.GetIncident)
	group.GET("/audit", requireToken(), notificationHandler.GetAudit)
	group.GET("/audit2", requireToken(), notificationHandler.GetAudit2)
	group.GET("/violation", requireToken(), notificationHandler.GetViolation)
	group.GET("/violation/top", requireToken(), notificationHandler.GetViolationTop)
	group.POST("/violation/track", requireToken(), notificationHandler.TrackViolation)
	group.GET("/threat", requireToken(), notificationHandler.GetThreat)
	group.GET("/threat/top", requireToken(), notificationHandler.GetThreatTop)
	group.POST("/threat/track", requireToken(), notificationHandler.TrackThreat)
	group.GET("/security-events", requireToken(), notificationHandler.GetSecurityEvents)
	group.GET("/security-events2", requireToken(), notificationHandler.GetSecurityEvents2)
	group.GET("/network/session", requireToken(), notificationHandler.GetNetworkSession)
	group.DELETE("/network/conversation", requireToken(), notificationHandler.DeleteConversation)
	group.GET("/network/history", requireToken(), notificationHandler.GetConversationHistory)
	group.DELETE("/network/endpoint", requireToken(), notificationHandler.DeleteConversationEndpoint)
	group.PATCH("/network/endpoint", requireToken(), notificationHandler.UpdateConversationEndpoint)
	group.POST("/notification/accept", requireToken(), notificationHandler.AcceptNotification)
	group.GET("/network/graph", requireToken(), notificationHandler.GetNetworkGraph)
	group.POST("/network/graph", requireToken(), notificationHandler.CreateNetworkGraph)
	group.GET("/network/graph/layout", requireToken(), notificationHandler.GetNetworkGraphLayout)
	group.GET("/network/graph/blacklist", requireToken(), notificationHandler.GetNetworkGraphBlacklist)
	group.POST("/network/graph/blacklist", requireToken(), notificationHandler.CreateNetworkGraphBlacklist)
	group.GET("/eula", func(c *gin.Context) { authHandler.EULA(c, cfg.UI.EULAOEMAppSafe) })
	group.POST("/auth", authHandler.Login)
	group.GET("/samlslo", authHandler.SAMLLogoutResponse)
	group.POST("/samlslo", authHandler.SAMLLogoutResponse)
	group.GET("/token_auth_server", authHandler.GetSAMLAuthServer)
	group.PATCH("/token_auth_server", authHandler.PatchSAMLAuthServer)
	group.POST("/token_auth_server", authHandler.PostSAMLAuthServer)
	group.GET("/openId_auth", authHandler.CompleteOpenIDAuth)
	group.PATCH("/openId_auth", authHandler.PatchOpenIDAuth)
	group.GET("/token_auth_server_slo", requireToken(), authHandler.GetSAMLAuthServerLogout)
	group.DELETE("/auth", requireToken(), authHandler.Logout)
	group.PATCH("/heartbeat", requireToken(), authHandler.Heartbeat)
	group.GET("/self", requireToken(), authHandler.Self)
	group.GET("/fed/switch", requireToken(), accessHandler.SwitchCluster)
	group.GET("/fed/member", requireToken(), clusterHandler.GetMember)
	group.GET("/fed/summary", requireToken(), clusterHandler.GetSummary)
	group.POST("/fed/promote", requireToken(), clusterHandler.Promote)
	group.POST("/fed/demote", requireToken(), clusterHandler.Demote)
	group.GET("/fed/join_token", requireToken(), clusterHandler.GetJoinToken)
	group.POST("/fed/join", requireToken(), clusterHandler.Join)
	group.POST("/fed/leave", requireToken(), clusterHandler.Leave)
	group.PATCH("/fed/config", requireToken(), clusterHandler.Config)
	group.DELETE("/fed", requireToken(), clusterHandler.Delete)
	group.POST("/fed/deploy", requireToken(), clusterHandler.Deploy)
	group.GET("/enforcer", requireToken(), deviceHandler.GetEnforcers)
	group.GET("/single-enforcer", requireToken(), deviceHandler.GetEnforcer)
	group.GET("/controller", requireToken(), deviceHandler.GetControllers)
	group.GET("/scanner", requireToken(), deviceHandler.GetScanners)
	group.GET("/summary", requireToken(), deviceHandler.GetSummary)
	group.GET("/ibmsa_setup", requireToken(), deviceHandler.GetIBMSetup)
	group.GET("/usage", requireToken(), deviceHandler.GetUsage)
	group.GET("/host", requireToken(), deviceHandler.GetHosts)
	group.GET("/host/workload", requireToken(), deviceHandler.GetHostWorkloads)
	group.GET("/host/compliance", requireToken(), deviceHandler.GetHostCompliance)
	group.POST("/host/scan-report", requireToken(), deviceHandler.GetHostScanReport)
	group.GET("/bench/docker", requireToken(), deviceHandler.GetDockerBench)
	group.POST("/bench/docker", requireToken(), deviceHandler.CreateDockerBench)
	group.GET("/bench/kubernetes", requireToken(), deviceHandler.GetKubernetesBench)
	group.POST("/bench/kubernetes", requireToken(), deviceHandler.CreateKubernetesBench)
	group.POST("/csp-support", requireToken(), deviceHandler.DownloadCSPFile)
	group.GET("/file/config", requireToken(), deviceHandler.GetFileConfig)
	group.POST("/file/config", requireToken(), deviceHandler.CreateFileConfig)
	group.POST("/file/config-fed", requireToken(), deviceHandler.ImportFedSystemConfig)
	group.POST("/file/export-config-fed", requireToken(), deviceHandler.ExportFedSystemConfig)
	group.GET("/file/debug", requireToken(), deviceHandler.GetDebugLog)
	group.POST("/file/debug", requireToken(), deviceHandler.CreateDebugLog)
	group.GET("/file/debug/check", requireToken(), deviceHandler.CheckDebugLog)
	group.POST("/group/export", requireToken(), groupHandler.Export("local"))
	group.POST("/group/export-fed", requireToken(), groupHandler.Export("fed"))
	group.POST("/group/import", requireToken(), groupHandler.Import("local"))
	group.POST("/group/import-fed", requireToken(), groupHandler.Import("fed"))
	group.GET("/group-list", requireToken(), groupHandler.GetGroupList)
	group.GET("/group/custom_check", requireToken(), groupHandler.GetCustomCheck)
	group.PATCH("/group/custom_check", requireToken(), groupHandler.UpdateCustomCheck)
	group.POST("/group", requireToken(), groupHandler.CreateGroup)
	group.GET("/group", requireToken(), groupHandler.GetGroups)
	group.PATCH("/group", requireToken(), groupHandler.UpdateGroup)
	group.DELETE("/group", requireToken(), groupHandler.DeleteGroup)
	group.GET("/service", requireToken(), groupHandler.GetService)
	group.PATCH("/service", requireToken(), groupHandler.UpdateService)
	group.POST("/service", requireToken(), groupHandler.CreateService)
	group.PATCH("/service/all", requireToken(), groupHandler.UpdateSystemRequest)
	group.GET("/processProfile", requireToken(), groupHandler.GetProcessProfile)
	group.PATCH("/processProfile", requireToken(), groupHandler.UpdateProcessProfile)
	group.GET("/fileProfile", requireToken(), groupHandler.GetFileProfile)
	group.PATCH("/fileProfile", requireToken(), groupHandler.UpdateFileProfile)
	group.GET("/filePreProfile", requireToken(), groupHandler.GetPredefinedFileProfile)
	group.GET("/dlp/sensor", requireToken(), groupHandler.GetDlpSensor)
	group.POST("/dlp/sensor", requireToken(), groupHandler.CreateDlpSensor)
	group.PATCH("/dlp/sensor", requireToken(), groupHandler.UpdateDlpSensor)
	group.DELETE("/dlp/sensor", requireToken(), groupHandler.DeleteDlpSensor)
	group.POST("/dlp/sensor/export", requireToken(), groupHandler.ExportDlpSensor("local"))
	group.POST("/dlp/sensor/export-fed", requireToken(), groupHandler.ExportDlpSensor("fed"))
	group.POST("/dlp/sensor/import", requireToken(), groupHandler.ImportDlpSensor("local"))
	group.POST("/dlp/sensor/import-fed", requireToken(), groupHandler.ImportDlpSensor("fed"))
	group.GET("/dlp/group", requireToken(), groupHandler.GetDlpGroup)
	group.PATCH("/dlp/group", requireToken(), groupHandler.UpdateDlpGroup)
	group.GET("/waf/sensor", requireToken(), groupHandler.GetWafSensor)
	group.POST("/waf/sensor", requireToken(), groupHandler.CreateWafSensor)
	group.PATCH("/waf/sensor", requireToken(), groupHandler.UpdateWafSensor)
	group.DELETE("/waf/sensor", requireToken(), groupHandler.DeleteWafSensor)
	group.POST("/waf/sensor/export", requireToken(), groupHandler.ExportWafSensor("local"))
	group.POST("/waf/sensor/export-fed", requireToken(), groupHandler.ExportWafSensor("fed"))
	group.POST("/waf/sensor/import", requireToken(), groupHandler.ImportWafSensor("local"))
	group.POST("/waf/sensor/import-fed", requireToken(), groupHandler.ImportWafSensor("fed"))
	group.GET("/waf/group", requireToken(), groupHandler.GetWafGroup)
	group.PATCH("/waf/group", requireToken(), groupHandler.UpdateWafGroup)
	group.POST("/responsePolicy/export", requireToken(), policyHandler.Export("local"))
	group.POST("/responsePolicy/export-fed", requireToken(), policyHandler.Export("fed"))
	group.POST("/responsePolicy/import", requireToken(), policyHandler.Import("local"))
	group.POST("/responsePolicy/import-fed", requireToken(), policyHandler.Import("fed"))
	group.GET("/responseRule", requireToken(), policyHandler.GetResponseRule)
	group.GET("/responsePolicy", requireToken(), policyHandler.GetResponsePolicy)
	group.DELETE("/responsePolicy", requireToken(), policyHandler.DeleteResponsePolicy)
	group.POST("/responsePolicy", requireToken(), policyHandler.CreateResponsePolicy)
	group.PATCH("/responsePolicy", requireToken(), policyHandler.UpdateResponsePolicy)
	group.POST("/fed-deploy", requireToken(), policyHandler.DeployFederal)
	group.GET("/conditionOption", requireToken(), policyHandler.GetConditionOptions)
	group.POST("/unquarantine", requireToken(), policyHandler.Unquarantine)
	group.PATCH("/policy", requireToken(), policyHandler.UpdatePolicy)
	group.DELETE("/policy", requireToken(), policyHandler.DeletePolicy)
	group.GET("/policy", requireToken(), policyHandler.GetPolicy)
	group.GET("/policy/application", requireToken(), policyHandler.GetPolicyApplications)
	group.GET("/policy/rule", requireToken(), policyHandler.GetPolicyRule)
	group.POST("/policy/rule", requireToken(), policyHandler.CreatePolicyRule)
	group.PATCH("/policy/rule", requireToken(), policyHandler.UpdatePolicyRule)
	group.GET("/policy/graph", requireToken(), policyHandler.GetPolicyGraph)
	group.GET("/admission/rules", requireToken(), policyHandler.GetAdmissionRules)
	group.POST("/admission/rule", requireToken(), policyHandler.CreateAdmissionRule)
	group.PATCH("/admission/rule", requireToken(), policyHandler.UpdateAdmissionRule)
	group.DELETE("/admission/rule", requireToken(), policyHandler.DeleteAdmissionRule)
	group.GET("/admission/options", requireToken(), policyHandler.GetAdmissionOptions)
	group.GET("/admission/state", requireToken(), policyHandler.GetAdmissionState)
	group.PATCH("/admission/state", requireToken(), policyHandler.UpdateAdmissionState)
	group.GET("/admission/test", requireToken(), policyHandler.TestAdmission)
	group.POST("/admission/matching-test", requireToken(), policyHandler.TestAdmissionMatching)
	group.POST("/admission/export", requireToken(), policyHandler.ExportAdmission("local"))
	group.POST("/admission/import", requireToken(), policyHandler.ImportAdmission("local"))
	group.POST("/admission/export-fed", requireToken(), policyHandler.ExportAdmission("fed"))
	group.POST("/admission/import-fed", requireToken(), policyHandler.ImportAdmission("fed"))
	group.POST("/admission/promote", requireToken(), policyHandler.PromoteAdmission)
	group.GET("/scan/status", requireToken(), policyHandler.GetScanStatus)
	group.GET("/scan/host", requireToken(), policyHandler.GetScanHost)
	group.POST("/scan/host", requireToken(), policyHandler.ScanHost)
	group.GET("/scan/platform", requireToken(), policyHandler.GetScanPlatform)
	group.POST("/scan/platform", requireToken(), policyHandler.ScanPlatform)
	group.GET("/scan/config", requireToken(), policyHandler.GetScanConfig)
	group.POST("/scan/config", requireToken(), policyHandler.UpdateScanConfig)
	group.GET("/scan/registry/type", requireToken(), policyHandler.GetRegistryTypes)
	group.POST("/policy/promote", requireToken(), policyHandler.PromotePolicy)
	group.POST("/scan/workload", requireToken(), policyHandler.ScanWorkload)
	group.GET("/scan/workload", requireToken(), policyHandler.GetScanWorkload)
	group.GET("/scan/registry", requireToken(), policyHandler.GetScanRegistry)
	group.DELETE("/scan/registry", requireToken(), policyHandler.DeleteScanRegistry)
	group.GET("/scan/registry/repo", requireToken(), policyHandler.GetRegistryRepo)
	group.POST("/scan/registry/repo", requireToken(), policyHandler.ScanRegistryRepo)
	group.DELETE("/scan/registry/repo", requireToken(), policyHandler.StopRegistryRepo)
	group.GET("/scan/registry/fed-repo", requireToken(), policyHandler.GetFederatedRegistryRepo)
	group.GET("/scan/registry/image", requireToken(), policyHandler.GetRegistryImage)
	group.GET("/scan/registry/layer", requireToken(), policyHandler.GetRegistryLayer)
	group.GET("/scan/top", requireToken(), policyHandler.GetScanTop)
	group.POST("/scan/registry", requireToken(), policyHandler.CreateScanRegistry)
	group.PATCH("/scan/registry", requireToken(), policyHandler.UpdateScanRegistry)
	group.POST("/scan/registry/test", requireToken(), policyHandler.TestScanRegistry)
	group.DELETE("/scan/registry/test", requireToken(), policyHandler.DeleteScanRegistryTest)
	group.POST("/risk/cve/profile/export", requireToken(), riskHandler.Export("vulnerability"))
	group.POST("/risk/cve/profile/import", requireToken(), riskHandler.ImportVulnerability)
	group.GET("/risk/cve/profile", requireToken(), riskHandler.GetVulnerabilityProfiles)
	group.PATCH("/risk/cve/profile", requireToken(), riskHandler.UpdateVulnerabilityProfile)
	group.POST("/risk/cve/profile/entry", requireToken(), riskHandler.AddVulnerabilityEntry)
	group.PATCH("/risk/cve/profile/entry", requireToken(), riskHandler.UpdateVulnerabilityEntry)
	group.DELETE("/risk/cve/profile/entry", requireToken(), riskHandler.DeleteVulnerabilityEntry)
	group.POST("/risk/compliance/profile/export", requireToken(), riskHandler.Export("compliance"))
	group.POST("/risk/compliance/profile/import", requireToken(), riskHandler.ImportCompliance)
	group.GET("/risk/compliance/profile", requireToken(), riskHandler.GetComplianceProfiles)
	group.PATCH("/risk/compliance/profile", requireToken(), riskHandler.UpdateComplianceProfile)
	group.GET("/risk/cve", requireToken(), riskHandler.GetCVE)
	group.PATCH("/risk/cve/assets-view", requireToken(), riskHandler.QueryCVEAssets)
	group.GET("/risk/compliance", requireToken(), riskHandler.GetCompliances)
	group.GET("/risk/compliance/template", requireToken(), riskHandler.GetComplianceTemplate)
	group.GET("/risk/compliance/available_filter", requireToken(), riskHandler.GetAvailableComplianceFilters)
	group.POST("/scanned-assets", requireToken(), riskHandler.QueryScannedAssets)
	group.GET("/scanned-assets", requireToken(), riskHandler.GetScannedAssets)
	group.POST("/vulasset", requireToken(), riskHandler.QueryVulnerabilityAssets)
	group.GET("/vulasset", requireToken(), riskHandler.GetVulnerabilityAssets)
	group.POST("/risk/complianceNIST", requireToken(), riskHandler.QueryNISTCompliances)
	group.POST("/webhook", requireToken(), deviceHandler.CreateWebhook)
	group.PATCH("/webhook", requireToken(), deviceHandler.UpdateWebhook)
	group.DELETE("/webhook", requireToken(), deviceHandler.DeleteWebhook)
	group.GET("/config", requireToken(), deviceHandler.GetConfig)
	group.PATCH("/config", requireToken(), deviceHandler.UpdateConfig)
	group.GET("/config-v2", requireToken(), deviceHandler.GetConfigV2)
	group.PATCH("/config-v2", requireToken(), deviceHandler.UpdateConfigV2)
	group.POST("/remote_repository", requireToken(), deviceHandler.CreateRemoteRepository)
	group.PATCH("/remote_repository", requireToken(), deviceHandler.UpdateRemoteRepository)
	group.DELETE("/remote_repository", requireToken(), deviceHandler.DeleteRemoteRepository)
	group.GET("/sigstore", requireToken(), sigstoreHandler.GetRoots)
	group.POST("/sigstore", requireToken(), sigstoreHandler.CreateRoot)
	group.PATCH("/sigstore", requireToken(), sigstoreHandler.UpdateRoot)
	group.DELETE("/sigstore", requireToken(), sigstoreHandler.DeleteRoot)
	group.GET("/verifier", requireToken(), sigstoreHandler.GetVerifiers)
	group.POST("/verifier", requireToken(), sigstoreHandler.CreateVerifier)
	group.PATCH("/verifier", requireToken(), sigstoreHandler.UpdateVerifier)
	group.DELETE("/verifier", requireToken(), sigstoreHandler.DeleteVerifier)
	group.GET("/workload", requireToken(), workloadHandler.GetWorkloads)
	group.POST("/workload", requireToken(), workloadHandler.UpdateWorkload)
	group.GET("/workload/workload-by-id", requireToken(), workloadHandler.GetWorkloadByID)
	group.GET("/workload/monitor", requireToken(), workloadHandler.UpdateMonitor)
	group.GET("/workload/compliance", requireToken(), workloadHandler.GetCompliance)
	group.Any("/workload/scanned", requireToken(), workloadHandler.GetScannedWorkloads)
	group.POST("/workload/scan-report", requireToken(), workloadHandler.GetScanReport)
	group.GET("/container", requireToken(), workloadHandler.GetContainers)
	group.GET("/container/process", requireToken(), workloadHandler.GetProcesses)
	group.GET("/container/processHistory", requireToken(), workloadHandler.GetProcessHistory)
	group.GET("/domain", requireToken(), workloadHandler.GetDomains)
	group.PATCH("/domain", requireToken(), workloadHandler.UpdateDomain)
	group.POST("/domain", requireToken(), workloadHandler.UpdateDomainSettings)
	group.GET("/sniffer", requireToken(), workloadHandler.GetSniffers)
	group.POST("/sniffer", requireToken(), workloadHandler.CreateSniffer)
	group.PATCH("/sniffer", requireToken(), workloadHandler.StopSniffer)
	group.DELETE("/sniffer", requireToken(), workloadHandler.DeleteSniffer)
	group.GET("/sniffer/pcap", requireToken(), workloadHandler.GetPCAP)
	group.GET("/role2/permission-options", requireToken(), accessHandler.PermissionOptions)
	group.GET("/role2", requireToken(), accessHandler.GetRoles)
	group.POST("/role2", requireToken(), accessHandler.AddRole)
	group.PATCH("/role2", requireToken(), accessHandler.UpdateRole)
	group.DELETE("/role2", requireToken(), accessHandler.DeleteRole)
	group.GET("/api_key", requireToken(), accessHandler.GetAPIKeys)
	group.POST("/api_key", requireToken(), accessHandler.AddOrCreateAPIKey)
	group.DELETE("/api_key", requireToken(), accessHandler.DeleteAPIKey)
	group.POST("/token", requireToken(), accountHandler.ValidateToken)
	group.GET("/user", requireToken(), accountHandler.GetUsers)
	group.POST("/user", requireToken(), accountHandler.AddUser)
	group.PATCH("/user", requireToken(), accountHandler.UpdateUser)
	group.DELETE("/user", requireToken(), accountHandler.DeleteUser)
	group.GET("/password-profile/public", requireToken(), accountHandler.PasswordPublic)
	group.POST("/password-profile/user", requireToken(), accountHandler.UpdateUserBlock)
	group.GET("/password-profile", requireToken(), accountHandler.PasswordProfile)
	group.PATCH("/password-profile", requireToken(), accountHandler.UpdatePasswordProfile)
	group.POST("/eula", requireToken(), accountHandler.SetEULA)
	group.GET("/license", requireToken(), accountHandler.GetLicense)
	group.POST("/license", requireToken(), accountHandler.RequestLicense)
	group.POST("/license/update", requireToken(), accountHandler.UpdateLicense)
	group.GET("/server", requireToken(), accountHandler.GetServers)
	group.POST("/server", requireToken(), accountHandler.AddServer)
	group.PATCH("/server", requireToken(), accountHandler.UpdateServer)
	group.DELETE("/server", requireToken(), accountHandler.DeleteServer)
	group.POST("/debug", requireToken(), accountHandler.TestServer)
}

func requireToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, present := c.Request.Header[http.CanonicalHeaderKey("Token")]; !present {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Token header is required"})
			return
		}
		c.Next()
	}
}

func NewHealthHandler(ready func() bool, metrics ...http.Handler) http.Handler {
	engine := gin.New()
	engine.GET("/livez", func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.GET("/readyz", func(c *gin.Context) {
		if !ready() {
			c.Status(http.StatusServiceUnavailable)
			return
		}
		c.Status(http.StatusOK)
	})
	if len(metrics) > 0 && metrics[0] != nil {
		engine.GET("/metrics", gin.WrapH(metrics[0]))
	}
	return engine
}
