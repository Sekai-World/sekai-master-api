package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadUsesDotenvLocalPrecedence(t *testing.T) {
	restoreEnv(
		t,
		"APP_ENV",
		"APP_PORT",
		"MASTER_DATA_RECOVER_INTERRUPTED_SYNC",
		"MASTER_DATA_RESUME_BASE_DIR",
		"MASTER_DATA_SYNC_CONCURRENCY",
		"MASTER_DATA_SYNC_TIMEOUT_SECONDS",
		"MASTER_DATA_SYNC_JOB_TIMEOUT_SECONDS",
		"MASTER_DATA_REGION_FILE_CONCURRENCY",
		"OTEL_ENABLED",
	)

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
	if cfg.MasterDataSyncConcurrency != 1 {
		t.Fatalf("expected development master data sync concurrency to default to 1, got %d", cfg.MasterDataSyncConcurrency)
	}
	if cfg.MasterDataSyncTimeout != 0 || cfg.MasterDataSyncJobTimeout != 1800 {
		t.Fatalf("expected sync timeout defaults region=0 job=1800, got region=%d job=%d", cfg.MasterDataSyncTimeout, cfg.MasterDataSyncJobTimeout)
	}
	if cfg.MasterDataFileConcurrency != 2 {
		t.Fatalf("expected development master data file concurrency to default to 2, got %d", cfg.MasterDataFileConcurrency)
	}
	if cfg.OTELEnabled {
		t.Fatalf("expected OTel to default disabled in development")
	}
	if cfg.MasterDataResumeBaseDir != "tmp/master-data-sync-resume" {
		t.Fatalf("expected MasterDataResumeBaseDir default, got %q", cfg.MasterDataResumeBaseDir)
	}
}

func TestLoadReadsDatabasePoolConfig(t *testing.T) {
	restoreEnv(t, "APP_ENV", "DATABASE_MAX_CONNS", "DATABASE_MIN_CONNS", "DATABASE_CONNECT_TIMEOUT_SECONDS", "DATABASE_MAX_CONN_LIFETIME_SECONDS", "DATABASE_MAX_CONN_IDLE_SECONDS")

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, ".env"), "APP_ENV=production\n"+
		"DATABASE_MAX_CONNS=40\n"+
		"DATABASE_MIN_CONNS=2\n"+
		"DATABASE_CONNECT_TIMEOUT_SECONDS=5\n"+
		"DATABASE_MAX_CONN_LIFETIME_SECONDS=600\n"+
		"DATABASE_MAX_CONN_IDLE_SECONDS=120\n")
	chdir(t, tmpDir)

	cfg := Load()
	if cfg.DatabaseMaxConns != 40 || cfg.DatabaseMinConns != 2 {
		t.Fatalf("unexpected pool size: max=%d min=%d", cfg.DatabaseMaxConns, cfg.DatabaseMinConns)
	}
	if cfg.DatabaseConnectTimeout != 5*time.Second || cfg.DatabaseMaxConnLifetime != 10*time.Minute || cfg.DatabaseMaxConnIdleTime != 2*time.Minute {
		t.Fatalf("unexpected pool timeouts: connect=%s lifetime=%s idle=%s", cfg.DatabaseConnectTimeout, cfg.DatabaseMaxConnLifetime, cfg.DatabaseMaxConnIdleTime)
	}
}

func TestLoadDefaultsDatabasePoolConfig(t *testing.T) {
	restoreEnv(t, "APP_ENV", "DATABASE_MAX_CONNS", "DATABASE_MIN_CONNS", "DATABASE_CONNECT_TIMEOUT_SECONDS", "DATABASE_MAX_CONN_LIFETIME_SECONDS", "DATABASE_MAX_CONN_IDLE_SECONDS")
	chdir(t, t.TempDir())

	cfg := Load()
	if cfg.DatabaseMaxConns != 0 || cfg.DatabaseMinConns != 0 || cfg.DatabaseMaxConnLifetime != 0 || cfg.DatabaseMaxConnIdleTime != 0 {
		t.Fatalf("pool limits should defer to DATABASE_URL and pgx defaults, got %+v", cfg)
	}
	if cfg.DatabaseConnectTimeout != 10*time.Second {
		t.Fatalf("connect timeout = %s, want 10s", cfg.DatabaseConnectTimeout)
	}
}

func TestLoadMasterDataStore(t *testing.T) {
	restoreEnv(t, "APP_ENV", "MASTER_DATA_STORE")
	chdir(t, t.TempDir())

	if cfg := Load(); cfg.MasterDataStore != "" || cfg.ValidateMasterDataStore() != nil {
		t.Fatalf("default store = %q, want unset and valid", cfg.MasterDataStore)
	}

	t.Setenv("MASTER_DATA_STORE", " Postgres ")
	if cfg := Load(); cfg.MasterDataStore != "postgres" || cfg.ValidateMasterDataStore() != nil {
		t.Fatalf("store = %q, want postgres", cfg.MasterDataStore)
	}

	t.Setenv("MASTER_DATA_STORE", "sqlite")
	if err := Load().ValidateMasterDataStore(); err == nil {
		t.Fatal("unknown store accepted")
	}
}

func TestValidateMasterDataStoreAcceptsOnlyPostgres(t *testing.T) {
	for _, store := range []string{"", "postgres"} {
		if err := (Config{MasterDataStore: store}).ValidateMasterDataStore(); err != nil {
			t.Fatalf("store %q rejected: %v", store, err)
		}
	}

	err := (Config{MasterDataStore: "redis"}).ValidateMasterDataStore()
	if err == nil {
		t.Fatal("store \"redis\" accepted, want an error")
	}
	if !strings.Contains(err.Error(), "Redis master-data store was removed") {
		t.Fatalf("redis store error = %q, want it to say the Redis store was removed", err)
	}
}

func TestLoadResponseCacheSettings(t *testing.T) {
	for _, key := range []string{"CACHE_REDIS_ADDR", "CACHE_REDIS_PASSWORD", "CACHE_REDIS_DB", "CACHE_REDIS_TIMEOUT_MS", "CACHE_TTL_SECONDS", "CACHE_MAX_ENTRY_BYTES"} {
		t.Setenv(key, "")
	}
	cfg := Load()
	if cfg.CacheRedisAddr != "" || cfg.CacheRedisTimeout != 50*time.Millisecond || cfg.CacheTTL != 6*time.Hour || cfg.CacheMaxEntryBytes != 1<<20 {
		t.Fatalf("defaults = addr %q timeout %s ttl %s max %d", cfg.CacheRedisAddr, cfg.CacheRedisTimeout, cfg.CacheTTL, cfg.CacheMaxEntryBytes)
	}

	t.Setenv("CACHE_REDIS_ADDR", " redis:6379 ")
	t.Setenv("CACHE_REDIS_DB", "2")
	t.Setenv("CACHE_REDIS_TIMEOUT_MS", "80")
	t.Setenv("CACHE_TTL_SECONDS", "600")
	t.Setenv("CACHE_MAX_ENTRY_BYTES", "4096")
	cfg = Load()
	if cfg.CacheRedisAddr != "redis:6379" || cfg.CacheRedisDB != 2 || cfg.CacheRedisTimeout != 80*time.Millisecond || cfg.CacheTTL != 10*time.Minute || cfg.CacheMaxEntryBytes != 4096 {
		t.Fatalf("overrides = addr %q db %d timeout %s ttl %s max %d", cfg.CacheRedisAddr, cfg.CacheRedisDB, cfg.CacheRedisTimeout, cfg.CacheTTL, cfg.CacheMaxEntryBytes)
	}
}

func TestValidateDatabaseDriverAcceptsOnlyPostgres(t *testing.T) {
	for _, driver := range []string{"", "pgx", "PGX", " postgres ", "postgresql"} {
		if err := (Config{DatabaseDriverName: driver}).ValidateDatabaseDriver(); err != nil {
			t.Fatalf("driver %q rejected: %v", driver, err)
		}
	}
	for _, driver := range []string{"sqlite", "mysql"} {
		if err := (Config{DatabaseDriverName: driver}).ValidateDatabaseDriver(); err == nil {
			t.Fatalf("driver %q accepted, want an error", driver)
		}
	}
}

func TestLoadMasterDataTimeoutOverrides(t *testing.T) {
	restoreEnv(t, "APP_ENV", "MASTER_DATA_SYNC_TIMEOUT_SECONDS", "MASTER_DATA_SYNC_JOB_TIMEOUT_SECONDS")
	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, ".env"), "APP_ENV=development\nMASTER_DATA_SYNC_TIMEOUT_SECONDS=45\nMASTER_DATA_SYNC_JOB_TIMEOUT_SECONDS=2400\n")
	chdir(t, tmpDir)

	cfg := Load()
	if cfg.MasterDataSyncTimeout != 45 || cfg.MasterDataSyncJobTimeout != 2400 {
		t.Fatalf("expected timeout overrides, got region=%d job=%d", cfg.MasterDataSyncTimeout, cfg.MasterDataSyncJobTimeout)
	}
}

func TestLoadDefaultsOTELDisabled(t *testing.T) {
	restoreEnv(t, "APP_ENV", "OTEL_ENABLED")

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, ".env"), "APP_ENV=production\n")
	chdir(t, tmpDir)

	cfg := Load()
	if cfg.OTELEnabled {
		t.Fatalf("expected OTel to default disabled unless explicitly enabled")
	}
}

func TestLoadKeepsExplicitMasterDataMemoryControlOverrides(t *testing.T) {
	restoreEnv(
		t,
		"APP_ENV",
		"MASTER_DATA_SYNC_CONCURRENCY",
		"MASTER_DATA_REGION_FILE_CONCURRENCY",
	)

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, ".env"), ""+
		"APP_ENV=development\n"+
		"MASTER_DATA_SYNC_CONCURRENCY=3\n"+
		"MASTER_DATA_REGION_FILE_CONCURRENCY=8\n")

	chdir(t, tmpDir)

	cfg := Load()
	if cfg.MasterDataSyncConcurrency != 3 {
		t.Fatalf("expected explicit sync concurrency override 3, got %d", cfg.MasterDataSyncConcurrency)
	}
	if cfg.MasterDataFileConcurrency != 8 {
		t.Fatalf("expected explicit file concurrency override 8, got %d", cfg.MasterDataFileConcurrency)
	}
}

func TestLoadKeepsProductionMasterDataDefaults(t *testing.T) {
	restoreEnv(
		t,
		"APP_ENV",
		"MASTER_DATA_SYNC_CONCURRENCY",
		"MASTER_DATA_REGION_FILE_CONCURRENCY",
	)

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, ".env"), "APP_ENV=production\n")
	chdir(t, tmpDir)

	cfg := Load()
	if cfg.MasterDataSyncConcurrency != 3 {
		t.Fatalf("expected production sync concurrency default 3, got %d", cfg.MasterDataSyncConcurrency)
	}
	if cfg.MasterDataFileConcurrency != 8 {
		t.Fatalf("expected production file concurrency default 8, got %d", cfg.MasterDataFileConcurrency)
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

func TestLoadReadsMasterDataResumeBaseDir(t *testing.T) {
	restoreEnv(t, "APP_ENV", "MASTER_DATA_RESUME_BASE_DIR")

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, ".env"), "APP_ENV=development\nMASTER_DATA_RESUME_BASE_DIR=/custom/resume/path\n")

	chdir(t, tmpDir)

	cfg := Load()
	if cfg.MasterDataResumeBaseDir != "/custom/resume/path" {
		t.Fatalf("expected resume base dir override, got %q", cfg.MasterDataResumeBaseDir)
	}
}

func TestLoadReadsOTELConfig(t *testing.T) {
	restoreEnv(
		t,
		"APP_ENV",
		"OTEL_ENABLED",
		"OTEL_TRACING_ENABLED",
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
	if !cfg.OTELTracingEnabled {
		t.Fatalf("expected tracing to inherit enabled OTel state")
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

func TestLoadAllowsTracingDisabledWhileOTELRemainsEnabled(t *testing.T) {
	restoreEnv(t, "APP_ENV", "OTEL_ENABLED", "OTEL_TRACING_ENABLED")

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, ".env"), "APP_ENV=development\nOTEL_ENABLED=true\nOTEL_TRACING_ENABLED=false\n")
	chdir(t, tmpDir)

	cfg := Load()
	if !cfg.OTELEnabled {
		t.Fatalf("expected OTel metrics to remain enabled")
	}
	if cfg.OTELTracingEnabled {
		t.Fatalf("expected tracing to be disabled explicitly")
	}
}

func TestLoadKeepsExplicitOTELDisabled(t *testing.T) {
	restoreEnv(t, "APP_ENV", "OTEL_ENABLED")

	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, ".env"), "APP_ENV=development\nOTEL_ENABLED=false\n")
	chdir(t, tmpDir)

	cfg := Load()
	if cfg.OTELEnabled {
		t.Fatalf("expected explicit OTEL_ENABLED=false to disable OTel")
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

func TestOwnsSyncLifecycleAndNeedsAdminSurface(t *testing.T) {
	standalone := Config{Role: AppRoleStandalone}
	control := Config{Role: AppRoleControl}
	serve := Config{Role: AppRoleServe}

	if !standalone.OwnsSyncLifecycle() {
		t.Fatalf("standalone should own sync lifecycle")
	}
	if !control.OwnsSyncLifecycle() {
		t.Fatalf("control should own sync lifecycle")
	}
	if serve.OwnsSyncLifecycle() {
		t.Fatalf("serve must not own sync lifecycle")
	}

	if !standalone.NeedsAdminSurface() {
		t.Fatalf("standalone should mount admin surface")
	}
	if !control.NeedsAdminSurface() {
		t.Fatalf("control should mount admin surface")
	}
	if serve.NeedsAdminSurface() {
		t.Fatalf("serve must not mount admin surface")
	}
}
