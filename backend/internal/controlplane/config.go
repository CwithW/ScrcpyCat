package controlplane

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddress              string
	JWTSecret                  []byte
	BootstrapAdminUser         string
	BootstrapAdminPass         string
	AllowRegistration          bool
	PublicURL                  string
	TurnURLs                   []string
	TurnUsername               string
	TurnCredential             string
	CORSOrigins                []string
	PostgresDSN                string
	RedisURL                   string
	AssetDir                   string
	AgentDir                   string
	ScrcpyJar                  string
	TaskTTL                    time.Duration
	AuditRetention             time.Duration
	MaxAssetBytes              int64
	AgentHelloTimeout          time.Duration
	AgentHelloMaxBytes         int64
	MaxPendingAgentHandshakes  int
	LoginAttemptWindow         time.Duration
	LoginMaxAttemptsPerAccount int
	LoginMaxAttemptsPerIP      int
	TrustedProxyCIDRs          []netip.Prefix
}

const (
	defaultAgentHelloTimeout                = 10 * time.Second
	defaultAgentHelloMaxBytes         int64 = 64 << 10
	defaultMaxPendingAgentHandshakes        = 64
	defaultLoginAttemptWindow               = 15 * time.Minute
	defaultLoginMaxAttemptsPerAccount       = 10
	defaultLoginMaxAttemptsPerIP            = 60
)

func LoadConfigFromEnv() (Config, error) {
	secret := strings.TrimSpace(os.Getenv("SCRCPYCAT_JWT_SECRET"))
	if secret == "" {
		buf := make([]byte, 48)
		if _, err := rand.Read(buf); err != nil {
			return Config{}, fmt.Errorf("generate development JWT secret: %w", err)
		}
		secret = base64.RawURLEncoding.EncodeToString(buf)
	}

	allowRegistration, err := strconv.ParseBool(valueOr("SCRCPYCAT_ALLOW_REGISTRATION", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("parse SCRCPYCAT_ALLOW_REGISTRATION: %w", err)
	}
	taskTTL, err := time.ParseDuration(valueOr("SCRCPYCAT_TASK_TTL", "24h"))
	if err != nil || taskTTL <= 0 {
		return Config{}, fmt.Errorf("parse SCRCPYCAT_TASK_TTL")
	}
	auditRetention, err := time.ParseDuration(valueOr("SCRCPYCAT_AUDIT_RETENTION", "2160h"))
	if err != nil || auditRetention <= 0 {
		return Config{}, fmt.Errorf("parse SCRCPYCAT_AUDIT_RETENTION")
	}
	maxAssetBytes, err := strconv.ParseInt(valueOr("SCRCPYCAT_MAX_ASSET_BYTES", "2147483648"), 10, 64)
	if err != nil || maxAssetBytes <= 0 {
		return Config{}, fmt.Errorf("parse SCRCPYCAT_MAX_ASSET_BYTES")
	}
	agentHelloTimeout, err := time.ParseDuration(valueOr("SCRCPYCAT_AGENT_HELLO_TIMEOUT", defaultAgentHelloTimeout.String()))
	if err != nil || agentHelloTimeout <= 0 {
		return Config{}, fmt.Errorf("parse SCRCPYCAT_AGENT_HELLO_TIMEOUT")
	}
	agentHelloMaxBytes, err := strconv.ParseInt(valueOr("SCRCPYCAT_AGENT_HELLO_MAX_BYTES", strconv.FormatInt(defaultAgentHelloMaxBytes, 10)), 10, 64)
	if err != nil || agentHelloMaxBytes <= 0 {
		return Config{}, fmt.Errorf("parse SCRCPYCAT_AGENT_HELLO_MAX_BYTES")
	}
	maxPendingAgentHandshakes, err := strconv.Atoi(valueOr("SCRCPYCAT_MAX_PENDING_AGENT_HANDSHAKES", strconv.Itoa(defaultMaxPendingAgentHandshakes)))
	if err != nil || maxPendingAgentHandshakes <= 0 {
		return Config{}, fmt.Errorf("parse SCRCPYCAT_MAX_PENDING_AGENT_HANDSHAKES")
	}
	loginAttemptWindow, err := time.ParseDuration(valueOr("SCRCPYCAT_LOGIN_ATTEMPT_WINDOW", defaultLoginAttemptWindow.String()))
	if err != nil || loginAttemptWindow <= 0 {
		return Config{}, fmt.Errorf("parse SCRCPYCAT_LOGIN_ATTEMPT_WINDOW")
	}
	loginMaxAttemptsPerAccount, err := strconv.Atoi(valueOr("SCRCPYCAT_LOGIN_MAX_ATTEMPTS_PER_ACCOUNT", strconv.Itoa(defaultLoginMaxAttemptsPerAccount)))
	if err != nil || loginMaxAttemptsPerAccount <= 0 {
		return Config{}, fmt.Errorf("parse SCRCPYCAT_LOGIN_MAX_ATTEMPTS_PER_ACCOUNT")
	}
	loginMaxAttemptsPerIP, err := strconv.Atoi(valueOr("SCRCPYCAT_LOGIN_MAX_ATTEMPTS_PER_IP", strconv.Itoa(defaultLoginMaxAttemptsPerIP)))
	if err != nil || loginMaxAttemptsPerIP <= 0 {
		return Config{}, fmt.Errorf("parse SCRCPYCAT_LOGIN_MAX_ATTEMPTS_PER_IP")
	}
	trustedProxyCIDRs, err := parseCIDRs(os.Getenv("SCRCPYCAT_TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return Config{}, fmt.Errorf("parse SCRCPYCAT_TRUSTED_PROXY_CIDRS: %w", err)
	}

	return Config{
		ListenAddress:              valueOr("SCRCPYCAT_LISTEN_ADDRESS", ":8443"),
		JWTSecret:                  []byte(secret),
		BootstrapAdminUser:         valueOr("SCRCPYCAT_ADMIN_USERNAME", "admin"),
		BootstrapAdminPass:         valueOr("SCRCPYCAT_ADMIN_PASSWORD", "change-me-before-production"),
		AllowRegistration:          allowRegistration,
		PublicURL:                  strings.TrimRight(valueOr("SCRCPYCAT_PUBLIC_URL", "https://localhost:8443"), "/"),
		TurnURLs:                   splitCSV(os.Getenv("SCRCPYCAT_TURN_URLS")),
		TurnUsername:               os.Getenv("SCRCPYCAT_TURN_USERNAME"),
		TurnCredential:             os.Getenv("SCRCPYCAT_TURN_CREDENTIAL"),
		CORSOrigins:                splitCSV(os.Getenv("SCRCPYCAT_CORS_ORIGINS")),
		PostgresDSN:                strings.TrimSpace(os.Getenv("SCRCPYCAT_POSTGRES_DSN")),
		RedisURL:                   strings.TrimSpace(os.Getenv("SCRCPYCAT_REDIS_URL")),
		AssetDir:                   valueOr("SCRCPYCAT_ASSET_DIR", "/var/lib/scrcpycat/assets"),
		AgentDir:                   valueOr("SCRCPYCAT_AGENT_DIR", "bin/agent"),
		ScrcpyJar:                  valueOr("SCRCPYCAT_SCRCPY_JAR", "bin/scrcpy-server/scrcpy-server"),
		TaskTTL:                    taskTTL,
		AuditRetention:             auditRetention,
		MaxAssetBytes:              maxAssetBytes,
		AgentHelloTimeout:          agentHelloTimeout,
		AgentHelloMaxBytes:         agentHelloMaxBytes,
		MaxPendingAgentHandshakes:  maxPendingAgentHandshakes,
		LoginAttemptWindow:         loginAttemptWindow,
		LoginMaxAttemptsPerAccount: loginMaxAttemptsPerAccount,
		LoginMaxAttemptsPerIP:      loginMaxAttemptsPerIP,
		TrustedProxyCIDRs:          trustedProxyCIDRs,
	}, nil
}

func (c Config) withSecurityDefaults() Config {
	if c.AgentHelloTimeout <= 0 {
		c.AgentHelloTimeout = defaultAgentHelloTimeout
	}
	if c.AgentHelloMaxBytes <= 0 {
		c.AgentHelloMaxBytes = defaultAgentHelloMaxBytes
	}
	if c.MaxPendingAgentHandshakes <= 0 {
		c.MaxPendingAgentHandshakes = defaultMaxPendingAgentHandshakes
	}
	if c.LoginAttemptWindow <= 0 {
		c.LoginAttemptWindow = defaultLoginAttemptWindow
	}
	if c.LoginMaxAttemptsPerAccount <= 0 {
		c.LoginMaxAttemptsPerAccount = defaultLoginMaxAttemptsPerAccount
	}
	if c.LoginMaxAttemptsPerIP <= 0 {
		c.LoginMaxAttemptsPerIP = defaultLoginMaxAttemptsPerIP
	}
	return c
}

func parseCIDRs(value string) ([]netip.Prefix, error) {
	items := splitCSV(value)
	result := make([]netip.Prefix, 0, len(items))
	for _, item := range items {
		prefix, err := netip.ParsePrefix(item)
		if err != nil {
			return nil, err
		}
		result = append(result, prefix.Masked())
	}
	return result, nil
}

func valueOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
