package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                         string
	AppEnv                       string
	LogLevel                     string
	LokiPushURL                  string
	DatabaseDriverName           string
	DatabaseURL                  string
	SQLitePath                   string
	MasterDataAutoSync           bool
	MasterDataSyncTimeout        int
	MasterDataSyncConcurrency    int
	MasterDataFileConcurrency    int
	MasterDataGitHubToken        string
	MasterDataHTTPTimeout        int
	MasterDataHTTPRetryCount     int
	MasterDataHTTPRetryBackoffMS int
	MasterDataRegions            []string
	MasterDataSources            map[string]MasterDataSource
	RedisAddr                    string
	RedisPassword                string
	RedisDB                      int
	MasterDataRedisKeyPrefix     string
	ZitadelIssuerURL             string
	ZitadelInternalURL           string
	ZitadelAudience              string
	ZitadelSkipIssuer            bool
	ZitadelSkipAudCheck          bool
	ZitadelClientID              string
	ZitadelAuthURL               string
	ZitadelTokenURL              string
	ZitadelRedirectURL           string
	ZitadelScopes                []string
	ZitadelPrivateKeyPath        string
	ZitadelPrivateKeyID          string
}

type MasterDataSource struct {
	Region string
	Owner  string
	Repo   string
	Ref    string
	Path   string
}

func Load() Config {
	loadEnvFiles()
	appEnv := getEnv("APP_ENV", "development")
	port := getEnv("APP_PORT", "8080")
	logLevel := resolveLogLevel(strings.TrimSpace(getEnv("LOG_LEVEL", "")), appEnv)

	return Config{
		Port:                         port,
		AppEnv:                       appEnv,
		LogLevel:                     logLevel,
		LokiPushURL:                  strings.TrimSpace(getEnv("LOKI_PUSH_URL", "")),
		DatabaseDriverName:           getEnv("DATABASE_DRIVER", ""),
		DatabaseURL:                  getEnv("DATABASE_URL", "postgres://sekai:sekai@localhost:5432/sekai?sslmode=disable"),
		SQLitePath:                   getEnv("SQLITE_PATH", "./tmp/dev.db"),
		MasterDataAutoSync:           getEnvBool("MASTER_DATA_AUTO_SYNC", true),
		MasterDataSyncTimeout:        getEnvInt("MASTER_DATA_SYNC_TIMEOUT_SECONDS", 120),
		MasterDataSyncConcurrency:    getEnvInt("MASTER_DATA_SYNC_CONCURRENCY", 4),
		MasterDataFileConcurrency:    getEnvInt("MASTER_DATA_REGION_FILE_CONCURRENCY", 8),
		MasterDataGitHubToken:        strings.TrimSpace(getEnv("MASTER_DATA_GITHUB_TOKEN", "")),
		MasterDataHTTPTimeout:        getEnvInt("MASTER_DATA_HTTP_TIMEOUT_SECONDS", 20),
		MasterDataHTTPRetryCount:     getEnvInt("MASTER_DATA_HTTP_RETRY_COUNT", 3),
		MasterDataHTTPRetryBackoffMS: getEnvInt("MASTER_DATA_HTTP_RETRY_BACKOFF_MS", 300),
		MasterDataRegions:            getEnvList("MASTER_DATA_REGIONS"),
		MasterDataSources:            loadMasterDataSources(getEnvList("MASTER_DATA_REGIONS")),
		RedisAddr:                    getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:                getEnv("REDIS_PASSWORD", ""),
		RedisDB:                      getEnvInt("REDIS_DB", 0),
		MasterDataRedisKeyPrefix:     getEnv("MASTER_DATA_REDIS_KEY_PREFIX", "sekai:master-data:"),
		ZitadelIssuerURL:             strings.TrimSpace(getEnv("ZITADEL_ISSUER_URL", "")),
		ZitadelInternalURL:           strings.TrimSpace(getEnv("ZITADEL_INTERNAL_URL", "")),
		ZitadelAudience:              strings.TrimSpace(getEnv("ZITADEL_AUDIENCE", "")),
		ZitadelSkipIssuer:            strings.EqualFold(getEnv("ZITADEL_SKIP_ISSUER_CHECK", "false"), "true"),
		ZitadelSkipAudCheck:          strings.EqualFold(getEnv("ZITADEL_SKIP_AUDIENCE_CHECK", "false"), "true"),
		ZitadelClientID:              strings.TrimSpace(getEnv("ZITADEL_CLIENT_ID", "")),
		ZitadelAuthURL:               strings.TrimSpace(getEnv("ZITADEL_AUTH_URL", "")),
		ZitadelTokenURL:              strings.TrimSpace(getEnv("ZITADEL_TOKEN_URL", "")),
		ZitadelRedirectURL:           strings.TrimSpace(getEnv("ZITADEL_REDIRECT_URL", "http://localhost:"+port+"/api/v1/admin/login/callback")),
		ZitadelScopes:                getEnvListWithFallback("ZITADEL_SCOPES", []string{"openid", "profile", "email"}),
		ZitadelPrivateKeyPath:        strings.TrimSpace(getEnv("ZITADEL_PRIVATE_KEY_PATH", "")),
		ZitadelPrivateKeyID:          strings.TrimSpace(getEnv("ZITADEL_PRIVATE_KEY_ID", "")),
	}
}

func loadEnvFiles() {
	appEnv := detectAppEnv()
	for _, path := range dotenvLoadOrder(appEnv) {
		_ = godotenv.Load(path)
	}
}

func detectAppEnv() string {
	if appEnv := strings.TrimSpace(os.Getenv("APP_ENV")); appEnv != "" {
		return appEnv
	}

	for _, path := range []string{".env.local", ".env"} {
		envMap, err := godotenv.Read(path)
		if err != nil {
			continue
		}

		if appEnv := strings.TrimSpace(envMap["APP_ENV"]); appEnv != "" {
			return appEnv
		}
	}

	return "development"
}

func dotenvLoadOrder(appEnv string) []string {
	normalizedEnv := strings.ToLower(strings.TrimSpace(appEnv))
	if normalizedEnv == "" {
		normalizedEnv = "development"
	}

	return []string{
		fmt.Sprintf(".env.%s.local", normalizedEnv),
		".env.local",
		fmt.Sprintf(".env.%s", normalizedEnv),
		".env",
	}
}

func (cfg Config) IsDevelopment() bool {
	return strings.EqualFold(cfg.AppEnv, "development") || strings.EqualFold(cfg.AppEnv, "dev")
}

func (cfg Config) DatabaseDriver() string {
	driver := strings.ToLower(strings.TrimSpace(cfg.DatabaseDriverName))
	switch driver {
	case "sqlite":
		return "sqlite"
	case "pgx", "postgres", "postgresql":
		return "pgx"
	}

	if cfg.IsDevelopment() {
		return "sqlite"
	}
	return "pgx"
}

func (cfg Config) EffectiveDatabaseDSN() string {
	if cfg.DatabaseDriver() == "sqlite" {
		return cfg.SQLitePath
	}
	return cfg.DatabaseURL
}

func (cfg Config) NormalizedZitadelIssuerURL() string {
	return normalizeZitadelIssuerURL(cfg.ZitadelIssuerURL)
}

func (cfg Config) NormalizedZitadelInternalURL() string {
	return normalizeZitadelIssuerURL(cfg.ZitadelInternalURL)
}

func (cfg Config) ZitadelAuthorizationURL() string {
	if cfg.ZitadelAuthURL != "" {
		return cfg.ZitadelAuthURL
	}

	return cfg.NormalizedZitadelIssuerURL() + "/oauth/v2/authorize"
}

func (cfg Config) ZitadelTokenEndpoint() string {
	if cfg.ZitadelTokenURL != "" {
		return cfg.ZitadelTokenURL
	}

	return cfg.NormalizedZitadelIssuerURL() + "/oauth/v2/token"
}

func normalizeZitadelIssuerURL(raw string) string {
	issuer := strings.TrimRight(strings.TrimSpace(raw), "/")
	if issuer == "" {
		return ""
	}

	for _, suffix := range []string{
		"/.well-known/openid-configuration",
		"/oauth/v2/authorize",
		"/oauth/v2/token",
		"/oauth/v2/userinfo",
	} {
		if strings.HasSuffix(strings.ToLower(issuer), suffix) {
			return issuer[:len(issuer)-len(suffix)]
		}
	}

	return issuer
}

func getEnv(key string, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	return value
}

func getEnvBool(key string, fallback bool) bool {
	value := strings.TrimSpace(getEnv(key, ""))
	if value == "" {
		return fallback
	}

	return strings.EqualFold(value, "true") || value == "1" || strings.EqualFold(value, "yes")
}

func getEnvInt(key string, fallback int) int {
	value := strings.TrimSpace(getEnv(key, ""))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return parsed
}

func getEnvList(key string) []string {
	raw := strings.TrimSpace(getEnv(key, ""))
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		values = append(values, value)
	}

	return values
}

func getEnvListWithFallback(key string, fallback []string) []string {
	values := getEnvList(key)
	if len(values) > 0 {
		return values
	}

	return append([]string(nil), fallback...)
}

func loadMasterDataSources(regions []string) map[string]MasterDataSource {
	sources := make(map[string]MasterDataSource)
	for _, region := range regions {
		normalizedRegion := strings.ToLower(strings.TrimSpace(region))
		if normalizedRegion == "" {
			continue
		}

		envSuffix := regionEnvSuffix(normalizedRegion)
		owner := strings.TrimSpace(getEnv("MASTER_DATA_GITHUB_OWNER_"+envSuffix, ""))
		repo := strings.TrimSpace(getEnv("MASTER_DATA_GITHUB_REPO_"+envSuffix, ""))
		if owner == "" || repo == "" {
			continue
		}

		source := MasterDataSource{
			Region: normalizedRegion,
			Owner:  owner,
			Repo:   repo,
			Ref:    strings.TrimSpace(getEnv("MASTER_DATA_GITHUB_REF_"+envSuffix, "main")),
			Path:   strings.Trim(strings.TrimSpace(getEnv("MASTER_DATA_GITHUB_PATH_"+envSuffix, "")), "/"),
		}
		sources[normalizedRegion] = source
	}

	return sources
}

func regionEnvSuffix(region string) string {
	re := regexp.MustCompile(`[^A-Z0-9]+`)
	upper := strings.ToUpper(strings.TrimSpace(region))
	normalized := re.ReplaceAllString(upper, "_")
	return strings.Trim(normalized, "_")
}

func resolveLogLevel(explicitLogLevel string, appEnv string) string {
	trimmedLevel := strings.ToLower(strings.TrimSpace(explicitLogLevel))
	if trimmedLevel != "" {
		return trimmedLevel
	}

	if isProductionEnv(appEnv) {
		return "info"
	}

	return "debug"
}

func isProductionEnv(appEnv string) bool {
	normalized := strings.ToLower(strings.TrimSpace(appEnv))
	return normalized == "production" || normalized == "prod"
}
