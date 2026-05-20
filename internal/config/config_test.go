package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUsesDotenvLocalPrecedence(t *testing.T) {
	restoreEnv(t, "APP_ENV", "APP_PORT", "MASTER_DATA_RECOVER_INTERRUPTED_SYNC", "MASTER_DATA_SYNC_CONCURRENCY", "OTEL_ENABLED")

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, ".env"), "APP_ENV=development\nAPP_PORT=1000\n")
	writeFile(t, filepath.Join(tmpDir, ".env.local"), "APP_PORT=2000\n")
	writeFile(t, filepath.Join(tmpDir, ".env.development"), "APP_PORT=3000\n")
	writeFile(t, filepath.Join(tmpDir, ".env.development.local"), "APP_PORT=4000\n")

	chdir(t, tmpDir)

	cfg := Load()
	if cfg.AppEnv != "development" {
		t.Fatalf("expected development app env, got %q", cfg.AppEnv)
	}
	if cfg.Port != "4000" {
		t.Fatalf("expected APP_PORT from .env.development.local, got %q", cfg.Port)
	}
	if !cfg.MasterDataRecoverInterrupted {
		t.Fatalf("expected interrupted sync recovery to default true")
	}
	if cfg.MasterDataSyncConcurrency != 3 {
		t.Fatalf("expected master data sync concurrency to default to 3, got %d", cfg.MasterDataSyncConcurrency)
	}
	if !cfg.OTELEnabled {
		t.Fatalf("expected OTel to default enabled in development")
	}
}

func TestLoadKeepsShellEnvHighestPrecedence(t *testing.T) {
	restoreEnv(t, "APP_ENV", "APP_PORT")

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, ".env"), "APP_ENV=development\nAPP_PORT=1000\n")
	writeFile(t, filepath.Join(tmpDir, ".env.development.local"), "APP_PORT=4000\n")

	chdir(t, tmpDir)

	if err := os.Setenv("APP_PORT", "5000"); err != nil {
		t.Fatalf("set APP_PORT: %v", err)
	}

	cfg := Load()
	if cfg.Port != "5000" {
		t.Fatalf("expected APP_PORT from shell env, got %q", cfg.Port)
	}
}

func TestLoadDefaultsDevelopmentPortAwayFromCommonConflicts(t *testing.T) {
	restoreEnv(t, "APP_ENV", "APP_PORT")

	tmpDir := t.TempDir()
	chdir(t, tmpDir)

	cfg := Load()
	if cfg.Port != "18080" {
		t.Fatalf("expected development APP_PORT default to avoid 8080, got %q", cfg.Port)
	}
}

func TestLoadDefaultsProductionPortToInternalContainerPort(t *testing.T) {
	restoreEnv(t, "APP_ENV", "APP_PORT")

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, ".env"), "APP_ENV=production\n")
	chdir(t, tmpDir)

	cfg := Load()
	if cfg.Port != "8080" {
		t.Fatalf("expected production APP_PORT default to remain 8080, got %q", cfg.Port)
	}
}

func TestDetectAppEnvPrefersDotenvLocalOverDotenv(t *testing.T) {
	restoreEnv(t, "APP_ENV")

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, ".env"), "APP_ENV=test\n")
	writeFile(t, filepath.Join(tmpDir, ".env.local"), "APP_ENV=production\n")

	chdir(t, tmpDir)

	appEnv := detectAppEnv()
	if appEnv != "production" {
		t.Fatalf("expected APP_ENV from .env.local, got %q", appEnv)
	}
}

func TestNormalizedOIDCIssuerURLStripsKnownSuffixes(t *testing.T) {
	testCases := map[string]string{
		"https://auth.example.com/oauth/v2/authorize":               "https://auth.example.com",
		"https://auth.example.com/oauth/v2/token":                   "https://auth.example.com",
		"https://auth.example.com/.well-known/openid-configuration": "https://auth.example.com",
		"https://auth.example.com/oauth/v2/userinfo":                "https://auth.example.com",
		"https://auth.example.com/tenant":                           "https://auth.example.com/tenant",
		"https://auth.example.com/application/o/sekai-admin-web/":   "https://auth.example.com/application/o/sekai-admin-web/",
	}

	for input, want := range testCases {
		cfg := Config{OIDCIssuerURL: input}
		if got := cfg.NormalizedOIDCIssuerURL(); got != want {
			t.Fatalf("normalized issuer for %q = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizedOIDCInternalURLStripsKnownSuffixes(t *testing.T) {
	cfg := Config{OIDCInternalURL: "http://host.docker.internal:18081/oauth/v2/token"}

	if got := cfg.NormalizedOIDCInternalURL(); got != "http://host.docker.internal:18081" {
		t.Fatalf("normalized internal issuer = %q, want %q", got, "http://host.docker.internal:18081")
	}
}

func TestOIDCAuthorizationURLUsesExplicitValue(t *testing.T) {
	cfg := Config{OIDCAuthURL: "https://auth.example.com/application/o/authorize/"}

	if got := cfg.OIDCAuthorizationURL(); got != "https://auth.example.com/application/o/authorize/" {
		t.Fatalf("authorization url = %q, want %q", got, "https://auth.example.com/application/o/authorize/")
	}
}

func TestOIDCTokenEndpointUsesExplicitValue(t *testing.T) {
	cfg := Config{OIDCTokenURL: "https://auth.example.com/application/o/token/"}

	if got := cfg.OIDCTokenEndpoint(); got != "https://auth.example.com/application/o/token/" {
		t.Fatalf("token endpoint = %q, want %q", got, "https://auth.example.com/application/o/token/")
	}
}

func TestLoadReadsOIDCAdminClaimConfig(t *testing.T) {
	restoreEnv(t, "APP_ENV", "OIDC_ADMIN_CLAIM", "OIDC_ADMIN_CLAIM_VALUES")

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, ".env"), "APP_ENV=development\nOIDC_ADMIN_CLAIM=groups\nOIDC_ADMIN_CLAIM_VALUES=sekai-admin,ops-admin\n")

	chdir(t, tmpDir)

	cfg := Load()
	if cfg.OIDCAdminClaim != "groups" {
		t.Fatalf("OIDC admin claim = %q, want %q", cfg.OIDCAdminClaim, "groups")
	}
	if len(cfg.OIDCAdminClaimValues) != 2 || cfg.OIDCAdminClaimValues[0] != "sekai-admin" || cfg.OIDCAdminClaimValues[1] != "ops-admin" {
		t.Fatalf("OIDC admin claim values = %v, want %v", cfg.OIDCAdminClaimValues, []string{"sekai-admin", "ops-admin"})
	}
}

func TestLoadReadsMasterDataRecoverInterruptedSync(t *testing.T) {
	restoreEnv(t, "APP_ENV", "MASTER_DATA_RECOVER_INTERRUPTED_SYNC")

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, ".env"), "APP_ENV=development\nMASTER_DATA_RECOVER_INTERRUPTED_SYNC=false\n")

	chdir(t, tmpDir)

	cfg := Load()
	if cfg.MasterDataRecoverInterrupted {
		t.Fatalf("expected interrupted sync recovery to be disabled by env override")
	}
}

func TestLoadReadsMasterDataGitHubWebhookSecret(t *testing.T) {
	restoreEnv(t, "APP_ENV", "MASTER_DATA_GITHUB_WEBHOOK_SECRET")

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, ".env"), "APP_ENV=development\nMASTER_DATA_GITHUB_WEBHOOK_SECRET=secret-value\n")

	chdir(t, tmpDir)

	cfg := Load()
	if cfg.MasterDataGitHubWebhookSecret != "secret-value" {
		t.Fatalf("expected github webhook secret to be loaded, got %q", cfg.MasterDataGitHubWebhookSecret)
	}
}

func TestLoadReadsOTELConfig(t *testing.T) {
	restoreEnv(
		t,
		"APP_ENV",
		"OTEL_ENABLED",
		"OTEL_SERVICE_NAME",
		"OTEL_SERVICE_VERSION",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_INSECURE",
		"OTEL_METRIC_EXPORT_INTERVAL",
	)

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, ".env"), ""+
		"APP_ENV=production\n"+
		"OTEL_ENABLED=true\n"+
		"OTEL_SERVICE_NAME=sekai-master-api-dev\n"+
		"OTEL_SERVICE_VERSION=1.2.3\n"+
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://host.docker.internal:4318\n"+
		"OTEL_EXPORTER_OTLP_INSECURE=true\n"+
		"OTEL_METRIC_EXPORT_INTERVAL=5000\n")

	chdir(t, tmpDir)

	cfg := Load()
	if !cfg.OTELEnabled {
		t.Fatalf("expected OTel to be enabled")
	}
	if cfg.OTELServiceName != "sekai-master-api-dev" {
		t.Fatalf("OTEL service name = %q, want %q", cfg.OTELServiceName, "sekai-master-api-dev")
	}
	if cfg.OTELServiceVersion != "1.2.3" {
		t.Fatalf("OTEL service version = %q, want %q", cfg.OTELServiceVersion, "1.2.3")
	}
	if cfg.OTELExporterOTLPEndpoint != "http://host.docker.internal:4318" {
		t.Fatalf("OTEL exporter endpoint = %q, want %q", cfg.OTELExporterOTLPEndpoint, "http://host.docker.internal:4318")
	}
	if !cfg.OTELExporterOTLPInsecure {
		t.Fatalf("expected OTEL exporter insecure to be enabled")
	}
	if cfg.OTELMetricExportIntervalMS != 5000 {
		t.Fatalf("OTEL metric export interval = %d, want %d", cfg.OTELMetricExportIntervalMS, 5000)
	}
}

func restoreEnv(t *testing.T, keys ...string) {
	t.Helper()

	original := make(map[string]*string, len(keys))
	for _, key := range keys {
		value, ok := os.LookupEnv(key)
		if ok {
			copyValue := value
			original[key] = &copyValue
		} else {
			original[key] = nil
		}

		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
	}

	t.Cleanup(func() {
		for _, key := range keys {
			if original[key] == nil {
				_ = os.Unsetenv(key)
				continue
			}
			_ = os.Setenv(key, *original[key])
		}
	})
}

func chdir(t *testing.T, dir string) {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}

	t.Cleanup(func() {
		_ = os.Chdir(wd)
	})
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
