package config

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                 string
	AppEnv               string
	DatabaseDriverName   string
	DatabaseURL          string
	SQLitePath           string
	KeycloakBaseURL      string
	KeycloakRealm        string
	KeycloakClientID     string
	KeycloakIssuerURL    string
	KeycloakAudience     string
	KeycloakSkipIssuer   bool
	KeycloakSkipAudCheck bool
}

func Load() Config {
	loadEnvFiles()

	return Config{
		Port:                 getEnv("APP_PORT", "8080"),
		AppEnv:               getEnv("APP_ENV", "development"),
		DatabaseDriverName:   getEnv("DATABASE_DRIVER", ""),
		DatabaseURL:          getEnv("DATABASE_URL", "postgres://sekai:sekai@localhost:5432/sekai?sslmode=disable"),
		SQLitePath:           getEnv("SQLITE_PATH", "./tmp/dev.db"),
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
