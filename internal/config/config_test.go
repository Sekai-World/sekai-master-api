package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUsesDotenvLocalPrecedence(t *testing.T) {
	restoreEnv(t, "APP_ENV", "APP_PORT")

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
