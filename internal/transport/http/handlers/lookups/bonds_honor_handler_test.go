package lookups

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/usecase"
)

type bondsHonorListAllCall struct {
	region string
	entity string
}

type bondsHonorTrackingCache struct {
	*fakeLookupCache
	listAllCalls  []bondsHonorListAllCall
	listAllErrors map[string]error
}

func (cache *bondsHonorTrackingCache) ListAll(ctx context.Context, region, entity string) ([]map[string]any, error) {
	cache.listAllCalls = append(cache.listAllCalls, bondsHonorListAllCall{region: region, entity: entity})
	if err := cache.listAllErrors[entity]; err != nil {
		return nil, err
	}

	return cache.fakeLookupCache.ListAll(ctx, region, entity)
}

func newBondsHonorRouter(handler *LookupHandler) *gin.Engine {
	router := gin.New()
	router.GET("/api/v1/bondsHonors/:region/list", handler.BondsHonorsList)
	router.GET("/api/v1/bondsHonors/:region/:id", handler.BondsHonorsByID)
	return router
}

func newReadyBondsHonorTrackingHandler(cache *bondsHonorTrackingCache) *LookupHandler {
	statusStore := &fakeLookupStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "jp", Status: "success"},
			{Region: "en", Status: "success"},
		},
	}
	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	return NewLookupHandler(syncUsecase)
}

func newReadyBondsHonorHandler(cache *fakeLookupCache, regions ...string) *LookupHandler {
	statuses := make([]masterdata.SyncStatus, 0, len(regions))
	for _, region := range regions {
		statuses = append(statuses, masterdata.SyncStatus{Region: region, Status: "success"})
	}
	statusStore := &fakeLookupStatusStore{statuses: statuses}
	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	return NewLookupHandler(syncUsecase)
}

func newBondsHonorRecord(id, seq, groupID, characterUnitID1, characterUnitID2 int64, name string) map[string]any {
	return map[string]any{
		"id":                            id,
		"seq":                           seq,
		"bondsGroupId":                  groupID,
		"gameCharacterUnitId1":          characterUnitID1,
		"gameCharacterUnitId2":          characterUnitID2,
		"honorRarity":                   "high",
		"name":                          name,
		"pronunciation":                 "test pronunciation",
		"description":                   "test description",
		"configurableUnitVirtualSinger": false,
		"levels": []any{
			map[string]any{
				"id":           id*10 + 1,
				"bondsHonorId": id,
				"level":        1,
				"description":  "first level",
				"unknownLevel": "must not be exposed",
			},
			"not a level object",
		},
		"unknownRoot": "must not be exposed",
	}
}

func newBondsHonorCharacterUnitRecord(id, gameCharacterID int64, unit string) map[string]any {
	return map[string]any{
		"id":              id,
		"gameCharacterId": gameCharacterID,
		"unit":            unit,
	}
}

func newBondsHonorWordRecord(id, seq, groupID int64, name string) map[string]any {
	return map[string]any{
		"id":              id,
		"seq":             seq,
		"bondsGroupId":    groupID,
		"assetbundleName": "word-" + strconv.FormatInt(id, 10),
		"name":            name,
		"description":     "description-" + name,
		"unknown":         "must not be exposed",
	}
}

func decodeBondsHonorObject(t *testing.T, body []byte) map[string]any {
	t.Helper()

	var item map[string]any
	if err := json.Unmarshal(body, &item); err != nil {
		t.Fatalf("decode bonds honor object: %v", err)
	}
	return item
}

func decodeBondsHonorList(t *testing.T, body []byte) ([]map[string]any, map[string]any) {
	t.Helper()

	var response struct {
		Items      []map[string]any `json:"items"`
		Pagination map[string]any   `json:"pagination"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode bonds honor list: %v", err)
	}
	return response.Items, response.Pagination
}

func bondsHonorErrorCode(t *testing.T, body []byte) string {
	t.Helper()

	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	return response.Error.Code
}

func assertBondsHonorProjection(t *testing.T, item map[string]any, id, groupID, unitID1, unitID2 int64) {
	t.Helper()

	assertBondsHonorBaseFields(t, item, id, groupID, unitID1, unitID2)
	assertBondsHonorLevels(t, item, id)
	assertBondsHonorRelationships(t, item, groupID, unitID1, unitID2)
}

func assertBondsHonorBaseFields(t *testing.T, item map[string]any, id, groupID, unitID1, unitID2 int64) {
	t.Helper()

	if len(item) != 14 {
		t.Fatalf("expected only typed bonds honor fields, got %#v", item)
	}
	if item["id"] != float64(id) || item["bondsGroupId"] != float64(groupID) || item["gameCharacterUnitId1"] != float64(unitID1) || item["gameCharacterUnitId2"] != float64(unitID2) {
		t.Fatalf("unexpected bonds honor identifiers: %#v", item)
	}
	if item["honorRarity"] != "high" || item["pronunciation"] != "test pronunciation" || item["description"] != "test description" || item["configurableUnitVirtualSinger"] != false {
		t.Fatalf("expected typed root values to be projected, got %#v", item)
	}
	if _, leaked := item["unknownRoot"]; leaked {
		t.Fatalf("unknown root field leaked: %#v", item)
	}
}

func assertBondsHonorLevels(t *testing.T, item map[string]any, id int64) {
	t.Helper()

	levels, ok := item["levels"].([]any)
	if !ok || len(levels) != 1 {
		t.Fatalf("expected valid normalized levels, got %#v", item["levels"])
	}
	level, ok := levels[0].(map[string]any)
	if !ok || len(level) != 4 || level["id"] != float64(id*10+1) || level["bondsHonorId"] != float64(id) || level["level"] != float64(1) || level["description"] != "first level" {
		t.Fatalf("expected bonds honor level fields only, got %#v", levels[0])
	}
}

func assertBondsHonorRelationships(t *testing.T, item map[string]any, groupID, unitID1, unitID2 int64) {
	t.Helper()

	group, ok := item["bondsGroup"].(map[string]any)
	if !ok || len(group) != 3 || group["groupId"] != float64(groupID) || group["characterId1"] != float64(201) || group["characterId2"] != float64(202) {
		t.Fatalf("expected bonds group matched by groupId and projected narrowly, got %#v", item["bondsGroup"])
	}
	unit1, ok := item["characterUnit1"].(map[string]any)
	if !ok || len(unit1) != 3 || unit1["id"] != float64(unitID1) || unit1["gameCharacterId"] != float64(unitID1+1000) || unit1["unit"] != "unit-1" {
		t.Fatalf("expected typed first character unit, got %#v", item["characterUnit1"])
	}
	unit2, ok := item["characterUnit2"].(map[string]any)
	if !ok || len(unit2) != 3 || unit2["id"] != float64(unitID2) || unit2["gameCharacterId"] != float64(unitID2+1000) || unit2["unit"] != "unit-2" {
		t.Fatalf("expected typed second character unit, got %#v", item["characterUnit2"])
	}
}

func TestBondsHonorsTypedEndpointsSupportFiveRegions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	regions := []string{"jp", "en", "tw", "kr", "cn"}
	cache := &fakeLookupCache{
		byID:         make(map[string]map[string]map[string]map[string]any, len(regions)),
		listByEntity: make(map[string]map[string][]map[string]any, len(regions)),
	}

	for index, region := range regions {
		id := int64(100 + index)
		groupID := int64(300 + index)
		unitID1 := int64(10 + index*2)
		unitID2 := unitID1 + 1
		record := newBondsHonorRecord(id, int64(index+1), groupID, unitID1, unitID2, "Bonds honor "+region)
		cache.byID[region] = map[string]map[string]map[string]any{
			bondsHonorsEntity: {strconv.FormatInt(id, 10): record},
		}
		cache.listByEntity[region] = map[string][]map[string]any{
			bondsHonorsEntity: {record},
			bondsHonorBondsEntity: {{
				"id":           groupID + 5000,
				"groupId":      groupID,
				"characterId1": int64(201),
				"characterId2": int64(202),
				"unknown":      "must not be exposed",
			}},
			bondsHonorGameCharacterUnitsEntity: {
				{"id": unitID1, "gameCharacterId": unitID1 + 1000, "unit": "unit-1", "unknown": "hidden"},
				{"id": unitID2, "gameCharacterId": unitID2 + 1000, "unit": "unit-2", "unknown": "hidden"},
			},
		}
	}

	router := newBondsHonorRouter(newReadyBondsHonorHandler(cache, regions...))
	for index, region := range regions {
		id := int64(100 + index)
		groupID := int64(300 + index)
		unitID1 := int64(10 + index*2)
		unitID2 := unitID1 + 1

		listResponse := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/"+region+"/list")
		if listResponse.Code != http.StatusOK {
			t.Fatalf("expected list 200 for %s, got %d: %s", region, listResponse.Code, listResponse.Body.String())
		}
		items, _ := decodeBondsHonorList(t, listResponse.Body.Bytes())
		if len(items) != 1 {
			t.Fatalf("expected one bonds honor in %s, got %#v", region, items)
		}
		assertBondsHonorProjection(t, items[0], id, groupID, unitID1, unitID2)

		byIDResponse := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/"+region+"/"+strconv.FormatInt(id, 10))
		if byIDResponse.Code != http.StatusOK {
			t.Fatalf("expected by-id 200 for %s, got %d: %s", region, byIDResponse.Code, byIDResponse.Body.String())
		}
		assertBondsHonorProjection(t, decodeBondsHonorObject(t, byIDResponse.Body.Bytes()), id, groupID, unitID1, unitID2)
	}
}

func TestBondsHonorsWordsAreGroupedProjectedAndSortedForListAndByID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	firstHonor := newBondsHonorRecord(1, 1, 77, 11, 12, "First")
	secondHonor := newBondsHonorRecord(2, 2, 88, 21, 22, "Second")
	wordRecords := []map[string]any{
		newBondsHonorWordRecord(22, 2, 77, "Later"),
		newBondsHonorWordRecord(10, 1, 77, "First tie"),
		newBondsHonorWordRecord(9, 1, 77, "Second tie"),
		newBondsHonorWordRecord(30, 1, 88, "Other group"),
		newBondsHonorWordRecord(40, 1, 99, "Unmatched group"),
	}
	cache := &bondsHonorTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			byID: map[string]map[string]map[string]map[string]any{
				"jp": {bondsHonorsEntity: {"1": firstHonor}},
			},
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {
					bondsHonorsEntity: {firstHonor, secondHonor},
					"bondshonorwords": wordRecords,
				},
			},
		},
	}
	router := newBondsHonorRouter(newReadyBondsHonorTrackingHandler(cache))

	listResponse := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/list")
	if listResponse.Code != http.StatusOK {
		t.Fatalf("expected list 200, got %d: %s", listResponse.Code, listResponse.Body.String())
	}
	items, _ := decodeBondsHonorList(t, listResponse.Body.Bytes())
	if len(items) != 2 {
		t.Fatalf("expected two bonds honors, got %#v", items)
	}
	assertBondsHonorWords(t, items[0], []int64{9, 10, 22}, 77)
	assertBondsHonorWords(t, items[1], []int64{30}, 88)

	byIDResponse := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/1")
	if byIDResponse.Code != http.StatusOK {
		t.Fatalf("expected by-ID 200, got %d: %s", byIDResponse.Code, byIDResponse.Body.String())
	}
	assertBondsHonorWords(t, decodeBondsHonorObject(t, byIDResponse.Body.Bytes()), []int64{9, 10, 22}, 77)

	if bondsHonorWordsEntity != "bondsHonorWords" {
		t.Fatalf("unexpected Bonds Honor words entity key %q", bondsHonorWordsEntity)
	}
	wordReads := 0
	for _, call := range cache.listAllCalls {
		if call.entity == "bondsHonorWords" {
			wordReads++
			if call.region != "jp" {
				t.Fatalf("expected words to be loaded from the request region, got %+v", call)
			}
		}
	}
	if wordReads != 2 {
		t.Fatalf("expected one words entity read per endpoint request, got %d calls: %+v", wordReads, cache.listAllCalls)
	}
}

func assertBondsHonorWords(t *testing.T, item map[string]any, expectedIDs []int64, groupID int64) {
	t.Helper()

	words, ok := item["words"].([]any)
	if !ok || len(words) != len(expectedIDs) {
		t.Fatalf("expected words %v for group %d, got %#v", expectedIDs, groupID, item["words"])
	}
	for index, expectedID := range expectedIDs {
		assertBondsHonorWord(t, words[index], expectedID, groupID)
	}
}

func assertBondsHonorWord(t *testing.T, value any, expectedID, groupID int64) {
	t.Helper()

	word, ok := value.(map[string]any)
	if !ok || len(word) != 6 {
		t.Fatalf("expected six projected word fields, got %#v", value)
	}
	if word["id"] != float64(expectedID) || word["bondsGroupId"] != float64(groupID) {
		t.Fatalf("unexpected word/group association: %#v", word)
	}
	if word["assetbundleName"] != "word-"+strconv.FormatInt(expectedID, 10) || word["name"] == nil || word["description"] == nil {
		t.Fatalf("expected all supported word fields to be projected: %#v", word)
	}
	if _, exists := word["seq"]; !exists {
		t.Fatalf("word seq is missing from projection: %#v", word)
	}
	if _, exists := word["unknown"]; exists {
		t.Fatalf("unknown word field leaked into response: %#v", word)
	}
}

func TestBondsHonorsListDefaultsToSequenceSortAndPaginatesAfterSorting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	records := []map[string]any{
		newBondsHonorRecord(5, 1, 25, 15, 16, "Echo"),
		newBondsHonorRecord(3, 1, 23, 13, 14, "Alpha"),
		newBondsHonorRecord(4, 1, 24, 14, 15, "Charlie"),
		newBondsHonorRecord(2, 2, 22, 12, 13, "Bravo"),
	}
	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {bondsHonorsEntity: records},
		},
	}
	router := newBondsHonorRouter(newReadyLookupHandler(cache))

	firstPage := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/list?page_size=2")
	if firstPage.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", firstPage.Code, firstPage.Body.String())
	}
	items, pagination := decodeBondsHonorList(t, firstPage.Body.Bytes())
	if len(items) != 2 || items[0]["id"] != float64(3) || items[1]["id"] != float64(4) {
		t.Fatalf("expected seq ASC and id ASC tie-break ordering, got %#v", items)
	}
	if pagination["page"] != float64(1) || pagination["page_size"] != float64(2) || pagination["total"] != float64(4) || pagination["total_pages"] != float64(2) || pagination["has_next"] != true {
		t.Fatalf("unexpected first-page pagination: %#v", pagination)
	}

	secondPage := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/list?page=2&page_size=2")
	items, pagination = decodeBondsHonorList(t, secondPage.Body.Bytes())
	if len(items) != 2 || items[0]["id"] != float64(5) || items[1]["id"] != float64(2) || pagination["has_next"] != false {
		t.Fatalf("expected second sorted page, got items=%#v pagination=%#v", items, pagination)
	}

	defaultPage := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/list")
	items, pagination = decodeBondsHonorList(t, defaultPage.Body.Bytes())
	if len(items) != 4 || pagination["page"] != float64(1) || pagination["page_size"] != float64(20) {
		t.Fatalf("expected default page=1/page_size=20, got items=%#v pagination=%#v", items, pagination)
	}

	nameSort := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/list?sort_by=name&sort_order=desc")
	items, _ = decodeBondsHonorList(t, nameSort.Body.Bytes())
	if len(items) != 4 || items[0]["id"] != float64(5) || items[1]["id"] != float64(4) || items[2]["id"] != float64(2) || items[3]["id"] != float64(3) {
		t.Fatalf("expected name DESC sorting, got %#v", items)
	}

	defaultFieldDesc := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/list?sort_order=desc")
	items, _ = decodeBondsHonorList(t, defaultFieldDesc.Body.Bytes())
	if len(items) != 4 || items[0]["id"] != float64(2) || items[1]["id"] != float64(3) || items[2]["id"] != float64(4) || items[3]["id"] != float64(5) {
		t.Fatalf("sort_order without sort_by must use seq DESC and id ASC tie-breaker, got %#v", items)
	}
}

func TestBondsHonorsListAppliesExactNumericFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				bondsHonorsEntity: {
					newBondsHonorRecord(1, 1, 10, 1, 2, "Match"),
					newBondsHonorRecord(2, 2, 10, 1, 3, "Wrong second unit"),
					newBondsHonorRecord(3, 3, 11, 1, 2, "Wrong group"),
					newBondsHonorRecord(4, 4, 10, 4, 2, "Wrong first unit"),
				},
			},
		},
	}
	router := newBondsHonorRouter(newReadyLookupHandler(cache))
	response := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/list?bonds_group_id=10&game_character_unit_id1=1&game_character_unit_id2=2")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	items, pagination := decodeBondsHonorList(t, response.Body.Bytes())
	if len(items) != 1 || items[0]["id"] != float64(1) || pagination["total"] != float64(1) {
		t.Fatalf("expected all exact filters to match only record 1, got items=%#v pagination=%#v", items, pagination)
	}
}

func TestBondsHonorsListSupportsEverySortableField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	first := newBondsHonorRecord(2, 10, 20, 5, 6, "Zeta")
	first["honorRarity"] = "low"
	second := newBondsHonorRecord(1, 20, 10, 1, 2, "Alpha")
	second["honorRarity"] = "high"
	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {bondsHonorsEntity: {first, second}},
		},
	}
	router := newBondsHonorRouter(newReadyLookupHandler(cache))
	tests := []struct {
		sortBy     string
		expectedID float64
	}{
		{sortBy: "id", expectedID: 1},
		{sortBy: "seq", expectedID: 2},
		{sortBy: "bondsGroupId", expectedID: 1},
		{sortBy: "gameCharacterUnitId1", expectedID: 1},
		{sortBy: "gameCharacterUnitId2", expectedID: 1},
		{sortBy: "honorRarity", expectedID: 1},
		{sortBy: "name", expectedID: 1},
	}

	for _, test := range tests {
		t.Run(test.sortBy, func(t *testing.T) {
			response := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/list?sort_by="+test.sortBy)
			if response.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
			}
			items, _ := decodeBondsHonorList(t, response.Body.Bytes())
			if len(items) != 2 || items[0]["id"] != test.expectedID {
				t.Fatalf("unexpected ascending order for %s: %#v", test.sortBy, items)
			}
		})
	}
}

func TestBondsHonorsByIDOmitsMissingFieldsAndPreservesEmptyLevels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {bondsHonorsEntity: {"9": {"id": int64(9), "levels": []any{}, "unknown": "hidden"}}},
		},
	}
	router := newBondsHonorRouter(newReadyLookupHandler(cache))
	response := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/9")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	item := decodeBondsHonorObject(t, response.Body.Bytes())
	if len(item) != 2 || item["id"] != float64(9) {
		t.Fatalf("missing fields and unknown fields must be omitted: %#v", item)
	}
	levels, ok := item["levels"].([]any)
	if !ok || len(levels) != 0 {
		t.Fatalf("an explicitly empty levels array must remain empty, got %#v", item["levels"])
	}
}

func TestBondsHonorsGameCharacterIDsFilterMatchesUnorderedUnderlyingCharacters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &bondsHonorTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {
					bondsHonorsEntity: {
						newBondsHonorRecord(1, 1, 70, 11, 12, "Forward"),
						newBondsHonorRecord(2, 2, 70, 13, 14, "Reverse"),
						newBondsHonorRecord(3, 3, 70, 15, 16, "Only one character"),
						newBondsHonorRecord(4, 4, 70, 17, 18, "Missing association"),
					},
					bondsHonorGameCharacterUnitsEntity: {
						newBondsHonorCharacterUnitRecord(11, 1, "unit-a"),
						newBondsHonorCharacterUnitRecord(12, 2, "unit-b"),
						newBondsHonorCharacterUnitRecord(13, 2, "unit-c"),
						newBondsHonorCharacterUnitRecord(14, 1, "unit-d"),
						newBondsHonorCharacterUnitRecord(15, 1, "unit-e"),
						newBondsHonorCharacterUnitRecord(16, 3, "unit-f"),
						newBondsHonorCharacterUnitRecord(17, 1, "unit-g"),
					},
				},
			},
		},
	}
	router := newBondsHonorRouter(newReadyBondsHonorTrackingHandler(cache))

	for _, query := range []string{"1,2", "2,1"} {
		response := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/list?game_character_ids="+query+"&page_size=100")
		if response.Code != http.StatusOK {
			t.Fatalf("expected 200 for game_character_ids=%s, got %d: %s", query, response.Code, response.Body.String())
		}
		items, pagination := decodeBondsHonorList(t, response.Body.Bytes())
		if len(items) != 2 || items[0]["id"] != float64(1) || items[1]["id"] != float64(2) || pagination["total"] != float64(2) {
			t.Fatalf("expected both unordered pair matches and no partial/missing matches for %s, got items=%#v pagination=%#v", query, items, pagination)
		}
		for _, item := range items {
			if item["characterUnit1"] == nil || item["characterUnit2"] == nil {
				t.Fatalf("expected current-page character unit enrichment to reuse the filter map: %#v", item)
			}
		}
	}
}

func TestBondsHonorsGameCharacterIDsCombinesWithExistingFiltersAndPaginatesAfterFiltering(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				bondsHonorsEntity: {
					newBondsHonorRecord(1, 1, 99, 101, 102, "Not a pair"),
					newBondsHonorRecord(2, 2, 99, 103, 104, "First match"),
					newBondsHonorRecord(3, 3, 99, 105, 106, "Second match"),
					newBondsHonorRecord(4, 4, 98, 107, 108, "Wrong group"),
				},
				bondsHonorGameCharacterUnitsEntity: {
					newBondsHonorCharacterUnitRecord(101, 9, "unit-101"),
					newBondsHonorCharacterUnitRecord(102, 10, "unit-102"),
					newBondsHonorCharacterUnitRecord(103, 1, "unit-103"),
					newBondsHonorCharacterUnitRecord(104, 2, "unit-104"),
					newBondsHonorCharacterUnitRecord(105, 2, "unit-105"),
					newBondsHonorCharacterUnitRecord(106, 1, "unit-106"),
					newBondsHonorCharacterUnitRecord(107, 1, "unit-107"),
					newBondsHonorCharacterUnitRecord(108, 2, "unit-108"),
				},
			},
		},
	}
	router := newBondsHonorRouter(newReadyLookupHandler(cache))

	combined := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/list?game_character_ids=1,2&bonds_group_id=99&game_character_unit_id1=103")
	if combined.Code != http.StatusOK {
		t.Fatalf("expected 200 for combined filters, got %d: %s", combined.Code, combined.Body.String())
	}
	items, pagination := decodeBondsHonorList(t, combined.Body.Bytes())
	if len(items) != 1 || items[0]["id"] != float64(2) || pagination["total"] != float64(1) {
		t.Fatalf("expected existing filters and game_character_ids to combine with AND, got items=%#v pagination=%#v", items, pagination)
	}

	paged := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/list?game_character_ids=1,2&bonds_group_id=99&page=2&page_size=1")
	if paged.Code != http.StatusOK {
		t.Fatalf("expected 200 for filtered page, got %d: %s", paged.Code, paged.Body.String())
	}
	items, pagination = decodeBondsHonorList(t, paged.Body.Bytes())
	if len(items) != 1 || items[0]["id"] != float64(3) || pagination["total"] != float64(2) {
		t.Fatalf("expected pagination after game-character filtering, got items=%#v pagination=%#v", items, pagination)
	}
}

func TestBondsHonorsGameCharacterIDsFilterLoadsUnitsOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &bondsHonorTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {
					bondsHonorsEntity: {
						newBondsHonorRecord(1, 1, 70, 11, 12, "Filtered"),
					},
					bondsHonorBondsEntity: {{"groupId": 70, "characterId1": 201, "characterId2": 202}},
					bondsHonorGameCharacterUnitsEntity: {
						newBondsHonorCharacterUnitRecord(11, 1, "unit-1"),
						newBondsHonorCharacterUnitRecord(12, 2, "unit-2"),
					},
				},
			},
		},
	}
	router := newBondsHonorRouter(newReadyBondsHonorTrackingHandler(cache))
	response := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/list?game_character_ids=1,2")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	counts := make(map[string]int)
	for _, call := range cache.listAllCalls {
		counts[call.entity]++
	}
	if counts[bondsHonorGameCharacterUnitsEntity] != 1 {
		t.Fatalf("expected exactly one gamecharacterunits ListAll call, got calls=%+v", cache.listAllCalls)
	}
	if counts[bondsHonorBondsEntity] > 1 {
		t.Fatalf("bonds enrichment must be loaded at most once, got calls=%+v", cache.listAllCalls)
	}
}

func TestBondsHonorsListRejectsUnknownAndMalformedParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newBondsHonorRouter(newReadyLookupHandler(&fakeLookupCache{}))
	tests := []struct {
		name string
		path string
	}{
		{name: "unknown query", path: "/api/v1/bondsHonors/jp/list?extra=1"},
		{name: "invalid page", path: "/api/v1/bondsHonors/jp/list?page=zero"},
		{name: "zero page", path: "/api/v1/bondsHonors/jp/list?page=0"},
		{name: "invalid page size", path: "/api/v1/bondsHonors/jp/list?page_size=invalid"},
		{name: "page size above maximum", path: "/api/v1/bondsHonors/jp/list?page_size=101"},
		{name: "empty sort field", path: "/api/v1/bondsHonors/jp/list?sort_by="},
		{name: "unsupported sort field", path: "/api/v1/bondsHonors/jp/list?sort_by=unknown"},
		{name: "invalid sort order", path: "/api/v1/bondsHonors/jp/list?sort_order=sideways"},
		{name: "malformed bonds group filter", path: "/api/v1/bondsHonors/jp/list?bonds_group_id=1.5"},
		{name: "nonpositive unit filter", path: "/api/v1/bondsHonors/jp/list?game_character_unit_id1=0"},
		{name: "list filter is not a scalar", path: "/api/v1/bondsHonors/jp/list?game_character_unit_id2=1,2"},
		{name: "duplicate parameter", path: "/api/v1/bondsHonors/jp/list?page=1&page=2"},
		{name: "game character ids empty", path: "/api/v1/bondsHonors/jp/list?game_character_ids="},
		{name: "game character ids missing value", path: "/api/v1/bondsHonors/jp/list?game_character_ids"},
		{name: "game character ids one item", path: "/api/v1/bondsHonors/jp/list?game_character_ids=1"},
		{name: "game character ids leading missing item", path: "/api/v1/bondsHonors/jp/list?game_character_ids=,2"},
		{name: "game character ids trailing missing item", path: "/api/v1/bondsHonors/jp/list?game_character_ids=1,"},
		{name: "game character ids internal missing item", path: "/api/v1/bondsHonors/jp/list?game_character_ids=1,,2"},
		{name: "game character ids too many items", path: "/api/v1/bondsHonors/jp/list?game_character_ids=1,2,3"},
		{name: "game character ids duplicate items", path: "/api/v1/bondsHonors/jp/list?game_character_ids=1,1"},
		{name: "game character ids zero", path: "/api/v1/bondsHonors/jp/list?game_character_ids=0,2"},
		{name: "game character ids negative", path: "/api/v1/bondsHonors/jp/list?game_character_ids=-1,2"},
		{name: "game character ids nonnumeric", path: "/api/v1/bondsHonors/jp/list?game_character_ids=one,2"},
		{name: "game character ids duplicate query key", path: "/api/v1/bondsHonors/jp/list?game_character_ids=1,2&game_character_ids=2,3"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := serveLookupRequest(t, router, http.MethodGet, test.path)
			if response.Code != http.StatusBadRequest || bondsHonorErrorCode(t, response.Body.Bytes()) != "INVALID_REQUEST" {
				t.Fatalf("expected 400 INVALID_REQUEST, got %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestBondsHonorsByIDValidatesIDAndReturnsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {bondsHonorsEntity: {"7": newBondsHonorRecord(7, 1, 77, 17, 18, "By ID")}},
		},
		listByEntity: map[string]map[string][]map[string]any{"jp": {}},
	}
	router := newBondsHonorRouter(newReadyLookupHandler(cache))

	for _, id := range []string{"0", "-1", "not-a-number"} {
		response := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/"+id)
		if response.Code != http.StatusBadRequest || bondsHonorErrorCode(t, response.Body.Bytes()) != "INVALID_REQUEST" {
			t.Fatalf("expected invalid id %q to return 400, got %d: %s", id, response.Code, response.Body.String())
		}
	}

	missing := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/999")
	if missing.Code != http.StatusNotFound || bondsHonorErrorCode(t, missing.Body.Bytes()) != bondsHonorNotFoundCode {
		t.Fatalf("expected BONDS_HONOR_NOT_FOUND, got %d: %s", missing.Code, missing.Body.String())
	}

	unknownQuery := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/7?extra=1")
	if unknownQuery.Code != http.StatusBadRequest {
		t.Fatalf("expected by-id query parameters to be rejected, got %d: %s", unknownQuery.Code, unknownQuery.Body.String())
	}
}

func TestBondsHonorsEnrichmentIsSameRegionAndMissingAssociationsAreOmitted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jpRecord := newBondsHonorRecord(1, 1, 77, 11, 12, "JP")
	enRecord := newBondsHonorRecord(1, 1, 77, 11, 12, "EN")
	cache := &bondsHonorTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			byID: map[string]map[string]map[string]map[string]any{
				"jp": {bondsHonorsEntity: {"1": jpRecord}},
				"en": {bondsHonorsEntity: {"1": enRecord}},
			},
			listByEntity: map[string]map[string][]map[string]any{
				"en": {
					bondsHonorBondsEntity: {{"id": 999, "groupId": 77, "characterId1": 1, "characterId2": 2}},
					bondsHonorGameCharacterUnitsEntity: {
						{"id": 11, "gameCharacterId": 101, "unit": "leo_need"},
						{"id": 12, "gameCharacterId": 102, "unit": "more_more_jump"},
					},
				},
			},
		},
	}
	router := newBondsHonorRouter(newReadyBondsHonorTrackingHandler(cache))

	jpResponse := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/1")
	if jpResponse.Code != http.StatusOK {
		t.Fatalf("expected JP by-id 200, got %d: %s", jpResponse.Code, jpResponse.Body.String())
	}
	jpItem := decodeBondsHonorObject(t, jpResponse.Body.Bytes())
	if jpItem["id"] != float64(1) || jpItem["name"] != "JP" {
		t.Fatalf("missing associations must not remove the base record: %#v", jpItem)
	}
	for _, association := range []string{"bondsGroup", "characterUnit1", "characterUnit2"} {
		if _, exists := jpItem[association]; exists {
			t.Fatalf("cross-region association %s must not be used: %#v", association, jpItem)
		}
	}

	enResponse := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/en/1")
	if enResponse.Code != http.StatusOK {
		t.Fatalf("expected EN by-id 200, got %d: %s", enResponse.Code, enResponse.Body.String())
	}
	enItem := decodeBondsHonorObject(t, enResponse.Body.Bytes())
	if enItem["bondsGroup"] == nil || enItem["characterUnit1"] == nil || enItem["characterUnit2"] == nil {
		t.Fatalf("same-region associations were not included: %#v", enItem)
	}
	for _, call := range cache.listAllCalls[:2] {
		if call.region != "jp" {
			t.Fatalf("JP request read related records from another region: %+v", call)
		}
	}
}

func TestBondsHonorsListLoadsEachRelationOnceAndOnlyEnrichesCurrentPage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	records := []map[string]any{
		newBondsHonorRecord(1, 1, 70, 11, 12, "First"),
		newBondsHonorRecord(2, 2, 70, 11, 12, "Second"),
		newBondsHonorRecord(3, 3, 70, 11, 12, "Outside page"),
	}
	cache := &bondsHonorTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {
					bondsHonorsEntity:     records,
					bondsHonorBondsEntity: {{"id": 999, "groupId": 70, "characterId1": 201, "characterId2": 202}},
					bondsHonorGameCharacterUnitsEntity: {
						{"id": 11, "gameCharacterId": 1011, "unit": "unit-1"},
						{"id": 12, "gameCharacterId": 1012, "unit": "unit-2"},
					},
				},
			},
		},
	}
	router := newBondsHonorRouter(newReadyBondsHonorTrackingHandler(cache))
	response := serveLookupRequest(t, router, http.MethodGet, "/api/v1/bondsHonors/jp/list?page_size=2")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	items, _ := decodeBondsHonorList(t, response.Body.Bytes())
	if len(items) != 2 || items[0]["id"] != float64(1) || items[1]["id"] != float64(2) {
		t.Fatalf("expected only the current page, got %#v", items)
	}
	if len(cache.listAllCalls) != 4 {
		t.Fatalf("expected one list call each for the base and three relation entities, got %+v", cache.listAllCalls)
	}
	counts := make(map[string]int)
	for _, call := range cache.listAllCalls {
		if call.region != "jp" {
			t.Fatalf("unexpected cross-region list call: %+v", call)
		}
		counts[call.entity]++
	}
	if counts[bondsHonorsEntity] != 1 || counts[bondsHonorBondsEntity] != 1 || counts[bondsHonorWordsEntity] != 1 || counts[bondsHonorGameCharacterUnitsEntity] != 1 {
		t.Fatalf("each entity must be loaded once, got counts=%v calls=%+v", counts, cache.listAllCalls)
	}
	if len(cache.byIDCalls) != 0 || cache.searchCalls != 0 {
		t.Fatalf("list enrichment must not use per-item GetByID or Search, byID=%+v searchCalls=%d", cache.byIDCalls, cache.searchCalls)
	}
}

func TestBondsHonorsStorageErrorsUseQueryErrorCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	baseRecord := newBondsHonorRecord(7, 1, 77, 17, 18, "Storage failure")

	baseListCache := &bondsHonorTrackingCache{
		fakeLookupCache: &fakeLookupCache{},
		listAllErrors:   map[string]error{bondsHonorsEntity: errors.New("base list unavailable")},
	}
	baseListResponse := serveLookupRequest(t, newBondsHonorRouter(newReadyBondsHonorTrackingHandler(baseListCache)), http.MethodGet, "/api/v1/bondsHonors/jp/list")
	if baseListResponse.Code != http.StatusInternalServerError || bondsHonorErrorCode(t, baseListResponse.Body.Bytes()) != bondsHonorQueryErrorCode {
		t.Fatalf("expected list storage error code %s, got %d: %s", bondsHonorQueryErrorCode, baseListResponse.Code, baseListResponse.Body.String())
	}

	baseByIDCache := &fakeLookupCache{byIDErr: errors.New("base record unavailable")}
	baseByIDResponse := serveLookupRequest(t, newBondsHonorRouter(newReadyLookupHandler(baseByIDCache)), http.MethodGet, "/api/v1/bondsHonors/jp/7")
	if baseByIDResponse.Code != http.StatusInternalServerError || bondsHonorErrorCode(t, baseByIDResponse.Body.Bytes()) != bondsHonorQueryErrorCode {
		t.Fatalf("expected by-id storage error code %s, got %d: %s", bondsHonorQueryErrorCode, baseByIDResponse.Code, baseByIDResponse.Body.String())
	}

	relationCache := &bondsHonorTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			byID: map[string]map[string]map[string]map[string]any{
				"jp": {bondsHonorsEntity: {"7": baseRecord}},
			},
		},
		listAllErrors: map[string]error{bondsHonorBondsEntity: errors.New("bonds unavailable")},
	}
	relationResponse := serveLookupRequest(t, newBondsHonorRouter(newReadyBondsHonorTrackingHandler(relationCache)), http.MethodGet, "/api/v1/bondsHonors/jp/7")
	if relationResponse.Code != http.StatusInternalServerError || bondsHonorErrorCode(t, relationResponse.Body.Bytes()) != bondsHonorQueryErrorCode {
		t.Fatalf("expected relationship storage error code %s, got %d: %s", bondsHonorQueryErrorCode, relationResponse.Code, relationResponse.Body.String())
	}
}

func TestBondsHonorsWordsStorageErrorsUseQueryErrorCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	baseRecord := newBondsHonorRecord(7, 1, 77, 17, 18, "Words storage failure")
	listCache := &bondsHonorTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {bondsHonorsEntity: {baseRecord}},
			},
		},
		listAllErrors: map[string]error{bondsHonorWordsEntity: errors.New("words unavailable")},
	}
	listResponse := serveLookupRequest(
		t,
		newBondsHonorRouter(newReadyBondsHonorTrackingHandler(listCache)),
		http.MethodGet,
		"/api/v1/bondsHonors/jp/list",
	)
	if listResponse.Code != http.StatusInternalServerError || bondsHonorErrorCode(t, listResponse.Body.Bytes()) != bondsHonorQueryErrorCode {
		t.Fatalf("expected list words error code %s, got %d: %s", bondsHonorQueryErrorCode, listResponse.Code, listResponse.Body.String())
	}

	byIDCache := &bondsHonorTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			byID: map[string]map[string]map[string]map[string]any{
				"jp": {bondsHonorsEntity: {"7": baseRecord}},
			},
		},
		listAllErrors: map[string]error{bondsHonorWordsEntity: errors.New("words unavailable")},
	}
	byIDResponse := serveLookupRequest(
		t,
		newBondsHonorRouter(newReadyBondsHonorTrackingHandler(byIDCache)),
		http.MethodGet,
		"/api/v1/bondsHonors/jp/7",
	)
	if byIDResponse.Code != http.StatusInternalServerError || bondsHonorErrorCode(t, byIDResponse.Body.Bytes()) != bondsHonorQueryErrorCode {
		t.Fatalf("expected by-ID words error code %s, got %d: %s", bondsHonorQueryErrorCode, byIDResponse.Code, byIDResponse.Body.String())
	}
}

func TestBondsHonorsReadinessUsesBondsHonorsEntity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeLookupCache{hasIndexSet: true, hasIndex: false}
	router := newBondsHonorRouter(newReadyLookupHandler(cache))
	for _, path := range []string{
		"/api/v1/bondsHonors/jp/list",
		"/api/v1/bondsHonors/jp/1",
	} {
		response := serveLookupRequest(t, router, http.MethodGet, path)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 for unready bondsHonors data at %s, got %d: %s", path, response.Code, response.Body.String())
		}
	}
}
