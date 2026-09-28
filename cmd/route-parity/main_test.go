package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testSwagger = `{"paths": {
	"/cards/{region}/list": {"get": {}},
	"/cards/{region}/{id}": {"get": {}},
	"/cards/{region}/batch": {"get": {}},
	"/cards/regions/{id}/availability": {"get": {}},
	"/unitProfiles/{region}/{unit}": {"get": {}},
	"/versions": {"get": {}},
	"/health": {"get": {}},
	"/admin/master-data/status": {"get": {}},
	"/cards/{region}/refresh": {"post": {}}
}}`

// fakeDeployment answers card lists with ids 1, 2 and 3 and echoes every
// other path; broken, when set, changes one path's body.
func fakeDeployment(t *testing.T, broken string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.RequestURI(), apiPrefix)
		switch {
		case strings.HasPrefix(path, "/cards/jp/list"):
			_, _ = w.Write([]byte(`{"items":[{"id":1},{"id":2},{"id":3}],"total":3}`))
		case strings.HasPrefix(path, "/unitProfiles/jp/list"):
			_, _ = w.Write([]byte(`{"items":[{"unit":"idol"}],"total":1}`))
		case path == broken:
			_, _ = w.Write([]byte("changed " + path))
		default:
			_, _ = w.Write([]byte("echo " + path))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func writeSwagger(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "swagger.json")
	if err := os.WriteFile(path, []byte(testSwagger), 0o600); err != nil {
		t.Fatalf("write swagger: %v", err)
	}
	return path
}

func testOptions(t *testing.T, reference, candidate string) options {
	return options{
		reference:  reference,
		candidate:  candidate,
		regions:    []string{"jp"},
		swagger:    writeSwagger(t),
		skip:       []string{"/health"},
		warmRounds: 1,
		timeout:    5 * time.Second,
		failAfter:  3 * time.Second,
		maxDiffs:   5,
	}
}

func TestRunExpandsPublicGetRoutes(t *testing.T) {
	server := fakeDeployment(t, "")
	opts := testOptions(t, server.URL, server.URL)

	report, err := run(opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var paths []string
	for _, entry := range report.results {
		paths = append(paths, entry.path)
	}
	want := []string{
		"/cards/regions/1/availability",
		"/cards/regions/2/availability",
		"/cards/regions/3/availability",
		"/cards/regions/999999999/availability",
		"/cards/jp/batch?ids=1,2,3,999999999",
		"/cards/jp/list",
		"/cards/jp/list?page=2&page_size=7",
		"/cards/jp/list?page_size=100&sort_order=desc",
		"/cards/jp/1",
		"/cards/jp/2",
		"/cards/jp/3",
		"/cards/jp/999999999",
		"/unitProfiles/jp/idol",
		"/versions",
	}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths =\n%s\nwant\n%s", strings.Join(paths, "\n"), strings.Join(want, "\n"))
	}
	if report.failed(opts.failAfter) {
		t.Fatal("identical deployments reported as failed")
	}
}

func TestRunReportsDifferingResponses(t *testing.T) {
	reference := fakeDeployment(t, "")
	candidate := fakeDeployment(t, "/cards/jp/2")
	opts := testOptions(t, reference.URL, candidate.URL)

	report, err := run(opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !report.failed(opts.failAfter) {
		t.Fatal("differing deployments not reported as failed")
	}
	var out bytes.Buffer
	report.print(&out, opts)
	if !strings.Contains(out.String(), "13 identical, 1 different") || !strings.Contains(out.String(), "DIFF /cards/jp/2") {
		t.Fatalf("report =\n%s", out.String())
	}
}

func TestFirstDifference(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		want        int
	}{
		{"abc", "abc", -1},
		{"abc", "abd", 2},
		{"abc", "abcd", 3},
		{"", "a", 0},
	} {
		if got := firstDifference([]byte(tc.left), []byte(tc.right)); got != tc.want {
			t.Errorf("firstDifference(%q, %q) = %d, want %d", tc.left, tc.right, got, tc.want)
		}
	}
}
