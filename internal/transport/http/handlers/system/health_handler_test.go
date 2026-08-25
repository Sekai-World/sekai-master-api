package system

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/config"
	"sekai-master-api/internal/startup"
)

// fakeReadinessChecker implements MasterDataReadinessChecker for tests.
type fakeReadinessChecker struct {
	configuredRegions []string
	entityRecords     map[string]bool // region -> has records
	entityRecordsErr  error           // error when HasEntityRecords called
	syncStatus        map[string]bool // region -> has successful sync
	syncStatusErr     error           // error when HasSuccessfulSync called
}

func (f *fakeReadinessChecker) ConfiguredRegions() []string {
	return f.configuredRegions
}

func (f *fakeReadinessChecker) HasEntityRecords(_ context.Context, region string, _ string) (bool, error) {
	if f.entityRecordsErr != nil {
		return false, f.entityRecordsErr
	}
	if val, ok := f.entityRecords[region]; ok {
		return val, nil
	}
	return false, nil
}

func (f *fakeReadinessChecker) HasSuccessfulSync(_ context.Context, region string) (bool, error) {
	if f.syncStatusErr != nil {
		return false, f.syncStatusErr
	}
	if val, ok := f.syncStatus[region]; ok {
		return val, nil
	}
	return false, nil
}

// readyRequestResult holds the output of runReadyRequest for convenient assertions.
type readyRequestResult struct {
	code int
	body map[string]any
}

// runReadyRequest wires a HealthHandler, fires GET /readyz, and returns the
// status code plus the parsed JSON body.
func runReadyRequest(t *testing.T, checker MasterDataReadinessChecker, role config.AppRole, ss *startup.State) readyRequestResult {
	t.Helper()
	gin.SetMode(gin.TestMode)
	handler := &HealthHandler{role: role, masterDataSync: checker, startupState: ss}
	router := gin.New()
	router.GET("/readyz", handler.Ready)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return readyRequestResult{code: resp.Code, body: body}
}

func TestReadyReturnsOKWhenAllRegionsHaveRecords(t *testing.T) {
	fake := &fakeReadinessChecker{
		configuredRegions: []string{"global", "jp"},
		entityRecords:     map[string]bool{"global": true, "jp": true},
		syncStatus:        map[string]bool{"global": true, "jp": true},
	}
	r := runReadyRequest(t, fake, config.AppRoleServe, nil)

	if r.code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", r.code, r.body)
	}
	if r.body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", r.body["status"])
	}
	readyRegions, ok := r.body["ready_regions"].([]any)
	if !ok {
		t.Fatalf("expected ready_regions array, got %v", r.body["ready_regions"])
	}
	if len(readyRegions) != 2 {
		t.Fatalf("expected 2 ready regions, got %d", len(readyRegions))
	}
}

func TestReadyReturnsOKWhenRecordsExistButSyncFailed(t *testing.T) {
	// Region has persisted cards records but no successful sync.
	// The new behavior says this is still "ok" because serve reads from records.
	fake := &fakeReadinessChecker{
		configuredRegions: []string{"jp"},
		entityRecords:     map[string]bool{"jp": true},
		syncStatus:        map[string]bool{"jp": false},
	}
	r := runReadyRequest(t, fake, config.AppRoleServe, nil)

	if r.code != http.StatusOK {
		t.Fatalf("expected 200 for records-without-sync, got %d body=%s", r.code, r.body)
	}
	if r.body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", r.body["status"])
	}
	pendingSync, ok := r.body["regions_pending_sync"].([]any)
	if !ok {
		t.Fatalf("expected regions_pending_sync array, got %v", r.body["regions_pending_sync"])
	}
	if len(pendingSync) != 1 || pendingSync[0] != "jp" {
		t.Fatalf("expected regions_pending_sync=[jp], got %v", pendingSync)
	}
}

func TestReadyReturns503WhenRegionHasNoRecords(t *testing.T) {
	fake := &fakeReadinessChecker{
		configuredRegions: []string{"jp"},
		entityRecords:     map[string]bool{"jp": false},
	}
	r := runReadyRequest(t, fake, config.AppRoleServe, nil)

	if r.code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", r.code, r.body)
	}
	if r.body["status"] != "not_ready" {
		t.Fatalf("expected status not_ready, got %v", r.body["status"])
	}
	if r.body["reason"] != "master_data" {
		t.Fatalf("expected reason master_data, got %v", r.body["reason"])
	}
}

func TestReadyDoesNot503WhenHasSuccessfulSyncErrors(t *testing.T) {
	// HasSuccessfulSync returns an error, but records exist → still 200.
	fake := &fakeReadinessChecker{
		configuredRegions: []string{"jp"},
		entityRecords:     map[string]bool{"jp": true},
		syncStatusErr:     errors.New("database unavailable"),
	}
	r := runReadyRequest(t, fake, config.AppRoleServe, nil)

	if r.code != http.StatusOK {
		t.Fatalf("expected 200 when sync status errors but records exist, got %d body=%s", r.code, r.body)
	}
	if r.body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", r.body["status"])
	}
	if _, ok := r.body["regions_pending_sync"]; ok {
		t.Fatalf("did not expect regions_pending_sync when sync status errors")
	}
}

func TestReadyReturns503WhenHasEntityRecordsErrors(t *testing.T) {
	fake := &fakeReadinessChecker{
		configuredRegions: []string{"jp"},
		entityRecordsErr:  errors.New("redis connection refused"),
	}
	r := runReadyRequest(t, fake, config.AppRoleServe, nil)

	if r.code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on HasEntityRecords error, got %d body=%s", r.code, r.body)
	}
}

func TestReadyReturns200ForNonServeRole(t *testing.T) {
	r := runReadyRequest(t, nil, config.AppRoleControl, nil)

	if r.code != http.StatusOK {
		t.Fatalf("expected 200 for control role, got %d", r.code)
	}
}

func TestReadyReturns503DuringStartup(t *testing.T) {
	ss := startup.NewState()
	r := runReadyRequest(t, nil, config.AppRoleServe, ss)

	if r.code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 during startup, got %d", r.code)
	}
}

func TestReadyPartialRecordsReturns503(t *testing.T) {
	// One region has records, another doesn't → 503 with partial ready_regions
	fake := &fakeReadinessChecker{
		configuredRegions: []string{"global", "jp"},
		entityRecords:     map[string]bool{"global": true, "jp": false},
		syncStatus:        map[string]bool{"global": true},
	}
	r := runReadyRequest(t, fake, config.AppRoleServe, nil)

	if r.code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for partial records, got %d body=%s", r.code, r.body)
	}
	if r.body["status"] != "not_ready" {
		t.Fatalf("expected status not_ready, got %v", r.body["status"])
	}
	readyRegions, ok := r.body["ready_regions"].([]any)
	if !ok {
		t.Fatalf("expected ready_regions array, got %v", r.body["ready_regions"])
	}
	if len(readyRegions) != 1 || readyRegions[0] != "global" {
		t.Fatalf("expected ready_regions=[global], got %v", readyRegions)
	}
}

func TestReadyNoRegionsPendingSyncWhenAllSyncSucceeds(t *testing.T) {
	fake := &fakeReadinessChecker{
		configuredRegions: []string{"jp"},
		entityRecords:     map[string]bool{"jp": true},
		syncStatus:        map[string]bool{"jp": true},
	}
	r := runReadyRequest(t, fake, config.AppRoleServe, nil)

	if r.code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", r.code, r.body)
	}
	if _, ok := r.body["regions_pending_sync"]; ok {
		t.Fatalf("did not expect regions_pending_sync when all syncs succeed, body=%s", r.body)
	}
}
