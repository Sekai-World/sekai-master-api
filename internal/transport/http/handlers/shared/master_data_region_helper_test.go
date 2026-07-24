package shared

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/usecase"
)

func TestEnsureRegionReadyForEntityRecords_returns_true_without_writing_response(t *testing.T) {
	// Given
	masterDataSync := usecase.NewMasterDataSyncUsecase(
		nil,
		nil,
		&fakeMasterDataRegionHelperCache{
			hasRecords: map[string]map[string]bool{
				"jp": {"cards": true},
			},
		},
		&fakeMasterDataRegionHelperStatusStore{
			statuses: []masterdata.SyncStatus{{Region: "jp", Status: "success"}},
		},
		nil,
		1,
	)
	c, recorder := newMasterDataRegionHelperContext()

	// When
	ready := EnsureRegionReadyForEntityRecords(c, masterDataSync, "jp", "cards")

	// Then
	if !ready {
		t.Fatal("expected region to be ready")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected no response to be written, got status %d", recorder.Code)
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("expected no response body, got %s", recorder.Body.String())
	}
}

func TestEnsureRegionReadyForEntityRecords_maps_unavailable_region(t *testing.T) {
	// Given
	masterDataSync := usecase.NewMasterDataSyncUsecase(
		nil,
		nil,
		&fakeMasterDataRegionHelperCache{
			hasRecords:     map[string]map[string]bool{"jp": {"cards": false}},
			hasRegionIndex: map[string]bool{"jp": false},
		},
		&fakeMasterDataRegionHelperStatusStore{
			statuses: []masterdata.SyncStatus{{Region: "jp", Status: "success"}},
		},
		nil,
		1,
	)
	c, recorder := newMasterDataRegionHelperContext()

	// When
	ready := EnsureRegionReadyForEntityRecords(c, masterDataSync, "jp", "cards")

	// Then
	if ready {
		t.Fatal("expected region to be unavailable")
	}
	assertMasterDataRegionHelperError(
		t,
		recorder,
		http.StatusServiceUnavailable,
		"REGION_DATA_NOT_READY",
		"region data is updating or unavailable, please try again later",
	)
}

func TestEnsureRegionReadyForEntityRecords_maps_status_error(t *testing.T) {
	// Given
	masterDataSync := usecase.NewMasterDataSyncUsecase(
		nil,
		nil,
		&fakeMasterDataRegionHelperCache{
			hasRecords: map[string]map[string]bool{"jp": {"cards": true}},
		},
		&fakeMasterDataRegionHelperStatusStore{
			listErr: errors.New("list statuses failed"),
		},
		nil,
		1,
	)
	c, recorder := newMasterDataRegionHelperContext()

	// When
	ready := EnsureRegionReadyForEntityRecords(c, masterDataSync, "jp", "cards")

	// Then
	if ready {
		t.Fatal("expected readiness check to fail")
	}
	assertMasterDataRegionHelperError(
		t,
		recorder,
		http.StatusInternalServerError,
		"MASTER_DATA_STATUS_ERROR",
		"failed to check master data sync status",
	)
}

func newMasterDataRegionHelperContext() (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c, recorder
}

func assertMasterDataRegionHelperError(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	expectedStatus int,
	expectedCode string,
	expectedMessage string,
) {
	t.Helper()

	if recorder.Code != expectedStatus {
		t.Fatalf("expected status %d, got %d", expectedStatus, recorder.Code)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.Error.Code != expectedCode {
		t.Fatalf("expected error code %q, got %q", expectedCode, body.Error.Code)
	}
	if body.Error.Message != expectedMessage {
		t.Fatalf("expected error message %q, got %q", expectedMessage, body.Error.Message)
	}
}

func TestRegionHasEntityRecordsOrReady_returns_true_when_entity_records_exist(t *testing.T) {
	t.Parallel()

	// Given
	ctx := context.Background()
	masterDataSync := usecase.NewMasterDataSyncUsecase(
		nil,
		nil,
		&fakeMasterDataRegionHelperCache{
			hasRecords: map[string]map[string]bool{
				"jp": {"cards": true},
			},
		},
		&fakeMasterDataRegionHelperStatusStore{
			statuses: []masterdata.SyncStatus{{Region: "jp", Status: "success"}},
		},
		nil,
		1,
	)

	// When
	ready, err := RegionHasEntityRecordsOrReady(ctx, masterDataSync, "jp", "cards")

	// Then
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ready {
		t.Fatalf("expected region to be ready when entity records exist")
	}
}

func TestRegionHasEntityRecordsOrReady_rejects_records_without_successful_sync(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status string
	}{
		{name: "missing status", status: ""},
		{name: "pending status", status: "pending"},
		{name: "running status", status: "running"},
		{name: "failed status", status: "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			statuses := []masterdata.SyncStatus(nil)
			if tt.status != "" {
				statuses = []masterdata.SyncStatus{{Region: "jp", Status: tt.status}}
			}
			masterDataSync := usecase.NewMasterDataSyncUsecase(
				nil,
				nil,
				&fakeMasterDataRegionHelperCache{
					hasRecords: map[string]map[string]bool{"jp": {"cards": true}},
				},
				&fakeMasterDataRegionHelperStatusStore{statuses: statuses},
				nil,
				1,
			)

			ready, err := RegionHasEntityRecordsOrReady(context.Background(), masterDataSync, "jp", "cards")

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if ready {
				t.Fatalf("expected records with %q status to remain unavailable", tt.status)
			}
		})
	}
}

func TestRegionHasEntityRecordsOrReady_returns_error_when_sync_status_check_fails(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("list statuses failed")
	masterDataSync := usecase.NewMasterDataSyncUsecase(
		nil,
		nil,
		&fakeMasterDataRegionHelperCache{
			hasRecords: map[string]map[string]bool{"jp": {"cards": true}},
		},
		&fakeMasterDataRegionHelperStatusStore{listErr: expectedErr},
		nil,
		1,
	)

	ready, err := RegionHasEntityRecordsOrReady(context.Background(), masterDataSync, "jp", "cards")

	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
	if ready {
		t.Fatalf("expected region to be unavailable when status lookup fails")
	}
}

func TestRegionHasEntityRecordsOrReady_returns_true_when_strict_ready_regions_include_region(t *testing.T) {
	t.Parallel()

	// Given
	ctx := context.Background()
	masterDataSync := usecase.NewMasterDataSyncUsecase(
		nil,
		nil,
		&fakeMasterDataRegionHelperCache{
			hasRecords: map[string]map[string]bool{
				"jp": {"cards": false},
			},
			hasRegionIndex: map[string]bool{"jp": true},
		},
		&fakeMasterDataRegionHelperStatusStore{
			statuses: []masterdata.SyncStatus{{Region: "jp", Status: "success"}},
		},
		nil,
		1,
	)

	// When
	ready, err := RegionHasEntityRecordsOrReady(ctx, masterDataSync, "jp", "cards")

	// Then
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ready {
		t.Fatalf("expected region to be ready when strict ready regions include it")
	}
}

func TestRegionHasEntityRecordsOrReady_returns_false_when_neither_entity_records_nor_strict_ready_region_exist(t *testing.T) {
	t.Parallel()

	// Given
	ctx := context.Background()
	masterDataSync := usecase.NewMasterDataSyncUsecase(
		nil,
		nil,
		&fakeMasterDataRegionHelperCache{
			hasRecords: map[string]map[string]bool{
				"jp": {"cards": false},
			},
			hasRegionIndex: map[string]bool{"jp": false},
		},
		&fakeMasterDataRegionHelperStatusStore{
			statuses: []masterdata.SyncStatus{{Region: "jp", Status: "success"}},
		},
		nil,
		1,
	)

	// When
	ready, err := RegionHasEntityRecordsOrReady(ctx, masterDataSync, "jp", "cards")

	// Then
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if ready {
		t.Fatalf("expected region to be unavailable when entity records and strict readiness both fail")
	}
}

func TestRegionHasEntityRecordsOrReady_returns_error_when_entity_record_check_fails(t *testing.T) {
	t.Parallel()

	// Given
	ctx := context.Background()
	expectedErr := errors.New("has entity records failed")
	masterDataSync := usecase.NewMasterDataSyncUsecase(
		nil,
		nil,
		&fakeMasterDataRegionHelperCache{hasRecordsErr: expectedErr},
		&fakeMasterDataRegionHelperStatusStore{},
		nil,
		1,
	)

	// When
	ready, err := RegionHasEntityRecordsOrReady(ctx, masterDataSync, "jp", "cards")

	// Then
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
	if ready {
		t.Fatalf("expected region to be unavailable when entity record check errors")
	}
}

func TestAvailableRegionsByID_uses_successful_status_and_direct_records_without_runtime_index(t *testing.T) {
	t.Parallel()

	// Given
	masterDataSync := usecase.NewMasterDataSyncUsecase(
		nil,
		nil,
		&fakeMasterDataRegionHelperCache{
			records: map[string]map[string]map[string]map[string]any{
				"jp": {"cards": {"42": {"id": "42"}}},
				"en": {"cards": {"42": {"id": "42"}}},
			},
		},
		&fakeMasterDataRegionHelperStatusStore{
			statuses: []masterdata.SyncStatus{
				{Region: "jp", Status: "success"},
				{Region: "en", Status: "failed"},
			},
		},
		nil,
		1,
	)

	// When
	regions, err := AvailableRegionsByID(context.Background(), masterDataSync, "cards", "42")

	// Then
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(regions) != 1 || regions[0] != "jp" {
		t.Fatalf("expected only the successful region with a direct record, got %v", regions)
	}
}

func TestRegionHasEntityRecordsOrReady_returns_false_for_empty_region_or_entity(t *testing.T) {
	t.Parallel()

	// Given
	ctx := context.Background()
	masterDataSync := usecase.NewMasterDataSyncUsecase(
		nil,
		nil,
		&fakeMasterDataRegionHelperCache{},
		&fakeMasterDataRegionHelperStatusStore{
			statuses: []masterdata.SyncStatus{{Region: "jp", Status: "success"}},
		},
		nil,
		1,
	)

	tests := []struct {
		name   string
		region string
		entity string
	}{
		{name: "empty region", region: "", entity: "cards"},
		{name: "empty entity", region: "jp", entity: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// When
			ready, err := RegionHasEntityRecordsOrReady(ctx, masterDataSync, tt.region, tt.entity)

			// Then
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if ready {
				t.Fatalf("expected false for region=%q entity=%q", tt.region, tt.entity)
			}
		})
	}
}

type fakeMasterDataRegionHelperCache struct {
	hasRecords     map[string]map[string]bool
	hasRecordsErr  error
	hasRegionIndex map[string]bool
	records        map[string]map[string]map[string]map[string]any
}

func (cache *fakeMasterDataRegionHelperCache) StoreRegion(_ context.Context, _ string, _ map[string]any) error {
	return nil
}

func (cache *fakeMasterDataRegionHelperCache) GetByID(_ context.Context, region string, entity string, id string) (map[string]any, bool, error) {
	if cache.records == nil || cache.records[region] == nil || cache.records[region][entity] == nil {
		return nil, false, nil
	}
	record, found := cache.records[region][entity][id]
	return record, found, nil
}

func (cache *fakeMasterDataRegionHelperCache) ListAll(_ context.Context, _, _ string) ([]map[string]any, error) {
	return nil, nil
}

func (cache *fakeMasterDataRegionHelperCache) ListByPage(_ context.Context, _, _ string, _, _ int) ([]map[string]any, int, error) {
	return nil, 0, nil
}

func (cache *fakeMasterDataRegionHelperCache) Search(_ context.Context, _, _, _ string, _ []string, _ int) ([]masterdata.SearchMatch, error) {
	return nil, nil
}

func (cache *fakeMasterDataRegionHelperCache) HasEntityRecords(_ context.Context, region string, entity string) (bool, error) {
	if cache.hasRecordsErr != nil {
		return false, cache.hasRecordsErr
	}
	if cache.hasRecords == nil {
		return false, nil
	}
	return cache.hasRecords[region][entity], nil
}

func (cache *fakeMasterDataRegionHelperCache) HasRegionIndex(region string) bool {
	if cache.hasRegionIndex == nil {
		return false
	}
	return cache.hasRegionIndex[region]
}

type fakeMasterDataRegionHelperStatusStore struct {
	statuses []masterdata.SyncStatus
	listErr  error
}

func (store *fakeMasterDataRegionHelperStatusStore) Save(_ context.Context, _ masterdata.SyncStatus) error {
	return nil
}

func (store *fakeMasterDataRegionHelperStatusStore) List(_ context.Context) ([]masterdata.SyncStatus, error) {
	if store.listErr != nil {
		return nil, store.listErr
	}
	return store.statuses, nil
}
