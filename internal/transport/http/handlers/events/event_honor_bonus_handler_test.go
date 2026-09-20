package events

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/usecase"
)

type eventHonorBonusTrackingCache struct {
	*fakeEventHandlerCache
	listCalls   map[string]map[string]int
	getByIDCall []eventHonorBonusGetByIDCall
	listErrors  map[string]error
	getByIDErr  error
}

type eventHonorBonusGetByIDCall struct {
	region string
	entity string
	id     string
}

func (cache *eventHonorBonusTrackingCache) ListAll(
	ctx context.Context,
	region string,
	entity string,
) ([]map[string]any, error) {
	if cache.listCalls == nil {
		cache.listCalls = make(map[string]map[string]int)
	}
	if cache.listCalls[region] == nil {
		cache.listCalls[region] = make(map[string]int)
	}
	cache.listCalls[region][entity]++
	if err := cache.listErrors[entity]; err != nil {
		return nil, err
	}

	return cache.fakeEventHandlerCache.ListAll(ctx, region, entity)
}

func (cache *eventHonorBonusTrackingCache) GetByID(
	ctx context.Context,
	region string,
	entity string,
	id string,
) (map[string]any, bool, error) {
	cache.getByIDCall = append(cache.getByIDCall, eventHonorBonusGetByIDCall{
		region: region,
		entity: entity,
		id:     id,
	})
	if cache.getByIDErr != nil {
		return nil, false, cache.getByIDErr
	}

	return cache.fakeEventHandlerCache.GetByID(ctx, region, entity, id)
}

func newEventHonorBonusHandlerForRegions(
	cache *eventHonorBonusTrackingCache,
	regions ...string,
) *EventHandler {
	statuses := make([]masterdata.SyncStatus, 0, len(regions))
	for _, region := range regions {
		statuses = append(statuses, masterdata.SyncStatus{Region: region, Status: "success"})
	}
	statusStore := &fakeEventHandlerStatusStore{statuses: statuses}
	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	return NewEventHandler(syncUsecase)
}

func newEventHonorBonusRouter(handler *EventHandler) *gin.Engine {
	router := gin.New()
	router.GET("/api/v1/events/:region/:id/honor-bonuses", handler.HonorBonusesByID)
	return router
}

func newEventHonorBonusFixtureCache() *eventHonorBonusTrackingCache {
	cache := &eventHonorBonusTrackingCache{
		fakeEventHandlerCache: &fakeEventHandlerCache{
			byID:         map[string]map[string]map[string]map[string]any{},
			listByEntity: map[string]map[string][]map[string]any{},
			hasRecords:   map[string]map[string]bool{},
			hasIndexSet:  true,
			hasIndex:     false,
		},
		listCalls:  make(map[string]map[string]int),
		listErrors: make(map[string]error),
	}

	return cache
}

func addEventHonorBonusFixture(
	cache *eventHonorBonusTrackingCache,
	region string,
	eventID int64,
	honorID int64,
	groupID int64,
	unitID int64,
) {
	if cache.byID[region] == nil {
		cache.byID[region] = make(map[string]map[string]map[string]any)
	}
	if cache.listByEntity[region] == nil {
		cache.listByEntity[region] = make(map[string][]map[string]any)
	}
	cache.hasRecords[region] = map[string]bool{"events": true}

	eventIDText := strconv.FormatInt(eventID, 10)
	cache.byID[region]["events"] = map[string]map[string]any{
		eventIDText: {"id": eventID, "name": "event-" + region},
	}
	cache.listByEntity[region][eventHonorBonusesEntity] = []map[string]any{
		{
			"id":                    int64(30),
			"eventId":               eventID,
			"honorId":               honorID,
			"leaderGameCharacterId": unitID,
			"bonusRate":             int64(25),
			"unknownField":          "must not leak",
		},
		{"id": int64(1), "eventId": int64(999999), "honorId": honorID, "bonusRate": int64(90)},
	}
	cache.listByEntity[region][eventHonorBonusHonorsEntity] = []map[string]any{
		{
			"id":               honorID,
			"groupId":          groupID,
			"name":             "Honor " + region,
			"honorRarity":      "honor_rarity_3",
			"honorMissionType": "event",
			"honorTypeId":      int64(7),
			"assetbundleName":  "honor_" + region,
			"levels":           []any{"must not leak"},
			"unknownField":     "must not leak",
		},
	}
	cache.listByEntity[region][eventHonorBonusHonorGroupsEntity] = []map[string]any{
		{
			"id":                        groupID,
			"name":                      "Group " + region,
			"honorType":                 "rank",
			"backgroundAssetbundleName": "group_bg_" + region,
			"frameName":                 "frame_" + region,
			"unknownField":              "must not leak",
		},
	}
	cache.listByEntity[region][eventHonorBonusGameCharacterEntity] = []map[string]any{
		{
			"id":              unitID,
			"gameCharacterId": unitID + 1000,
			"unit":            "unit_" + region,
			"colorCode":       "must not leak",
		},
	}
}

func decodeEventHonorBonusList(t *testing.T, body []byte) []map[string]any {
	t.Helper()

	var response struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode event honor bonus list: %v", err)
	}

	return response.Items
}

func eventHonorBonusErrorCode(t *testing.T, body []byte) string {
	t.Helper()

	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode event honor bonus error: %v", err)
	}

	return response.Error.Code
}

func serveEventHonorBonusRequest(
	t *testing.T,
	router *gin.Engine,
	path string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}

func callEventHonorBonusHandler(
	handler *EventHandler,
	region string,
	id string,
	path string,
) *httptest.ResponseRecorder {
	resp := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(resp)
	context.Request = httptest.NewRequest(http.MethodGet, path, nil)
	context.Params = gin.Params{
		{Key: "region", Value: region},
		{Key: "id", Value: id},
	}
	handler.HonorBonusesByID(context)
	return resp
}

func TestEventHonorBonusesTypedEndpointSupportsFiveRegionsAndBatchEnrichment(t *testing.T) {
	gin.SetMode(gin.TestMode)

	regions := []string{"jp", "en", "tw", "kr", "cn"}
	cache := newEventHonorBonusFixtureCache()
	for index, region := range regions {
		addEventHonorBonusFixture(
			cache,
			region,
			int64(100+index),
			int64(200+index),
			int64(300+index),
			int64(400+index),
		)
	}
	router := newEventHonorBonusRouter(newEventHonorBonusHandlerForRegions(cache, regions...))

	for index, region := range regions {
		t.Run(region, func(t *testing.T) {
			assertEventHonorBonusTypedRegionResponse(
				t,
				router,
				cache,
				region,
				index,
			)
		})
	}

	assertEventHonorBonusParentLookups(t, cache, len(regions))
}

func assertEventHonorBonusTypedRegionResponse(
	t *testing.T,
	router *gin.Engine,
	cache *eventHonorBonusTrackingCache,
	region string,
	index int,
) {
	t.Helper()

	eventID := strconv.Itoa(100 + index)
	resp := serveEventHonorBonusRequest(t, router, "/api/v1/events/"+region+"/"+eventID+"/honor-bonuses")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	items := decodeEventHonorBonusList(t, resp.Body.Bytes())
	if len(items) != 1 {
		t.Fatalf("expected one matching item, got %#v", items)
	}

	item := items[0]
	assertEventHonorBonusBaseProjection(t, item, index)
	assertEventHonorBonusHonorProjection(
		t,
		item,
		region,
		index,
	)
	assertEventHonorBonusLeaderUnitProjection(
		t,
		item,
		region,
		index,
	)
	assertEventHonorBonusBatchListCalls(t, cache, region)
}

func assertEventHonorBonusBaseProjection(t *testing.T, item map[string]any, index int) {
	t.Helper()

	if item["id"] != float64(30) || item["eventId"] != float64(100+index) ||
		item["honorId"] != float64(200+index) || item["leaderGameCharacterId"] != float64(400+index) ||
		item["bonusRate"] != float64(25) {
		t.Fatalf("unexpected typed base projection: %#v", item)
	}
	if _, leaked := item["unknownField"]; leaked {
		t.Fatalf("unknown base field leaked: %#v", item)
	}
}

func assertEventHonorBonusHonorProjection(t *testing.T, item map[string]any, region string, index int) {
	t.Helper()

	honor, ok := item["honor"].(map[string]any)
	if !ok || honor["id"] != float64(200+index) || honor["groupId"] != float64(300+index) ||
		honor["name"] != "Honor "+region || honor["honorTypeId"] != float64(7) {
		t.Fatalf("unexpected honor projection: %#v", item["honor"])
	}
	if _, leaked := honor["levels"]; leaked {
		t.Fatalf("honor levels leaked: %#v", honor)
	}
	assertEventHonorBonusGroupProjection(
		t,
		honor,
		region,
		index,
	)
}

func assertEventHonorBonusGroupProjection(t *testing.T, honor map[string]any, region string, index int) {
	t.Helper()

	group, ok := honor["group"].(map[string]any)
	if !ok || group["id"] != float64(300+index) ||
		group["name"] != "Group "+region || group["honorType"] != "rank" {
		t.Fatalf("unexpected honor group projection: %#v", honor["group"])
	}
}

func assertEventHonorBonusLeaderUnitProjection(t *testing.T, item map[string]any, region string, index int) {
	t.Helper()

	unit, ok := item["leaderGameCharacterUnit"].(map[string]any)
	if !ok || unit["id"] != float64(400+index) ||
		unit["gameCharacterId"] != float64(1400+index) || unit["unit"] != "unit_"+region {
		t.Fatalf("unexpected leader unit projection: %#v", item["leaderGameCharacterUnit"])
	}
	if _, leaked := unit["colorCode"]; leaked {
		t.Fatalf("unknown leader unit field leaked: %#v", unit)
	}
}

func assertEventHonorBonusBatchListCalls(t *testing.T, cache *eventHonorBonusTrackingCache, region string) {
	t.Helper()

	expectedCalls := []struct {
		entity string
		label  string
	}{
		{entity: eventHonorBonusesEntity, label: "event honor bonus"},
		{entity: eventHonorBonusHonorsEntity, label: "honors"},
		{entity: eventHonorBonusHonorGroupsEntity, label: "honor groups"},
		{entity: eventHonorBonusGameCharacterEntity, label: "game character units"},
	}
	for _, expected := range expectedCalls {
		if got := cache.listCalls[region][expected.entity]; got != 1 {
			t.Fatalf("expected one %s ListAll call, got %d", expected.label, got)
		}
	}
}

func assertEventHonorBonusParentLookups(t *testing.T, cache *eventHonorBonusTrackingCache, expectedCount int) {
	t.Helper()

	if len(cache.getByIDCall) != expectedCount {
		t.Fatalf("expected one parent GetByID call per request, got %#v", cache.getByIDCall)
	}
	for _, call := range cache.getByIDCall {
		if call.entity != "events" {
			t.Fatalf("relation lookups must not use GetByID: %#v", cache.getByIDCall)
		}
	}
}

func TestEventHonorBonusesSortByIDAndFilterByEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := newEventHonorBonusFixtureCache()
	cache.byID["jp"] = map[string]map[string]map[string]any{
		"events": {"101": {"id": int64(101)}},
	}
	cache.hasRecords["jp"] = map[string]bool{"events": true}
	cache.listByEntity["jp"] = map[string][]map[string]any{
		eventHonorBonusesEntity: {
			{"id": int64(12), "eventId": int64(101), "bonusRate": int64(12)},
			{"id": int64(2), "eventId": "101", "bonusRate": int64(2)},
			{"id": int64(7), "eventId": int64(101), "bonusRate": int64(7)},
			{"id": int64(1), "eventId": int64(202), "bonusRate": int64(1)},
		},
		eventHonorBonusHonorsEntity: {},
	}
	router := newEventHonorBonusRouter(newEventHonorBonusHandlerForRegions(cache, "jp"))

	resp := serveEventHonorBonusRequest(t, router, "/api/v1/events/jp/101/honor-bonuses")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	items := decodeEventHonorBonusList(t, resp.Body.Bytes())
	if len(items) != 3 || items[0]["id"] != float64(2) || items[1]["id"] != float64(7) || items[2]["id"] != float64(12) {
		t.Fatalf("expected matching records sorted by id ASC, got %#v", items)
	}
}

func TestEventHonorBonusesPreserveBaseWhenAssociationsAreMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := newEventHonorBonusFixtureCache()
	addEventHonorBonusFixture(cache, "jp", 101, 201, 301, 401)
	cache.listByEntity["jp"][eventHonorBonusesEntity] = []map[string]any{
		{"id": int64(1), "eventId": int64(101), "honorId": int64(999), "leaderGameCharacterId": int64(999), "bonusRate": int64(10)},
	}
	cache.listByEntity["en"] = map[string][]map[string]any{
		eventHonorBonusHonorsEntity:        {{"id": int64(999), "name": "cross-region"}},
		eventHonorBonusGameCharacterEntity: {{"id": int64(999), "unit": "cross-region"}},
	}
	router := newEventHonorBonusRouter(newEventHonorBonusHandlerForRegions(cache, "jp", "en"))

	resp := serveEventHonorBonusRequest(t, router, "/api/v1/events/jp/101/honor-bonuses")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	items := decodeEventHonorBonusList(t, resp.Body.Bytes())
	if len(items) != 1 || items[0]["id"] != float64(1) || items[0]["honorId"] != float64(999) || items[0]["leaderGameCharacterId"] != float64(999) || items[0]["bonusRate"] != float64(10) {
		t.Fatalf("expected base bonus to be preserved, got %#v", items)
	}
	if _, found := items[0]["honor"]; found {
		t.Fatalf("missing same-region honor must not create an empty or cross-region object: %#v", items[0])
	}
	if _, found := items[0]["leaderGameCharacterUnit"]; found {
		t.Fatalf("missing same-region leader unit must not create an empty or cross-region object: %#v", items[0])
	}
}

func TestEventHonorBonusesReturnEmptyItemsWithoutRelationQueries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := newEventHonorBonusFixtureCache()
	cache.byID["jp"] = map[string]map[string]map[string]any{
		"events": {"101": {"id": int64(101)}},
	}
	cache.hasRecords["jp"] = map[string]bool{"events": true}
	cache.listByEntity["jp"] = map[string][]map[string]any{eventHonorBonusesEntity: {}}
	router := newEventHonorBonusRouter(newEventHonorBonusHandlerForRegions(cache, "jp"))

	resp := serveEventHonorBonusRequest(t, router, "/api/v1/events/jp/101/honor-bonuses")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	items := decodeEventHonorBonusList(t, resp.Body.Bytes())
	if items == nil || len(items) != 0 {
		t.Fatalf("expected a non-nil empty items array, got %#v", items)
	}
	if cache.listCalls["jp"][eventHonorBonusHonorsEntity] != 0 || cache.listCalls["jp"][eventHonorBonusGameCharacterEntity] != 0 {
		t.Fatalf("empty result must not query relations: %#v", cache.listCalls)
	}
}

func TestEventHonorBonusesValidateAvailabilityParentAndQueryParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := newEventHonorBonusFixtureCache()
	addEventHonorBonusFixture(cache, "jp", 101, 201, 301, 401)
	handler := newEventHonorBonusHandlerForRegions(cache, "jp")
	router := newEventHonorBonusRouter(handler)

	tests := []struct {
		name     string
		response *httptest.ResponseRecorder
		status   int
		code     string
	}{
		{
			name:     "unknown query parameter",
			response: serveEventHonorBonusRequest(t, router, "/api/v1/events/jp/101/honor-bonuses?extra=1"),
			status:   http.StatusBadRequest,
			code:     "INVALID_REQUEST",
		},
		{
			name:     "missing region",
			response: callEventHonorBonusHandler(handler, "", "101", "/api/v1/events//101/honor-bonuses"),
			status:   http.StatusBadRequest,
			code:     "INVALID_REQUEST",
		},
		{
			name:     "missing id",
			response: callEventHonorBonusHandler(handler, "jp", "", "/api/v1/events/jp//honor-bonuses"),
			status:   http.StatusBadRequest,
			code:     "INVALID_REQUEST",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.response.Code != test.status || eventHonorBonusErrorCode(t, test.response.Body.Bytes()) != test.code {
				t.Fatalf("expected %d %s, got %d: %s", test.status, test.code, test.response.Code, test.response.Body.String())
			}
		})
	}

	missingEventCache := newEventHonorBonusFixtureCache()
	missingEventCache.hasRecords["jp"] = map[string]bool{"events": true}
	missingEventHandler := newEventHonorBonusHandlerForRegions(missingEventCache, "jp")
	missingEventRouter := newEventHonorBonusRouter(missingEventHandler)
	missing := serveEventHonorBonusRequest(t, missingEventRouter, "/api/v1/events/jp/999/honor-bonuses")
	if missing.Code != http.StatusNotFound || eventHonorBonusErrorCode(t, missing.Body.Bytes()) != "EVENT_NOT_FOUND" {
		t.Fatalf("expected EVENT_NOT_FOUND, got %d: %s", missing.Code, missing.Body.String())
	}
	if len(missingEventCache.getByIDCall) != 1 || len(missingEventCache.listCalls["jp"]) != 0 {
		t.Fatalf("parent absence must stop before association queries: gets=%#v lists=%#v", missingEventCache.getByIDCall, missingEventCache.listCalls)
	}

	noMasterData := callEventHonorBonusHandler(NewEventHandler(nil), "jp", "101", "/api/v1/events/jp/101/honor-bonuses")
	if noMasterData.Code != http.StatusServiceUnavailable || eventHonorBonusErrorCode(t, noMasterData.Body.Bytes()) != "MASTER_DATA_DISABLED" {
		t.Fatalf("expected MASTER_DATA_DISABLED, got %d: %s", noMasterData.Code, noMasterData.Body.String())
	}
}

func TestEventHonorBonusesMapStorageErrorsToEventQueryError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	entities := []string{
		eventHonorBonusesEntity,
		eventHonorBonusHonorsEntity,
		eventHonorBonusHonorGroupsEntity,
		eventHonorBonusGameCharacterEntity,
	}
	for _, entity := range entities {
		t.Run(entity, func(t *testing.T) {
			cache := newEventHonorBonusFixtureCache()
			addEventHonorBonusFixture(cache, "jp", 101, 201, 301, 401)
			cache.listErrors[entity] = errors.New("storage failure")
			router := newEventHonorBonusRouter(newEventHonorBonusHandlerForRegions(cache, "jp"))

			resp := serveEventHonorBonusRequest(t, router, "/api/v1/events/jp/101/honor-bonuses")
			if resp.Code != http.StatusInternalServerError || eventHonorBonusErrorCode(t, resp.Body.Bytes()) != "EVENT_QUERY_ERROR" {
				t.Fatalf("expected EVENT_QUERY_ERROR, got %d: %s", resp.Code, resp.Body.String())
			}
		})
	}
}
