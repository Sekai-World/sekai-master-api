package config

import (
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                 string
	AppEnv               string
	DatabaseDriverName   string
	DatabaseURL          string
	SQLitePath           string
	MasterDataAutoSync   bool
	MasterDataSyncTimeout int
	MasterDataCacheBackend string
	MasterDataGitHubToken string
	MasterDataHTTPTimeout int
	MasterDataRegions    []string
	MasterDataSources    map[string]MasterDataSource
	RedisAddr            string
	RedisPassword        string
	RedisDB              int
	MasterDataRedisKeyPrefix string
	KeycloakBaseURL      string
	KeycloakRealm        string
	KeycloakClientID     string
	KeycloakIssuerURL    string
	KeycloakAudience     string
	KeycloakSkipIssuer   bool
	KeycloakSkipAudCheck bool
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

	return Config{
		Port:                 getEnv("APP_PORT", "8080"),
		AppEnv:               getEnv("APP_ENV", "development"),
		DatabaseDriverName:   getEnv("DATABASE_DRIVER", ""),
		DatabaseURL:          getEnv("DATABASE_URL", "postgres://sekai:sekai@localhost:5432/sekai?sslmode=disable"),
		SQLitePath:           getEnv("SQLITE_PATH", "./tmp/dev.db"),
		MasterDataAutoSync:   getEnvBool("MASTER_DATA_AUTO_SYNC", true),
		MasterDataSyncTimeout: getEnvInt("MASTER_DATA_SYNC_TIMEOUT_SECONDS", 120),
		MasterDataCacheBackend: strings.ToLower(getEnv("MASTER_DATA_CACHE_BACKEND", "memory")),
		MasterDataGitHubToken: strings.TrimSpace(getEnv("MASTER_DATA_GITHUB_TOKEN", "")),
		MasterDataHTTPTimeout: getEnvInt("MASTER_DATA_HTTP_TIMEOUT_SECONDS", 20),
		MasterDataRegions:    getEnvList("MASTER_DATA_REGIONS"),
		MasterDataSources:    loadMasterDataSources(getEnvList("MASTER_DATA_REGIONS")),
		RedisAddr:            getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:        getEnv("REDIS_PASSWORD", ""),
		RedisDB:              getEnvInt("REDIS_DB", 0),
		MasterDataRedisKeyPrefix: getEnv("MASTER_DATA_REDIS_KEY_PREFIX", "sekai:master-data:"),
		KeycloakBaseURL:      getEnv("KEYCLOAK_BASE_URL", "http://localhost:8081"),
		KeycloakRealm:        getEnv("KEYCLOAK_REALM", "sekai"),
		KeycloakClientID:     getEnv("KEYCLOAK_CLIENT_ID", "sekai-api"),
		KeycloakIssuerURL:    getEnv("KEYCLOAK_ISSUER_URL", "http://localhost:8081/realms/sekai"),
		KeycloakAudience:     getEnv("KEYCLOAK_AUDIENCE", "sekai-api"),
		KeycloakSkipIssuer:   strings.EqualFold(getEnv("KEYCLOAK_SKIP_ISSUER_CHECK", "false"), "true"),
		KeycloakSkipAudCheck: strings.EqualFold(getEnv("KEYCLOAK_SKIP_AUDIENCE_CHECK", "false"), "true"),
	}
}

func loadEnvFiles() {
	appEnv := strings.TrimSpace(os.Getenv("APP_ENV"))
	if appEnv == "" {
		envMap, err := godotenv.Read(".env")
		if err == nil {
			appEnv = strings.TrimSpace(envMap["APP_ENV"])
		}
	}

	if appEnv == "" {
		appEnv = "development"
	}

	_ = godotenv.Load(".env." + appEnv)
	_ = godotenv.Load(".env")
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
