package gamenews

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/usecase"
)

type fakeGameNewsCache struct {
	records       []map[string]any
	listErr       error
	hasRecords    bool
	hasRecordsErr error
}

type fakeGameNewsStatusStore struct {
	statuses []masterdata.SyncStatus
	listErr  error
}

func newReadyGameNewsHandler(cache *fakeGameNewsCache) *GameNewsHandler {
	syncUsecase := usecase.NewMasterDataSyncUsecase(
		nil,
		nil,
		cache,
		&fakeGameNewsStatusStore{statuses: []masterdata.SyncStatus{{Region: "jp", Status: "success"}}},
		nil,
		1,
	)
	return NewGameNewsHandler(syncUsecase)
}

func (store *fakeGameNewsStatusStore) Save(_ context.Context, _ masterdata.SyncStatus) error {
	return nil
}

func (store *fakeGameNewsStatusStore) List(_ context.Context) ([]masterdata.SyncStatus, error) {
	if store.listErr != nil {
		return nil, store.listErr
	}
	return store.statuses, nil
}

func (cache *fakeGameNewsCache) StoreRegion(_ context.Context, _ string, _ map[string]any) error {
	return nil
}

func (cache *fakeGameNewsCache) GetByID(_ context.Context, _ string, _ string, _ string) (map[string]any, bool, error) {
	return nil, false, nil
}

func (cache *fakeGameNewsCache) ListAll(_ context.Context, _ string, _ string) ([]map[string]any, error) {
	if cache.listErr != nil {
		return nil, cache.listErr
	}

	items := make([]map[string]any, 0, len(cache.records))
	for _, record := range cache.records {
		copied := make(map[string]any, len(record))
		for key, value := range record {
			copied[key] = value
		}
		items = append(items, copied)
	}
	return items, nil
}

func (cache *fakeGameNewsCache) ListByPage(_ context.Context, _ string, _ string, _ int, _ int) ([]map[string]any, int, error) {
	return cache.records, len(cache.records), cache.listErr
}

func (cache *fakeGameNewsCache) Search(_ context.Context, _ string, _ string, _ string, _ []string, _ int) ([]masterdata.SearchMatch, error) {
	return nil, nil
}

func (cache *fakeGameNewsCache) HasEntityRecords(_ context.Context, _ string, _ string) (bool, error) {
	if cache.hasRecordsErr != nil {
		return false, cache.hasRecordsErr
	}
	return cache.hasRecords, nil
}

func (cache *fakeGameNewsCache) HasRegionIndex(_ string) bool {
	return cache.hasRecords
}

func serveGameNewsRequest(t *testing.T, handler *GameNewsHandler, target string) *httptest.ResponseRecorder {
	t.Helper()

	router := gin.New()
	router.GET("/api/v1/game-news/:region/list", handler.List)

	req := httptest.NewRequest(http.MethodGet, target, nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}

func decodeGameNewsItems(t *testing.T, resp *httptest.ResponseRecorder) []map[string]any {
	t.Helper()

	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return body.Items
}

func assertGameNewsError(t *testing.T, resp *httptest.ResponseRecorder, status int, code string) {
	t.Helper()

	if resp.Code != status {
		t.Fatalf("expected status %d, got %d", status, resp.Code)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if body.Error.Code != code {
		t.Fatalf("expected error code %q, got %q", code, body.Error.Code)
	}
}

func TestGameNewsListReturnsCurrentRecordsByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	cache := &fakeGameNewsCache{
		hasRecords: true,
		records: []map[string]any{
			{"id": 1, "startAt": float64(now.Add(-time.Hour).UnixMilli())},
			{"id": 2, "startAt": float64(now.Add(-time.Hour).UnixMilli()), "endAt": float64(now.Add(time.Hour).UnixMilli())},
			{"id": 3, "startAt": float64(now.Add(time.Hour).UnixMilli())},
			{"id": 4, "startAt": float64(now.Add(-time.Hour).UnixMilli()), "endAt": float64(now.Add(-time.Minute).UnixMilli())},
			{"id": 5, "startAt": "not-a-timestamp"},
			{"id": 6},
			{"id": 7, "startAt": float64(now.Add(-time.Hour).UnixMilli()), "endAt": "not-a-timestamp"},
		},
	}

	resp := serveGameNewsRequest(t, newReadyGameNewsHandler(cache), "/api/v1/game-news/jp/list")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	items := decodeGameNewsItems(t, resp)
	if len(items) != 3 {
		t.Fatalf("expected 3 current records, got %d", len(items))
	}
	if items[0]["id"] != float64(1) || items[1]["id"] != float64(2) || items[2]["id"] != float64(7) {
		t.Fatalf("expected current ids [1 2 7], got %v", items)
	}
	if _, exists := items[2]["endAt"]; exists {
		t.Fatalf("expected malformed optional endAt to be omitted: %v", items[2])
	}
}

func TestGameNewsListIncludeAllReturnsAllRecords(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeGameNewsCache{
		hasRecords: true,
		records: []map[string]any{
			{"id": 1, "startAt": 1},
			{"id": 2, "startAt": 2, "endAt": 3},
		},
	}

	resp := serveGameNewsRequest(t, newReadyGameNewsHandler(cache), "/api/v1/game-news/jp/list?includeAll=true")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	if items := decodeGameNewsItems(t, resp); len(items) != 2 {
		t.Fatalf("expected all 2 records, got %d", len(items))
	}
}

func TestGameNewsListRejectsInvalidIncludeAll(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeGameNewsCache{hasRecords: true}

	resp := serveGameNewsRequest(t, newReadyGameNewsHandler(cache), "/api/v1/game-news/jp/list?includeAll=maybe")
	assertGameNewsError(t, resp, http.StatusBadRequest, "INVALID_REQUEST")
}

func TestGameNewsListReturnsServiceUnavailableWhenDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	resp := serveGameNewsRequest(t, NewGameNewsHandler(nil), "/api/v1/game-news/jp/list")
	assertGameNewsError(t, resp, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED")
}

func TestGameNewsListReturnsQueryError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeGameNewsCache{
		hasRecords: true,
		listErr:    errors.New("list user informations failed"),
	}

	resp := serveGameNewsRequest(t, newReadyGameNewsHandler(cache), "/api/v1/game-news/jp/list?includeAll=true")
	assertGameNewsError(t, resp, http.StatusInternalServerError, "GAME_NEWS_QUERY_ERROR")
}

func TestGameNewsListReturnsRegionNotReady(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeGameNewsCache{hasRecords: false}

	resp := serveGameNewsRequest(t, newReadyGameNewsHandler(cache), "/api/v1/game-news/jp/list")
	assertGameNewsError(t, resp, http.StatusServiceUnavailable, "REGION_DATA_NOT_READY")
}

func TestGameNewsListUsesExpectedEntityAndPreservesFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	cache := &fakeGameNewsCache{
		hasRecords: true,
		records: []map[string]any{{
			"id":                    7,
			"seq":                   8,
			"displayOrder":          9,
			"informationType":       "normal",
			"informationTag":        "event",
			"browseType":            "internal",
			"platform":              "all",
			"title":                 "News",
			"path":                  "/news/7",
			"startAt":               float64(now.Add(-time.Hour).UnixMilli()),
			"bannerAssetbundleName": "banner_7",
		}},
	}

	resp := serveGameNewsRequest(t, newReadyGameNewsHandler(cache), "/api/v1/game-news/jp/list")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	body := resp.Body.String()
	for _, field := range []string{"id", "seq", "displayOrder", "informationType", "informationTag", "browseType", "platform", "title", "path", "startAt", "bannerAssetbundleName"} {
		if !strings.Contains(body, `"`+field+`"`) {
			t.Fatalf("expected response to preserve field %q: %s", field, body)
		}
	}
}

func TestParseGameNewsTimestampNormalizesEpochUnitsAndFractions(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  int64
	}{
		{name: "seconds", value: int64(1_700_000_000), want: 1_700_000_000_000},
		{name: "fractional seconds", value: 1_700_000_000.125, want: 1_700_000_000_125},
		{name: "fractional milliseconds string", value: "1700000000123.75", want: 1_700_000_000_123},
		{name: "fractional microseconds string", value: "1700000000000000.5", want: 1_700_000_000_000},
		{name: "json number", value: json.Number("1700000000.25"), want: 1_700_000_000_250},
		{name: "rfc3339", value: "2023-11-14T22:13:20Z", want: 1_700_000_000_000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseGameNewsTimestamp(tt.value)
			if !ok {
				t.Fatalf("expected timestamp to parse")
			}
			if got != tt.want {
				t.Fatalf("expected %d, got %d", tt.want, got)
			}
		})
	}
}

func TestNormalizeGameNewsRecordsDropsMalformedRequiredTimestamps(t *testing.T) {
	records := []map[string]any{
		{
			"id":      1,
			"title":   "valid",
			"startAt": "1700000000.5",
			"endAt":   "1700000001000000",
		},
		{
			"id":      2,
			"startAt": "1700000001",
			"endAt":   "not-a-timestamp",
		},
		{
			"id":      3,
			"startAt": "not-a-timestamp",
			"endAt":   "1700000002",
		},
		{
			"id":    4,
			"endAt": "1700000002",
		},
		{
			"id":      5,
			"startAt": "1700000003",
			"endAt":   nil,
		},
	}

	normalized := normalizeGameNewsRecords(records)
	if len(normalized) != 3 {
		t.Fatalf("expected 3 normalized records, got %d", len(normalized))
	}

	if normalized[0]["id"] != 1 || normalized[0]["startAt"] != int64(1_700_000_000_500) || normalized[0]["endAt"] != int64(1_700_000_001_000) {
		t.Fatalf("unexpected normalized first record: %v", normalized[0])
	}
	if normalized[1]["id"] != 2 {
		t.Fatalf("expected malformed optional endAt record to remain, got %v", normalized[1])
	}
	if _, exists := normalized[1]["endAt"]; exists {
		t.Fatalf("expected malformed optional endAt to be omitted: %v", normalized[1])
	}
	if normalized[2]["id"] != 5 || normalized[2]["endAt"] != nil {
		t.Fatalf("expected explicit null endAt to remain nullable: %v", normalized[2])
	}
}

func TestGameNewsListOmitsMalformedRecordsAndNormalizesResponseTimestamps(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeGameNewsCache{
		hasRecords: true,
		records: []map[string]any{
			{"id": 1, "startAt": "1700000000.5", "endAt": "1700000001000000"},
			{"id": 2, "startAt": "bad-start", "endAt": "1700000002"},
			{"id": 3, "startAt": "1700000003", "endAt": "bad-end"},
		},
	}

	resp := serveGameNewsRequest(t, newReadyGameNewsHandler(cache), "/api/v1/game-news/jp/list?includeAll=true")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	items := decodeGameNewsItems(t, resp)
	if len(items) != 2 {
		t.Fatalf("expected 2 normalized records, got %d", len(items))
	}
	if items[0]["id"] != float64(1) || items[1]["id"] != float64(3) {
		t.Fatalf("expected normalized ids [1 3], got %v", items)
	}
	if items[0]["startAt"] != float64(1_700_000_000_500) || items[0]["endAt"] != float64(1_700_000_001_000) {
		t.Fatalf("expected normalized first timestamps, got %v", items[0])
	}
	if _, exists := items[1]["endAt"]; exists {
		t.Fatalf("expected malformed optional endAt to be omitted: %v", items[1])
	}
}
