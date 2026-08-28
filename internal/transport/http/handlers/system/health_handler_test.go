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
	redisDown         bool            // simulate Redis unreachable (no error)
	redisErr          error           // error when RedisReady called
	versionReady      map[string]bool // region -> version metadata available
	versionErr        error           // error when RegionVersionReady called
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

func (f *fakeReadinessChecker) RedisReady(_ context.Context) (bool, error) {
	if f.redisErr != nil {
		return false, f.redisErr
	}
	if f.redisDown {
		return false, nil
	}
	return true, nil
}

func (f *fakeReadinessChecker) RegionVersionReady(_ context.Context, region string) (bool, error) {
	if f.versionErr != nil {
		// Mirror the production classification so tests can simulate both a
		// corrupt/malformed payload (data problem -> not ready, no error) and a
		// genuine Redis transport error (propagated so the probe reports redis).
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		if errors.As(f.versionErr, &syntaxErr) || errors.As(f.versionErr, &typeErr) {
			return false, nil
		}
		return false, f.versionErr
	}
	if val, ok := f.versionReady[region]; ok {
		return val, nil
	}
	return true, nil
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

func TestReadyReturns503WhenRedisDown(t *testing.T) {
	fake := &fakeReadinessChecker{
		configuredRegions: []string{"global", "jp"},
		entityRecords:     map[string]bool{"global": true, "jp": true},
		redisDown:         true,
	}
	r := runReadyRequest(t, fake, config.AppRoleServe, nil)

	if r.code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when redis down, got %d body=%s", r.code, r.body)
	}
	if r.body["status"] != "not_ready" {
		t.Fatalf("expected status not_ready, got %v", r.body["status"])
	}
	if r.body["reason"] != "redis" {
		t.Fatalf("expected reason redis, got %v", r.body["reason"])
	}
	unready, ok := r.body["unready_regions"].([]any)
	if !ok {
		t.Fatalf("expected unready_regions array, got %v", r.body["unready_regions"])
	}
	if len(unready) != 2 {
		t.Fatalf("expected all configured regions unready when redis down, got %d", len(unready))
	}
}

func TestReadyReturns503WhenRedisErrors(t *testing.T) {
	fake := &fakeReadinessChecker{
		configuredRegions: []string{"jp"},
		entityRecords:     map[string]bool{"jp": true},
		redisErr:          errors.New("redis connection refused"),
	}
	r := runReadyRequest(t, fake, config.AppRoleServe, nil)

	if r.code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on redis error, got %d body=%s", r.code, r.body)
	}
	if r.body["reason"] != "redis" {
		t.Fatalf("expected reason redis, got %v", r.body["reason"])
	}
}

func TestReadyReturns503WhenRegionVersionMissing(t *testing.T) {
	fake := &fakeReadinessChecker{
		configuredRegions: []string{"global", "jp"},
		entityRecords:     map[string]bool{"global": true, "jp": true},
		versionReady:      map[string]bool{"global": true, "jp": false},
	}
	r := runReadyRequest(t, fake, config.AppRoleServe, nil)

	if r.code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when region version missing, got %d body=%s", r.code, r.body)
	}
	if r.body["reason"] != "master_data" {
		t.Fatalf("expected reason master_data, got %v", r.body["reason"])
	}
	unready, ok := r.body["unready_regions"].([]any)
	if !ok {
		t.Fatalf("expected unready_regions array, got %v", r.body["unready_regions"])
	}
	if len(unready) != 1 || unready[0] != "jp" {
		t.Fatalf("expected unready_regions=[jp], got %v", unready)
	}
	readyRegions, ok := r.body["ready_regions"].([]any)
	if !ok || len(readyRegions) != 1 || readyRegions[0] != "global" {
		t.Fatalf("expected ready_regions=[global], got %v", r.body["ready_regions"])
	}
}

func TestReadyReturns503WhenRedisErrorLoadingVersion(t *testing.T) {
	// A version load error is a Redis read failure, so the readiness probe
	// reports the redis dependency (not master_data).
	fake := &fakeReadinessChecker{
		configuredRegions: []string{"jp"},
		entityRecords:     map[string]bool{"jp": true},
		versionErr:        errors.New("redis timeout loading version"),
	}
	r := runReadyRequest(t, fake, config.AppRoleServe, nil)

	if r.code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on redis version error, got %d body=%s", r.code, r.body)
	}
	if r.body["reason"] != "redis" {
		t.Fatalf("expected reason redis, got %v", r.body["reason"])
	}
}

func TestReadyReturnsOKWhenRecordsAndVersionPresent(t *testing.T) {
	fake := &fakeReadinessChecker{
		configuredRegions: []string{"global", "jp"},
		entityRecords:     map[string]bool{"global": true, "jp": true},
		versionReady:      map[string]bool{"global": true, "jp": true},
		syncStatus:        map[string]bool{"global": true, "jp": true},
	}
	r := runReadyRequest(t, fake, config.AppRoleServe, nil)

	if r.code != http.StatusOK {
		t.Fatalf("expected 200 when records and version present, got %d body=%s", r.code, r.body)
	}
	if r.body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", r.body["status"])
	}
	readyRegions, ok := r.body["ready_regions"].([]any)
	if !ok || len(readyRegions) != 2 {
		t.Fatalf("expected 2 ready regions, got %v", r.body["ready_regions"])
	}
}

func TestReadyReturns503WhenVersionCorruptReportedAsMasterData(t *testing.T) {
	// A corrupt/malformed version payload from Redis is a data problem, so the
	// probe must report master_data (not redis) and enumerate the affected region.
	corrupt := json.Unmarshal([]byte("{not-valid-json"), new(any))
	fake := &fakeReadinessChecker{
		configuredRegions: []string{"jp"},
		entityRecords:     map[string]bool{"jp": true},
		versionErr:        corrupt,
	}
	r := runReadyRequest(t, fake, config.AppRoleServe, nil)

	if r.code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on corrupt version payload, got %d body=%s", r.code, r.body)
	}
	if r.body["reason"] != "master_data" {
		t.Fatalf("expected reason master_data for corrupt payload, got %v", r.body["reason"])
	}
	unready, ok := r.body["unready_regions"].([]any)
	if !ok || len(unready) != 1 || unready[0] != "jp" {
		t.Fatalf("expected unready_regions=[jp], got %v", r.body["unready_regions"])
	}
}

func TestReadyReturns503MasterDataIncludesEmptyReadyRegions(t *testing.T) {
	// When master_data readiness fails and no region is ready, the response must
	// still include ready_regions (as an empty array) for compatibility.
	fake := &fakeReadinessChecker{
		configuredRegions: []string{"jp"},
		entityRecords:     map[string]bool{"jp": false},
	}
	r := runReadyRequest(t, fake, config.AppRoleServe, nil)

	if r.code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when region has no records, got %d body=%s", r.code, r.body)
	}
	if r.body["reason"] != "master_data" {
		t.Fatalf("expected reason master_data, got %v", r.body["reason"])
	}
	readyRegions, ok := r.body["ready_regions"].([]any)
	if !ok {
		t.Fatalf("expected ready_regions key present (even if empty), got %v", r.body["ready_regions"])
	}
	if len(readyRegions) != 0 {
		t.Fatalf("expected empty ready_regions, got %v", readyRegions)
	}
	unready, ok := r.body["unready_regions"].([]any)
	if !ok || len(unready) != 1 || unready[0] != "jp" {
		t.Fatalf("expected unready_regions=[jp], got %v", r.body["unready_regions"])
	}
}
