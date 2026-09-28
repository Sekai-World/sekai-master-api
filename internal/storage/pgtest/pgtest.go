// Package pgtest runs PostgreSQL-backed tests against one PostgreSQL 18
// container per test binary, started on first use through testcontainers-go
// on the host Docker API. Every test gets its own empty database, so tests
// stay isolated and can run in parallel.
//
// Without Docker the tests skip with a clear message, unless the CI
// environment variable is set, in which case they fail. Setting
// PGTEST_DATABASE_URL to the URL of an existing server (any database the
// user may create databases from) uses that server instead of a container.
package pgtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Image is the PostgreSQL image the tests run against. Keep it on the major
// version production runs, and on a glibc (Debian) image: its default
// en_US.utf8 collation orders text differently from byte order, as production
// servers do, so tests notice a missing COLLATE "C". Alpine's musl locales
// sort byte-wise and would hide that.
const Image = "postgres:18"

// ServerURLEnv names the variable that points the tests at an existing
// server instead of a container.
const ServerURLEnv = "PGTEST_DATABASE_URL"

var (
	serverOnce sync.Once
	serverURL  string
	serverErr  error
	container  *tcpostgres.PostgresContainer

	databaseSeq atomic.Int64
)

// Main runs a package's tests and then stops the shared container. Call it
// from TestMain in every package that uses NewDatabase.
func Main(m *testing.M) {
	code := m.Run()
	if container != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = container.Terminate(ctx)
		cancel()
	}
	os.Exit(code)
}

// NewDatabase creates an empty database for the calling test, drops it when
// the test ends, and returns its connection URL.
func NewDatabase(t testing.TB) string {
	t.Helper()

	adminURL := server(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	name := fmt.Sprintf("pgtest_%d_%d", os.Getpid(), databaseSeq.Add(1))
	if err := execAdmin(ctx, adminURL, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := execAdmin(ctx, adminURL, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Logf("drop test database %s: %v", name, err)
		}
	})

	databaseURL, err := withDatabase(adminURL, name)
	if err != nil {
		t.Fatalf("build test database URL: %v", err)
	}
	return databaseURL
}

// server returns the admin URL of the shared server, starting the container
// on first use. It skips or fails the test when no server is available.
func server(t testing.TB) string {
	t.Helper()

	serverOnce.Do(func() {
		if configured := strings.TrimSpace(os.Getenv(ServerURLEnv)); configured != "" {
			serverURL = configured
			return
		}
		serverURL, serverErr = startContainer()
	})

	if serverErr != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("PostgreSQL tests need Docker, and CI is set: %v", serverErr)
		}
		t.Skipf("skipping PostgreSQL test: Docker is unavailable (%v); start Docker or set %s to run it", serverErr, ServerURLEnv)
	}
	return serverURL
}

func startContainer() (adminURL string, err error) {
	// testcontainers panics on some Docker discovery failures; report them
	// as an unavailable Docker instead of crashing the test binary.
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("start PostgreSQL container: %v", recovered)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	started, err := tcpostgres.Run(ctx, Image,
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(time.Minute),
		),
	)
	if started != nil {
		container = started
	}
	if err != nil {
		return "", err
	}

	return started.ConnectionString(ctx, "sslmode=disable")
}

func execAdmin(ctx context.Context, adminURL string, statement string) error {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, statement)
	return err
}

func withDatabase(rawURL string, name string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	parsed.Path = "/" + name
	return parsed.String(), nil
}
