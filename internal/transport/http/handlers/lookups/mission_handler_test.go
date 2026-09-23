package lookups

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/usecase"
)

type missionListCall struct {
	region string
	entity string
}

type missionListVariantCase struct {
	name   string
	path   string
	family string
	id     float64
}

type missionTrackingCache struct {
	*fakeLookupCache
	listCalls  []missionListCall
	listErrors map[string]error
}

func (cache *missionTrackingCache) ListAll(ctx context.Context, region string, entity string) ([]map[string]any, error) {
	cache.listCalls = append(cache.listCalls, missionListCall{region: region, entity: entity})
	if err, ok := cache.listErrors[entity]; ok {
		return nil, err
	}
	return cache.fakeLookupCache.ListAll(ctx, region, entity)
}

func newMissionTestHandler(cache *missionTrackingCache) *LookupHandler {
	regions := []string{"jp", "en", "tw", "kr", "cn"}
	sources := make([]masterdata.Source, 0, len(regions))
	statuses := make([]masterdata.SyncStatus, 0, len(regions))
	for _, region := range regions {
		sources = append(sources, masterdata.Source{Region: region})
		statuses = append(statuses, masterdata.SyncStatus{Region: region, Status: "success"})
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(
		sources,
		nil,
		cache,
		&fakeLookupStatusStore{statuses: statuses},
		nil,
		1,
	)
	return NewLookupHandler(syncUsecase)
}

func newMissionTestRouter(handler *LookupHandler) *gin.Engine {
	router := gin.New()
	router.GET("/api/v1/missions/:region/list", handler.MissionsList)
	router.GET("/api/v1/missions/:region/:family/:id", handler.MissionByID)
	router.GET("/api/v1/missions/:region/:family/", handler.MissionByID)
	router.GET("/api/v1/characterMissionV2ParameterGroups/:region/:id/levels", handler.CharacterMissionV2ParameterGroupLevels)
	router.GET("/api/v1/characterRanks/:region/list", handler.CharacterRanksList)
	return router
}

func decodeMissionList(t *testing.T, responseBody []byte) (items []map[string]any, pagination map[string]any) {
	t.Helper()

	var body struct {
		Items      []map[string]any `json:"items"`
		Pagination map[string]any   `json:"pagination"`
	}
	if err := json.Unmarshal(responseBody, &body); err != nil {
		t.Fatalf("decode mission list: %v", err)
	}
	return body.Items, body.Pagination
}

func TestMissionsRejectUnknownFamiliesFiltersAndInvalidIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &missionTrackingCache{fakeLookupCache: &fakeLookupCache{}}
	router := newMissionTestRouter(newMissionTestHandler(cache))

	tests := []struct {
		name string
		path string
	}{
		{name: "missing list family", path: "/api/v1/missions/jp/list"},
		{name: "unknown list family", path: "/api/v1/missions/jp/list?family=cards"},
		{name: "resource entity list family", path: "/api/v1/missions/jp/list?family=resourceboxes"},
		{name: "unknown path family", path: "/api/v1/missions/jp/cards/1"},
		{name: "zero id", path: "/api/v1/missions/jp/normalMissions/0"},
		{name: "non numeric id", path: "/api/v1/missions/jp/normalMissions/not-an-id"},
		{name: "empty id", path: "/api/v1/missions/jp/normalMissions/"},
		{name: "character filter on normal missions", path: "/api/v1/missions/jp/list?family=normalMissions&character_id=1"},
		{name: "unknown filter", path: "/api/v1/missions/jp/list?family=normalMissions&purpose=reward"},
		{name: "unknown query on by id", path: "/api/v1/missions/jp/normalMissions/1?purpose=reward"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			response := serveLookupRequest(t, router, http.MethodGet, testCase.path)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func newFiveRegionMissionTestCache() *missionTrackingCache {
	return &missionTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {
					normalMissionsEntity: {
						{
							"id":                1,
							"seq":               20,
							"normalMissionType": "play_live",
							"requirement":       10,
							"sentence":          "JP mission",
							"rewards": []any{map[string]any{
								"id":            101,
								"missionType":   "normal_mission",
								"missionId":     1,
								"seq":           1,
								"resourceBoxId": 10,
								"unknownReward": "hidden",
							}},
							"unknownMission": true,
						},
					},
					resourceBoxesEntity: {
						{
							"id":                 10,
							"resourceBoxPurpose": "normal_mission",
							"resourceBoxType":    "item",
							"details": []any{
								map[string]any{"seq": 2, "resourceType": "coin", "resourceQuantity": 20, "unknown": true},
								map[string]any{"seq": 1, "resourceType": "jewel", "resourceQuantity": 10},
							},
						},
					},
				},
				"en": {
					normalMissionsEntity: {
						{
							"id":                2,
							"seq":               30,
							"normalMissionType": "read_story",
							"sentence":          "EN mission",
							"rewards": []any{map[string]any{
								"id": 102,
								"resourceBox": map[string]any{
									"id":                 20,
									"resourceBoxPurpose": "normal_mission",
									"resourceBoxType":    "item",
									"details":            []any{map[string]any{"seq": 1, "resourceType": "jewel"}},
								},
							}},
						},
					},
					resourceBoxesEntity: {
						{"id": 20, "resourceBoxPurpose": "normal_mission"},
					},
				},
				"tw": {
					normalMissionsEntity: {
						{
							"id":                3,
							"seq":               40,
							"normalMissionType": "clear_live",
							"sentence":          "TW mission",
							"rewards":           []any{[]any{float64(30)}},
						},
					},
					resourceBoxesEntity: {
						{"id": 30, "resourceBoxPurpose": "normal_mission"},
						{"id": 30, "resourceBoxPurpose": "event_reward"},
					},
				},
				"kr": {
					characterMissionV2sEntity: {
						{
							"id":                    4,
							"characterId":           1,
							"parameterGroupId":      2,
							"characterMissionType":  "play_live",
							"sentence":              "KR mission",
							"progressSentence":      "{progress} times",
							"isAchievementMission":  true,
							"unknownCharacterField": "hidden",
						},
						{"id": 6, "characterId": 1, "sentence": "KR mission without reward"},
					},
				},
				"cn": {
					storyMissionsEntity: {
						{"id": 5, "requirement": 50, "resourceBoxId": 50, "unknownStoryField": "hidden"},
					},
					resourceBoxesEntity: {
						{"id": 50, "resourceBoxPurpose": "story_mission", "resourceBoxType": "item"},
					},
					resourceBoxDetailsEntity: {
						{"resourceBoxId": 50, "resourceBoxPurpose": "story_mission", "seq": 2, "resourceType": "coin"},
						{"resourceBoxId": 50, "resourceBoxPurpose": "story_mission", "seq": 1, "resourceType": "jewel"},
					},
				},
			},
		},
	}
}

func assertMissionListVariant(t *testing.T, router *gin.Engine, testCase missionListVariantCase) {
	t.Helper()
	response := serveLookupRequest(t, router, http.MethodGet, testCase.path)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	items, _ := decodeMissionList(t, response.Body.Bytes())
	if len(items) != 1 || items[0]["family"] != testCase.family || items[0]["id"] != testCase.id {
		t.Fatalf("unexpected mission item: %#v", items)
	}
	if _, leaked := items[0]["unknownMission"]; leaked {
		t.Fatalf("unknown mission field leaked: %#v", items[0])
	}
}

func assertResolvedJPMissionReward(t *testing.T, router *gin.Engine) {
	t.Helper()
	jpResponse := serveLookupRequest(t, router, http.MethodGet, "/api/v1/missions/jp/list?family=normalMissions")
	jpItems, _ := decodeMissionList(t, jpResponse.Body.Bytes())
	jpReward := jpItems[0]["rewards"].([]any)[0].(map[string]any)
	if jpReward["status"] != missionRewardResolvedStatus || jpReward["resourceBox"].(map[string]any)["id"] != float64(10) {
		t.Fatalf("expected resolved JP reward, got %#v", jpReward)
	}
	jpDetails := jpReward["resourceBox"].(map[string]any)["details"].([]any)
	if jpDetails[0].(map[string]any)["seq"] != float64(1) || jpDetails[1].(map[string]any)["seq"] != float64(2) {
		t.Fatalf("expected embedded details sorted by seq, got %#v", jpDetails)
	}
	if _, leaked := jpDetails[0].(map[string]any)["unknown"]; leaked {
		t.Fatalf("unknown reward detail field leaked: %#v", jpDetails[0])
	}
}

func assertUnresolvedTWMissionReward(t *testing.T, router *gin.Engine) {
	t.Helper()
	twResponse := serveLookupRequest(t, router, http.MethodGet, "/api/v1/missions/tw/list?family=normalMissions")
	twItems, _ := decodeMissionList(t, twResponse.Body.Bytes())
	twReward := twItems[0]["rewards"].([]any)[0].(map[string]any)
	if twReward["status"] != missionRewardUnresolvedStatus || twReward["resourceBoxId"] != float64(30) {
		t.Fatalf("expected ambiguous TW reward to remain unresolved, got %#v", twReward)
	}
	if _, expanded := twReward["resourceBox"]; expanded {
		t.Fatalf("ambiguous TW reward must not be expanded: %#v", twReward)
	}
}

func assertKRMissionWithoutRewards(t *testing.T, router *gin.Engine) {
	t.Helper()
	krResponse := serveLookupRequest(t, router, http.MethodGet, "/api/v1/missions/kr/list?family=characterMissionV2s&id=6")
	krItems, _ := decodeMissionList(t, krResponse.Body.Bytes())
	if _, present := krItems[0]["rewards"]; present {
		t.Fatalf("missing rewards must be omitted: %#v", krItems[0])
	}
}

func assertMissionRewardLookupCalls(t *testing.T, cache *missionTrackingCache) {
	t.Helper()
	for _, entity := range []string{resourceBoxesEntity, resourceBoxDetailsEntity} {
		calls := 0
		for _, call := range cache.listCalls {
			if call.entity == entity {
				calls++
			}
		}
		if calls != 5 {
			t.Fatalf("expected one batched %s ListAll call per reward-bearing region, got %d calls: %#v", entity, calls, cache.listCalls)
		}
	}
	if len(cache.byIDCalls) != 0 {
		t.Fatalf("mission lists must not use GetByID for rewards, got %#v", cache.byIDCalls)
	}
}

func TestMissionsProjectFiveRegionVariantsAndResolveRewardsSafely(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := newFiveRegionMissionTestCache()
	router := newMissionTestRouter(newMissionTestHandler(cache))

	tests := []missionListVariantCase{
		{name: "jp object reward", path: "/api/v1/missions/jp/list?family=normalMissions", family: normalMissionsFamily, id: 1},
		{name: "en embedded reward", path: "/api/v1/missions/en/list?family=normalMissions", family: normalMissionsFamily, id: 2},
		{name: "tw selectable reward remains unresolved", path: "/api/v1/missions/tw/list?family=normalMissions", family: normalMissionsFamily, id: 3},
		{name: "kr character variant", path: "/api/v1/missions/kr/list?family=characterMissionV2s&id=4", family: characterMissionV2sFamily, id: 4},
		{name: "cn story variant", path: "/api/v1/missions/cn/list?family=storyMissions", family: storyMissionsFamily, id: 5},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			assertMissionListVariant(t, router, testCase)
		})
	}

	assertResolvedJPMissionReward(t, router)
	assertUnresolvedTWMissionReward(t, router)
	assertKRMissionWithoutRewards(t, router)
	assertMissionRewardLookupCalls(t, cache)
}

func TestMissionsPaginationAndStableIDTieBreak(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &missionTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {
					normalMissionsEntity: {
						{"id": 3, "seq": 30, "sentence": "same"},
						{"id": 1, "seq": 10, "sentence": "same"},
						{"id": 2, "seq": 20, "sentence": "same"},
					},
				},
			},
		},
	}
	router := newMissionTestRouter(newMissionTestHandler(cache))

	first := serveLookupRequest(t, router, http.MethodGet, "/api/v1/missions/jp/list?family=normalMissions&page=1&page_size=2&sort_by=sentence")
	if first.Code != http.StatusOK {
		t.Fatalf("expected first page 200, got %d: %s", first.Code, first.Body.String())
	}
	firstItems, firstPagination := decodeMissionList(t, first.Body.Bytes())
	if firstItems[0]["id"] != float64(1) || firstItems[1]["id"] != float64(2) {
		t.Fatalf("expected id tie-break order [1,2], got %#v", firstItems)
	}
	if firstPagination["total"] != float64(3) || firstPagination["total_pages"] != float64(2) || firstPagination["has_next"] != true {
		t.Fatalf("unexpected first pagination: %#v", firstPagination)
	}

	second := serveLookupRequest(t, router, http.MethodGet, "/api/v1/missions/jp/list?family=normalMissions&page=2&page_size=2&sort_by=sentence")
	secondItems, secondPagination := decodeMissionList(t, second.Body.Bytes())
	if len(secondItems) != 1 || secondItems[0]["id"] != float64(3) || secondPagination["has_next"] != false {
		t.Fatalf("unexpected second page: items=%#v pagination=%#v", secondItems, secondPagination)
	}
}

func TestMissionByIDUsesOnlyMissionGetByIDAndReturnsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &missionTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			byID: map[string]map[string]map[string]map[string]any{
				"jp": {
					normalMissionsEntity: {
						"7": {
							"id":            7,
							"sentence":      "By ID",
							"resourceBoxId": 70,
						},
					},
				},
			},
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {
					resourceBoxesEntity:      {{"id": 70, "resourceBoxPurpose": "normal_mission"}},
					resourceBoxDetailsEntity: {{"resourceBoxId": 70, "resourceBoxPurpose": "normal_mission", "seq": 1, "resourceType": "jewel"}},
				},
			},
		},
	}
	router := newMissionTestRouter(newMissionTestHandler(cache))

	found := serveLookupRequest(t, router, http.MethodGet, "/api/v1/missions/jp/normalMissions/7")
	if found.Code != http.StatusOK {
		t.Fatalf("expected by-id 200, got %d: %s", found.Code, found.Body.String())
	}
	var item map[string]any
	if err := json.Unmarshal(found.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode by-id response: %v", err)
	}
	if item["id"] != float64(7) || item["family"] != normalMissionsFamily {
		t.Fatalf("unexpected by-id response: %#v", item)
	}
	if len(cache.byIDCalls) != 1 || cache.byIDCalls[0].entity != normalMissionsEntity {
		t.Fatalf("expected only mission entity GetByID, got %#v", cache.byIDCalls)
	}
	for _, call := range cache.byIDCalls {
		if call.entity == resourceBoxesEntity || call.entity == resourceBoxDetailsEntity {
			t.Fatalf("resource reward entity used GetByID: %#v", cache.byIDCalls)
		}
	}

	missing := serveLookupRequest(t, router, http.MethodGet, "/api/v1/missions/jp/normalMissions/999")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("expected missing mission 404, got %d: %s", missing.Code, missing.Body.String())
	}
}

func TestCharacterMissionParameterGroupsAreJoinedAndOrdered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &missionTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {
					characterMissionV2sEntity: {
						{"id": 1, "parameterGroupId": 7},
					},
					characterMissionV2ParameterGroupsEntity: {
						{"id": 7, "seq": 2, "requirement": 200, "exp": 20, "quantity": 2},
						{"id": 7, "seq": 1, "requirement": 100, "exp": 10},
						{"id": 8, "seq": 1, "requirement": 999},
					},
				},
			},
		},
	}
	router := newMissionTestRouter(newMissionTestHandler(cache))

	response := serveLookupRequest(t, router, http.MethodGet, "/api/v1/missions/jp/list?family=characterMissionV2s")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	items, _ := decodeMissionList(t, response.Body.Bytes())
	group, ok := items[0]["parameterGroup"].(map[string]any)
	if !ok || group["id"] != float64(7) {
		t.Fatalf("expected parameter group 7, got %#v", items[0]["parameterGroup"])
	}
	if group["totalLevels"] != float64(2) {
		t.Fatalf("expected two parameter levels, got %#v", group["totalLevels"])
	}
	previewLevels, ok := group["previewLevels"].([]any)
	if !ok || len(previewLevels) != 2 {
		t.Fatalf("expected two preview levels, got %#v", group["previewLevels"])
	}
	if previewLevels[0].(map[string]any)["seq"] != float64(1) || previewLevels[0].(map[string]any)["requirement"] != float64(100) {
		t.Fatalf("expected first preview level to be seq 1, got %#v", previewLevels[0])
	}
	if previewLevels[1].(map[string]any)["seq"] != float64(2) || previewLevels[1].(map[string]any)["requirement"] != float64(200) {
		t.Fatalf("expected second preview level to be seq 2, got %#v", previewLevels[1])
	}
	if _, exists := previewLevels[0].(map[string]any)["quantity"]; exists {
		t.Fatalf("missing quantity must remain omitted: %#v", previewLevels[0])
	}
	if _, exists := items[0]["requirement"]; exists {
		t.Fatalf("parameter group thresholds must not populate scalar requirement: %#v", items[0])
	}

	groupCalls := 0
	for _, call := range cache.listCalls {
		if call.entity == characterMissionV2ParameterGroupsEntity {
			groupCalls++
		}
	}
	if groupCalls != 1 {
		t.Fatalf("expected one parameter-group ListAll call, got %d: %#v", groupCalls, cache.listCalls)
	}
}

func TestCharacterMissionParameterGroupLevelsPaginatesAndReturnsMissingGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &missionTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {
					characterMissionV2sEntity: {
						{"id": 1, "parameterGroupId": 7, "characterMissionType": "play_live"},
					},
					characterMissionV2ParameterGroupsEntity: {
						{"id": 7, "seq": 3, "requirement": 300},
						{"id": 7, "seq": 1, "requirement": 100},
						{"id": 7, "seq": 2, "requirement": 200},
					},
				},
			},
		},
	}
	router := newMissionTestRouter(newMissionTestHandler(cache))

	response := serveLookupRequest(t, router, http.MethodGet, "/api/v1/characterMissionV2ParameterGroups/jp/7/levels?page=2&page_size=2")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Items      []map[string]any `json:"items"`
		Pagination map[string]any   `json:"pagination"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode parameter group levels: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0]["seq"] != float64(3) {
		t.Fatalf("expected page two to contain seq 3, got %#v", body.Items)
	}
	if body.Pagination["total"] != float64(3) || body.Pagination["has_next"] != false {
		t.Fatalf("unexpected pagination: %#v", body.Pagination)
	}

	missing := serveLookupRequest(t, router, http.MethodGet, "/api/v1/characterMissionV2ParameterGroups/jp/999/levels")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("expected missing group 404, got %d: %s", missing.Code, missing.Body.String())
	}
}

func TestCharacterRanksListFiltersCharacterAndResolvesOnlyCharacterRankRewardBoxes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &missionTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {
					characterRanksEntity: {
						{"id": 2, "characterId": 1, "characterRank": 2, "power1BonusRate": 0.2, "rewardResourceBoxIds": []any{1002}},
						{"id": 1, "characterId": 1, "characterRank": 1, "power1BonusRate": 0.1, "rewardResourceBoxIds": []any{1001}},
						{"id": 3, "characterId": 2, "characterRank": 1, "rewardResourceBoxIds": []any{1003}},
					},
					resourceBoxesEntity: {
						{"id": 1001, "resourceBoxPurpose": "character_rank_reward", "details": []any{map[string]any{"resourceType": "material", "resourceQuantity": 1}}},
						{"id": 1002, "resourceBoxPurpose": "event_ranking_reward", "details": []any{map[string]any{"resourceType": "jewel", "resourceQuantity": 999}}},
						{"id": 1002, "resourceBoxPurpose": "character_rank_reward", "details": []any{map[string]any{"resourceType": "jewel", "resourceQuantity": 2}}},
					},
				},
			},
		},
	}
	router := newMissionTestRouter(newMissionTestHandler(cache))

	response := serveLookupRequest(t, router, http.MethodGet, "/api/v1/characterRanks/jp/list?character_id=1")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode character ranks: %v", err)
	}
	if len(body.Items) != 2 || body.Items[0]["characterRank"] != float64(1) || body.Items[1]["characterRank"] != float64(2) {
		t.Fatalf("expected sorted character ranks, got %#v", body.Items)
	}
	boxes, ok := body.Items[1]["rewardResourceBoxes"].([]any)
	if !ok || len(boxes) != 1 {
		t.Fatalf("expected one character-rank reward box, got %#v", body.Items[1]["rewardResourceBoxes"])
	}
	box := boxes[0].(map[string]any)
	if box["resourceBoxPurpose"] != "character_rank_reward" {
		t.Fatalf("expected character_rank_reward box, got %#v", box)
	}

	invalid := serveLookupRequest(t, router, http.MethodGet, "/api/v1/characterRanks/jp/list")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("expected missing character ID 400, got %d: %s", invalid.Code, invalid.Body.String())
	}
}

func TestCharacterMissionMissingParameterGroupIsOmitted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &missionTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {
					characterMissionV2sEntity: {
						{"id": 1, "parameterGroupId": 7},
					},
					characterMissionV2ParameterGroupsEntity: {
						{"id": 8, "seq": 1, "requirement": 100},
					},
				},
			},
		},
	}
	router := newMissionTestRouter(newMissionTestHandler(cache))

	response := serveLookupRequest(t, router, http.MethodGet, "/api/v1/missions/jp/list?family=characterMissionV2s")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var item map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode mission response: %v", err)
	}
	if _, exists := item["parameterGroup"]; exists {
		t.Fatalf("missing parameter group must be omitted: %#v", item)
	}
}

func TestNonCharacterMissionDoesNotLoadParameterGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &missionTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {
					normalMissionsEntity: {{"id": 1}},
				},
			},
		},
		listErrors: map[string]error{
			characterMissionV2ParameterGroupsEntity: errors.New("parameter groups unavailable"),
		},
	}
	router := newMissionTestRouter(newMissionTestHandler(cache))

	response := serveLookupRequest(t, router, http.MethodGet, "/api/v1/missions/jp/list?family=normalMissions")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	for _, call := range cache.listCalls {
		if call.entity == characterMissionV2ParameterGroupsEntity {
			t.Fatalf("non-character mission loaded parameter groups: %#v", cache.listCalls)
		}
	}
}

func TestCharacterMissionParameterGroupErrorsUseMissionQueryError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &missionTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			byID: map[string]map[string]map[string]map[string]any{
				"jp": {
					characterMissionV2sEntity: {
						"1": {"id": 1, "parameterGroupId": 7},
					},
				},
			},
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {
					characterMissionV2sEntity: {{"id": 1, "parameterGroupId": 7}},
				},
			},
		},
		listErrors: map[string]error{
			characterMissionV2ParameterGroupsEntity: errors.New("parameter groups unavailable"),
		},
	}
	router := newMissionTestRouter(newMissionTestHandler(cache))

	tests := []struct {
		name   string
		target string
	}{
		{name: "list", target: "/api/v1/missions/jp/list?family=characterMissionV2s"},
		{name: "by id", target: "/api/v1/missions/jp/characterMissionV2s/1"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			response := serveLookupRequest(t, router, http.MethodGet, testCase.target)
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("expected 500 for %s, got %d: %s", testCase.target, response.Code, response.Body.String())
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error response for %s: %v", testCase.target, err)
			}
			if body.Error.Code != missionQueryErrorCode {
				t.Fatalf("expected %s for %s, got %s", missionQueryErrorCode, testCase.target, body.Error.Code)
			}
		})
	}
}
