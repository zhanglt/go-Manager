package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Server     ServerConfig
	Controller ControllerConfig
	UI         UIConfig
	Session    SessionConfig
	Cache      CacheConfig
	Support    SupportConfig
	SSO        SSOConfig
}

type ServerConfig struct {
	Address           string
	InternalAddress   string
	TLS               bool
	Development       bool
	CertificateFile   string
	PrivateKeyFile    string
	MaxHeaderBytes    int
	MaxBodyBytes      int64
	MaxConnections    int
	PathPrefix        string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
}

type ControllerConfig struct {
	BaseURL   *url.URL
	TLSVerify bool
	Timeout   time.Duration
}

type UIConfig struct {
	GravatarEnabled         string
	CustomLoginLogo         string
	CustomPolicy            string
	CustomPageHeaderContent string
	CustomPageHeaderColor   string
	CustomPageFooterContent string
	CustomPageFooterColor   string
	EULAOEMAppSafe          bool
}

type SessionConfig struct {
	MaxEntries int
}

type CacheConfig struct {
	MaxEntries int
	MaxBytes   int64
	TTL        time.Duration
}

type SupportConfig struct {
	Command       string
	TempDir       string
	Timeout       time.Duration
	MaxFileBytes  int64
	MaxConcurrent int
}

type SSOConfig struct {
	PublicURL  *url.URL
	TTL        time.Duration
	MaxEntries int
}

func Load() (Config, error) {
	var problems []error

	port, err := parsePort(env("MANAGER_SERVER_PORT", "8443"))
	if err != nil {
		problems = append(problems, fmt.Errorf("MANAGER_SERVER_PORT: %w", err))
	}
	ctrlPort, err := parsePort(env("CTRL_SERVER_PORT", "10443"))
	if err != nil {
		problems = append(problems, fmt.Errorf("CTRL_SERVER_PORT: %w", err))
	}
	managerTLS, err := parseOnOff(env("MANAGER_SSL", "on"))
	if err != nil {
		problems = append(problems, fmt.Errorf("MANAGER_SSL: %w", err))
	}
	tlsVerify, err := strconv.ParseBool(env("CTRL_TLS_VERIFY", "false"))
	if err != nil {
		problems = append(problems, fmt.Errorf("CTRL_TLS_VERIFY: %w", err))
	}
	maxHeader, err := parseBytes(env("HTTP_MAX_HEADER_LENGTH", "32k"))
	if err != nil {
		problems = append(problems, fmt.Errorf("HTTP_MAX_HEADER_LENGTH: %w", err))
	}
	maxBody, err := parseBytes(env("MANAGER_MAX_BODY_BYTES", "50m"))
	if err != nil {
		problems = append(problems, fmt.Errorf("MANAGER_MAX_BODY_BYTES: %w", err))
	}
	maxConnections, err := strconv.Atoi(env("MANAGER_MAX_CONNECTIONS", "1024"))
	if err != nil || maxConnections < 1 {
		problems = append(problems, errors.New("MANAGER_MAX_CONNECTIONS must be a positive integer"))
	}
	readHeaderTimeout, err := positiveDuration("MANAGER_READ_HEADER_TIMEOUT", "10s")
	if err != nil {
		problems = append(problems, err)
	}
	readTimeout, err := positiveDuration("MANAGER_READ_TIMEOUT", "2m")
	if err != nil {
		problems = append(problems, err)
	}
	writeTimeout, err := positiveDuration("MANAGER_WRITE_TIMEOUT", "15m")
	if err != nil {
		problems = append(problems, err)
	}
	idleTimeout, err := positiveDuration("MANAGER_IDLE_TIMEOUT", "2m")
	if err != nil {
		problems = append(problems, err)
	}
	shutdownTimeout, err := time.ParseDuration(env("MANAGER_SHUTDOWN_TIMEOUT", "30s"))
	if err != nil || shutdownTimeout <= 0 {
		problems = append(problems, errors.New("MANAGER_SHUTDOWN_TIMEOUT must be a positive duration"))
	}
	controllerTimeout, err := time.ParseDuration(env("CTRL_REQUEST_TIMEOUT", "60s"))
	if err != nil || controllerTimeout <= 0 {
		problems = append(problems, errors.New("CTRL_REQUEST_TIMEOUT must be a positive duration"))
	}
	sessionMaxEntries, err := strconv.Atoi(env("MANAGER_SESSION_MAX_ENTRIES", "10000"))
	if err != nil || sessionMaxEntries < 1 {
		problems = append(problems, errors.New("MANAGER_SESSION_MAX_ENTRIES must be a positive integer"))
	}
	cacheMaxEntries, err := strconv.Atoi(env("MANAGER_CACHE_MAX_ENTRIES", "1000"))
	if err != nil || cacheMaxEntries < 1 {
		problems = append(problems, errors.New("MANAGER_CACHE_MAX_ENTRIES must be a positive integer"))
	}
	cacheMaxBytes, err := parseBytes(env("MANAGER_CACHE_MAX_BYTES", "64m"))
	if err != nil {
		problems = append(problems, fmt.Errorf("MANAGER_CACHE_MAX_BYTES: %w", err))
	}
	cacheTTL, err := time.ParseDuration(env("MANAGER_CACHE_TTL", "5m"))
	if err != nil || cacheTTL <= 0 {
		problems = append(problems, errors.New("MANAGER_CACHE_TTL must be a positive duration"))
	}
	supportTimeout, err := time.ParseDuration(env("MANAGER_SUPPORT_TIMEOUT", "10m"))
	if err != nil || supportTimeout <= 0 {
		problems = append(problems, errors.New("MANAGER_SUPPORT_TIMEOUT must be a positive duration"))
	}
	supportMaxBytes, err := parseBytes(env("MANAGER_SUPPORT_MAX_FILE_BYTES", "64m"))
	if err != nil {
		problems = append(problems, fmt.Errorf("MANAGER_SUPPORT_MAX_FILE_BYTES: %w", err))
	}
	supportMaxConcurrent, err := strconv.Atoi(env("MANAGER_SUPPORT_MAX_CONCURRENT", "2"))
	if err != nil || supportMaxConcurrent < 1 {
		problems = append(problems, errors.New("MANAGER_SUPPORT_MAX_CONCURRENT must be a positive integer"))
	}
	ssoTTL, err := time.ParseDuration(env("MANAGER_SSO_STATE_TTL", "5m"))
	if err != nil || ssoTTL <= 0 {
		problems = append(problems, errors.New("MANAGER_SSO_STATE_TTL must be a positive duration"))
	}
	ssoMaxEntries, err := strconv.Atoi(env("MANAGER_SSO_MAX_PENDING", "1024"))
	if err != nil || ssoMaxEntries < 1 {
		problems = append(problems, errors.New("MANAGER_SSO_MAX_PENDING must be a positive integer"))
	}
	supportCommand := strings.TrimSpace(env("MANAGER_SUPPORT_COMMAND", "/usr/local/bin/support"))
	if !filepath.IsAbs(supportCommand) {
		problems = append(problems, errors.New("MANAGER_SUPPORT_COMMAND must be an absolute path"))
	}
	supportTempDir := strings.TrimSpace(env("MANAGER_SUPPORT_TEMP_DIR", "/tmp/neuvector-support"))
	if !filepath.IsAbs(supportTempDir) {
		problems = append(problems, errors.New("MANAGER_SUPPORT_TEMP_DIR must be an absolute path"))
	}
	prefix, err := normalizePrefix(os.Getenv("PATH_PREFIX"))
	if err != nil {
		problems = append(problems, fmt.Errorf("PATH_PREFIX: %w", err))
	}
	publicURL, err := parsePublicURL(os.Getenv("MANAGER_PUBLIC_URL"), managerTLS)
	if err != nil {
		problems = append(problems, fmt.Errorf("MANAGER_PUBLIC_URL: %w", err))
	}
	internalAddress := strings.TrimSpace(os.Getenv("MANAGER_INTERNAL_ADDR"))
	if internalAddress != "" {
		if _, _, err := net.SplitHostPort(internalAddress); err != nil {
			problems = append(problems, fmt.Errorf("MANAGER_INTERNAL_ADDR: %w", err))
		}
	}
	ctrlHost := strings.TrimSpace(env("CTRL_SERVER_IP", "127.0.0.1"))
	if ctrlHost == "" || strings.ContainsAny(ctrlHost, "/?#") {
		problems = append(problems, errors.New("CTRL_SERVER_IP must be a host name or IP address"))
	}

	if err := errors.Join(problems...); err != nil {
		return Config{}, err
	}
	baseURL, err := url.Parse("https://" + net.JoinHostPort(ctrlHost, strconv.Itoa(ctrlPort)) + "/v1")
	if err != nil {
		return Config{}, fmt.Errorf("build controller URL: %w", err)
	}

	return Config{
		Server: ServerConfig{
			Address:           net.JoinHostPort("0.0.0.0", strconv.Itoa(port)),
			InternalAddress:   internalAddress,
			TLS:               managerTLS,
			Development:       os.Getenv("IS_DEV") == "true",
			CertificateFile:   env("MANAGER_CERT_FILE", "/etc/neuvector/certs/ssl-cert.pem"),
			PrivateKeyFile:    env("MANAGER_KEY_FILE", "/etc/neuvector/certs/ssl-cert.key"),
			MaxHeaderBytes:    maxHeader,
			MaxBodyBytes:      int64(maxBody),
			MaxConnections:    maxConnections,
			PathPrefix:        prefix,
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			ShutdownTimeout:   shutdownTimeout,
		},
		Controller: ControllerConfig{BaseURL: baseURL, TLSVerify: tlsVerify, Timeout: controllerTimeout},
		UI: UIConfig{
			GravatarEnabled:         env("GRAVATAR_ENABLED", "false"),
			CustomLoginLogo:         os.Getenv("CUSTOM_LOGIN_LOGO"),
			CustomPolicy:            os.Getenv("CUSTOM_EULA_POLICY"),
			CustomPageHeaderContent: os.Getenv("CUSTOM_PAGE_HEADER_CONTENT"),
			CustomPageHeaderColor:   os.Getenv("CUSTOM_PAGE_HEADER_COLOR"),
			CustomPageFooterContent: os.Getenv("CUSTOM_PAGE_FOOTER_CONTENT"),
			CustomPageFooterColor:   os.Getenv("CUSTOM_PAGE_FOOTER_COLOR"),
			EULAOEMAppSafe:          strings.EqualFold(env("EULA_OEM_APPSAFE", "false"), "true"),
		},
		Session: SessionConfig{MaxEntries: sessionMaxEntries},
		Cache: CacheConfig{
			MaxEntries: cacheMaxEntries,
			MaxBytes:   int64(cacheMaxBytes),
			TTL:        cacheTTL,
		},
		Support: SupportConfig{
			Command: supportCommand, TempDir: supportTempDir, Timeout: supportTimeout,
			MaxFileBytes: int64(supportMaxBytes), MaxConcurrent: supportMaxConcurrent,
		},
		SSO: SSOConfig{PublicURL: publicURL, TTL: ssoTTL, MaxEntries: ssoMaxEntries},
	}, nil
}

func positiveDuration(name, fallback string) (time.Duration, error) {
	value, err := time.ParseDuration(env(name, fallback))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return value, nil
}

func parsePublicURL(value string, tlsEnabled bool) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := url.Parse(value)
	if err != nil || !parsed.IsAbs() || parsed.Hostname() == "" {
		return nil, errors.New("must be an absolute HTTP(S) origin")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("must not contain credentials, a path, query, or fragment")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && !tlsEnabled) {
		return nil, errors.New("must use https when MANAGER_SSL is on")
	}
	parsed.Path = ""
	return parsed, nil
}

func env(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}

func parsePort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("must be an integer from 1 to 65535, got %q", value)
	}
	return port, nil
}

func parseOnOff(value string) (bool, error) {
	switch value {
	case "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return false, fmt.Errorf("must be %q or %q, got %q", "on", "off", value)
	}
}

func parseBytes(value string) (int, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	multiplier := int64(1)
	if strings.HasSuffix(normalized, "k") {
		multiplier = 1024
		normalized = strings.TrimSuffix(normalized, "k")
	} else if strings.HasSuffix(normalized, "m") {
		multiplier = 1024 * 1024
		normalized = strings.TrimSuffix(normalized, "m")
	}
	n, err := strconv.ParseInt(normalized, 10, 32)
	if err != nil || n <= 0 || n*multiplier > int64(^uint(0)>>1) {
		return 0, fmt.Errorf("must be a positive byte count with optional k or m suffix, got %q", value)
	}
	return int(n * multiplier), nil
}

func normalizePrefix(value string) (string, error) {
	prefix := strings.TrimSpace(value)
	if prefix == "" || prefix == "/" {
		return "", nil
	}
	if strings.ContainsAny(prefix, "?#") || strings.Contains(prefix, "\\") {
		return "", errors.New("must be a URL path without query, fragment, or backslash")
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	prefix = strings.TrimRight(prefix, "/")
	for _, segment := range strings.Split(strings.TrimPrefix(prefix, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", errors.New("must not contain empty, dot, or parent segments")
		}
	}
	return prefix, nil
}
