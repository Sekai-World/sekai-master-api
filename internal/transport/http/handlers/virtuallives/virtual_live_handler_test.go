package virtuallives

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/usecase"
)

type fakeVirtualLiveHandlerCache struct {
	byID                  map[string]map[string]map[string]map[string]any
	listByEntity          map[string][]map[string]any
	listItems             []map[string]any
	listTotal             int
	hasRecords            map[string]map[string]bool
	hasIndex              bool
	hasIndexSet           bool
	searchMatches         []masterdata.SearchMatch
	searchMatchesByEntity map[string][]masterdata.SearchMatch
	searchCalls           []struct {
		region string
		entity string
		query  string
		fields []string
		limit  int
	}
}

type fakeVirtualLiveHandlerStatusStore struct {
	statuses []masterdata.SyncStatus
}

func newReadyVirtualLiveHandler(cache *fakeVirtualLiveHandlerCache) *VirtualLiveHandler {
	statusStore := &fakeVirtualLiveHandlerStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "jp", Status: "success"},
		},
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	return NewVirtualLiveHandler(syncUsecase)
}

func (store *fakeVirtualLiveHandlerStatusStore) Save(_ context.Context, _ masterdata.SyncStatus) error {
	return nil
}

func (store *fakeVirtualLiveHandlerStatusStore) List(_ context.Context) ([]masterdata.SyncStatus, error) {
	return store.statuses, nil
}

func (cache *fakeVirtualLiveHandlerCache) StoreRegion(_ context.Context, _ string, _ map[string]any) error {
	return nil
}

func (cache *fakeVirtualLiveHandlerCache) GetByID(_ context.Context, region string, entity string, id string) (map[string]any, bool, error) {
	regionData, ok := cache.byID[region]
	if !ok {
		return nil, false, nil
	}
	entityData, ok := regionData[entity]
	if !ok {
		return nil, false, nil
	}
	record, ok := entityData[id]
	if !ok {
		return nil, false, nil
	}
	return record, true, nil
}

func (cache *fakeVirtualLiveHandlerCache) ListAll(_ context.Context, _ string, entity string) ([]map[string]any, error) {
	source := cache.listItems
	if cache.listByEntity != nil {
		if entityItems, ok := cache.listByEntity[strings.ToLower(strings.TrimSpace(entity))]; ok {
			source = entityItems
		}
	}

	items := make([]map[string]any, 0, len(source))
	for _, item := range source {
		copied := make(map[string]any, len(item))
		for key, value := range item {
			copied[key] = value
		}
		items = append(items, copied)
	}
	return items, nil
}

func (cache *fakeVirtualLiveHandlerCache) ListByPage(_ context.Context, _, _ string, _, _ int) ([]map[string]any, int, error) {
	return cache.listItems, cache.listTotal, nil
}

func (cache *fakeVirtualLiveHandlerCache) Search(_ context.Context, region, entity, query string, fields []string, limit int) ([]masterdata.SearchMatch, error) {
	cache.searchCalls = append(cache.searchCalls, struct {
		region string
		entity string
		query  string
		fields []string
		limit  int
	}{
		region: region,
		entity: entity,
		query:  query,
		fields: append([]string{}, fields...),
		limit:  limit,
	})
	if cache.searchMatchesByEntity != nil {
		if matches, ok := cache.searchMatchesByEntity[entity]; ok {
			return matches, nil
		}
	}
	return cache.searchMatches, nil
}

func (cache *fakeVirtualLiveHandlerCache) HasEntityRecords(_ context.Context, region string, entity string) (bool, error) {
	if cache.hasRecords == nil {
		return false, nil
	}
	return cache.hasRecords[strings.ToLower(strings.TrimSpace(region))][strings.ToLower(strings.TrimSpace(entity))], nil
}

func (cache *fakeVirtualLiveHandlerCache) HasRegionIndex(_ string) bool {
	if !cache.hasIndexSet {
		return true
	}
	return cache.hasIndex
}

func TestVirtualLiveByIDEndpointReturnsVirtualLive(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"musicvocals": {
					"29": {
						"id":             29,
						"musicId":        27,
						"caption":        "VIRTUAL SINGER ver.",
						"musicVocalType": "original_song",
					},
				},
				"virtuallivegroups": {
					"77": {
						"id":   77,
						"name": "Group A",
					},
				},
				"virtuallives": {
					"501": {
						"id":                   501,
						"name":                 "after live",
						"assetbundleName":      "vl_501",
						"startAt":              1000,
						"endAt":                2000,
						"screenMvMusicVocalId": 29,
						"virtualLiveType":      "normal",
						"virtualLiveGroupId":   77,
						"virtualItems": []any{
							map[string]any{
								"id":                    1,
								"name":                  "Confetti",
								"assetbundleName":       "cracker_001",
								"effectAssetbundleName": "cracker_001",
								"effectExpressionType":  "throw_effect",
								"virtualItemCategory":   "normal",
								"virtualItemLabelType":  "normal",
								"priority":              10,
								"seq":                   10,
								"costJewel":             10,
								"costVirtualCoin":       30,
							},
						},
						"virtualLiveSchedules": []any{
							map[string]any{"id": 10, "startAt": 1000},
						},
						"virtualLiveSetlists": []any{
							map[string]any{"musicId": 1, "seq": 1},
						},
					},
				},
			},
		},
		listByEntity: map[string][]map[string]any{
			"virtuallivepamphlets": {
				{
					"id":              9001,
					"name":            "Pamphlet A",
					"assetbundleName": "pamphlet_501",
					"flavorText":      "hello",
					"virtualLiveId":   501,
				},
			},
			"virtuallivetickets": {
				{
					"id":                    9101,
					"name":                  "Ticket A",
					"assetbundleName":       "ticket_501",
					"flavorText":            "admit one",
					"virtualLiveTicketType": "normal",
					"virtualLiveId":         501,
				},
			},
		},
	}

	handler := newReadyVirtualLiveHandler(cache)

	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/:id", handler.ByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/501", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if body["id"] != float64(501) {
		t.Fatalf("expected id=501, got %v", body["id"])
	}
	if body["name"] != "after live" {
		t.Fatalf("expected name=after live, got %v", body["name"])
	}
	if _, exists := body["virtualItems"]; exists {
		t.Fatalf("expected virtualItems to be omitted from by-id response")
	}
	if _, exists := body["virtualLiveSchedules"]; exists {
		t.Fatalf("expected virtualLiveSchedules to be omitted from by-id response")
	}
	if _, exists := body["virtualLiveSetlists"]; exists {
		t.Fatalf("expected virtualLiveSetlists to be omitted from by-id response")
	}
	virtualLiveGroupRaw, ok := body["virtualLiveGroup"]
	if !ok {
		t.Fatalf("expected virtualLiveGroup in response")
	}
	virtualLiveGroup, ok := virtualLiveGroupRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected virtualLiveGroup object, got %T", virtualLiveGroupRaw)
	}
	if virtualLiveGroup["id"] != float64(77) {
		t.Fatalf("expected virtualLiveGroup.id=77, got %v", virtualLiveGroup["id"])
	}
	if _, exists := body["virtualLiveGroupId"]; exists {
		t.Fatalf("expected virtualLiveGroupId to be omitted from response")
	}
	screenMvMusicVocalRaw, ok := body["screenMvMusicVocal"]
	if !ok {
		t.Fatalf("expected screenMvMusicVocal in response")
	}
	screenMvMusicVocal, ok := screenMvMusicVocalRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected screenMvMusicVocal object, got %T", screenMvMusicVocalRaw)
	}
	if screenMvMusicVocal["id"] != float64(29) {
		t.Fatalf("expected screenMvMusicVocal.id=29, got %v", screenMvMusicVocal["id"])
	}
	if _, exists := body["screenMvMusicVocalId"]; exists {
		t.Fatalf("expected screenMvMusicVocalId to be omitted from response")
	}
	pamphletRaw, ok := body["pamphlet"]
	if !ok {
		t.Fatalf("expected pamphlet in response")
	}
	pamphlet, ok := pamphletRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected pamphlet object, got %T", pamphletRaw)
	}
	if pamphlet["id"] != float64(9001) {
		t.Fatalf("expected pamphlet.id=9001, got %v", pamphlet["id"])
	}
	if _, exists := pamphlet["virtualLiveId"]; exists {
		t.Fatalf("expected pamphlet.virtualLiveId to be omitted")
	}
	ticketRaw, ok := body["ticket"]
	if !ok {
		t.Fatalf("expected ticket in response")
	}
	ticket, ok := ticketRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected ticket object, got %T", ticketRaw)
	}
	if ticket["id"] != float64(9101) {
		t.Fatalf("expected ticket.id=9101, got %v", ticket["id"])
	}
	if _, exists := ticket["virtualLiveId"]; exists {
		t.Fatalf("expected ticket.virtualLiveId to be omitted")
	}
}

func TestVirtualLiveByIDExpandsVirtualLiveRewardResourceBox(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {
						"id":              501,
						"name":            "after live",
						"virtualLiveType": "normal",
						"virtualLiveRewards": []any{
							map[string]any{
								"id":              1,
								"resourceBoxId":   7001,
								"virtualLiveId":   501,
								"virtualLiveType": "normal",
							},
						},
					},
				},
				"resourceboxes": {
					"7001": {
						"id":                 7001,
						"resourceBoxPurpose": "virtual_live_reward",
						"resourceBoxType":    "material",
						"details": []any{
							map[string]any{
								"resourceType":     "honor",
								"resourceId":       8801,
								"resourceLevel":    1,
								"resourceQuantity": 1,
								"seq":              1,
							},
						},
					},
				},
				"honors": {
					"8801": {
						"id":          8801,
						"groupId":     9901,
						"honorRarity": "rarity_normal",
						"honorType":   "type_normal",
						"name":        "Test Honor",
						"levels":      []any{},
					},
				},
				"honorgroups": {
					"9901": {
						"id":   9901,
						"name": "Test Honor Group",
					},
				},
			},
		},
		listByEntity: map[string][]map[string]any{
			"resourceboxes": {
				{
					"id":                 7001,
					"resourceBoxPurpose": "virtual_live_reward",
					"resourceBoxType":    "material",
					"details": []any{
						map[string]any{
							"resourceType":     "honor",
							"resourceId":       8801,
							"resourceLevel":    1,
							"resourceQuantity": 1,
							"seq":              1,
						},
					},
				},
			},
		},
	}

	handler := newReadyVirtualLiveHandler(cache)
	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/:id", handler.ByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/501", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	rewardsRaw, ok := body["virtualLiveRewards"]
	if !ok {
		t.Fatalf("expected virtualLiveRewards in response")
	}
	rewards, ok := rewardsRaw.([]any)
	if !ok || len(rewards) != 1 {
		t.Fatalf("expected 1 reward, got %T len=%d", rewardsRaw, len(rewards))
	}
	reward, ok := rewards[0].(map[string]any)
	if !ok {
		t.Fatalf("expected reward object, got %T", rewards[0])
	}
	boxRaw, ok := reward["resourceBox"]
	if !ok || boxRaw == nil {
		t.Fatalf("expected resourceBox to be expanded for virtual_live_reward purpose")
	}
	box, ok := boxRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected resourceBox object, got %T", boxRaw)
	}
	if box["resourceBoxPurpose"] != "virtual_live_reward" {
		t.Fatalf("expected resourceBoxPurpose=virtual_live_reward, got %v", box["resourceBoxPurpose"])
	}
	detailsRaw, ok := box["details"]
	if !ok {
		t.Fatalf("expected details in resourceBox")
	}
	details, ok := detailsRaw.([]any)
	if !ok || len(details) != 1 {
		t.Fatalf("expected 1 detail, got %T len=%d", detailsRaw, len(details))
	}
	detail, ok := details[0].(map[string]any)
	if !ok {
		t.Fatalf("expected detail object, got %T", details[0])
	}
	if detail["resourceType"] != "honor" {
		t.Fatalf("expected detail.resourceType=honor, got %v", detail["resourceType"])
	}
	if detail["resourceId"] != float64(8801) {
		t.Fatalf("expected detail.resourceId=8801, got %v", detail["resourceId"])
	}
	if detail["resourceLevel"] != float64(1) {
		t.Fatalf("expected detail.resourceLevel=1, got %v", detail["resourceLevel"])
	}
	if detail["resourceQuantity"] != float64(1) {
		t.Fatalf("expected detail.resourceQuantity=1, got %v", detail["resourceQuantity"])
	}
	if detail["seq"] != float64(1) {
		t.Fatalf("expected detail.seq=1, got %v", detail["seq"])
	}
	honorRaw, ok := detail["honor"]
	if !ok || honorRaw == nil {
		t.Fatalf("expected honor to be expanded for honor detail")
	}
	honor, ok := honorRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected honor object, got %T", honorRaw)
	}
	if honor["id"] != float64(8801) {
		t.Fatalf("expected honor.id=8801, got %v", honor["id"])
	}
	if honor["name"] != "Test Honor" {
		t.Fatalf("expected honor.name=Test Honor, got %v", honor["name"])
	}
}

func TestVirtualLiveByIDSameIDWrongPurposeDoesNotExpandResourceBox(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {
						"id":              501,
						"name":            "after live",
						"virtualLiveType": "normal",
						"virtualLiveRewards": []any{
							map[string]any{
								"id":              1,
								"resourceBoxId":   7001,
								"virtualLiveId":   501,
								"virtualLiveType": "normal",
							},
						},
					},
				},
				"resourceboxes": {
					"7001": {
						"id":                 7001,
						"resourceBoxPurpose": "event_ranking_reward",
						"resourceBoxType":    "material",
						"details": []any{
							map[string]any{
								"resourceType":     "honor",
								"resourceId":       8801,
								"resourceLevel":    1,
								"resourceQuantity": 1,
								"seq":              1,
							},
						},
					},
				},
			},
		},
		listByEntity: map[string][]map[string]any{
			"resourceboxes": {
				{
					"id":                 7001,
					"resourceBoxPurpose": "event_ranking_reward",
					"resourceBoxType":    "material",
					"details": []any{
						map[string]any{
							"resourceType":     "honor",
							"resourceId":       8801,
							"resourceLevel":    1,
							"resourceQuantity": 1,
							"seq":              1,
						},
					},
				},
			},
		},
	}

	handler := newReadyVirtualLiveHandler(cache)
	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/:id", handler.ByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/501", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	rewards := body["virtualLiveRewards"].([]any)
	reward := rewards[0].(map[string]any)
	if _, ok := reward["resourceBox"]; ok {
		t.Fatalf("expected resourceBox to be absent for wrong-purpose box, got %v", reward["resourceBox"])
	}
}

func TestVirtualLiveByIDMissingResourceBoxStaysSparse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {
						"id":              501,
						"name":            "after live",
						"virtualLiveType": "normal",
						"virtualLiveRewards": []any{
							map[string]any{
								"id":              1,
								"resourceBoxId":   7001,
								"virtualLiveId":   501,
								"virtualLiveType": "normal",
							},
						},
					},
				},
			},
		},
	}

	handler := newReadyVirtualLiveHandler(cache)
	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/:id", handler.ByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/501", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	rewards := body["virtualLiveRewards"].([]any)
	reward := rewards[0].(map[string]any)
	if _, ok := reward["resourceBox"]; ok {
		t.Fatalf("expected resourceBox to be absent when box missing, got %v", reward["resourceBox"])
	}
}

func TestVirtualLiveByIDSameIDMultiplePurposesSelectsVirtualLiveReward(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// resourceboxes IDs are NOT unique across resourceBoxPurpose. The reward
	// references resourceBoxId=1, which exists for both event_ranking_reward and
	// virtual_live_reward. The resolver must select the virtual_live_reward one
	// (jewel x300), never the colliding event_ranking_reward record.
	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {
						"id":              501,
						"name":            "after live",
						"virtualLiveType": "normal",
						"virtualLiveRewards": []any{
							map[string]any{
								"id":              1,
								"resourceBoxId":   1,
								"virtualLiveId":   501,
								"virtualLiveType": "normal",
							},
						},
					},
				},
			},
		},
		listByEntity: map[string][]map[string]any{
			"resourceboxes": {
				{
					"id":                 1,
					"resourceBoxPurpose": "event_ranking_reward",
					"resourceBoxType":    "material",
					"details": []any{
						map[string]any{
							"resourceType":     "jewel",
							"resourceId":       0,
							"resourceLevel":    0,
							"resourceQuantity": 999,
							"seq":              1,
						},
					},
				},
				{
					"id":                 1,
					"resourceBoxPurpose": "virtual_live_reward",
					"resourceBoxType":    "material",
					"details": []any{
						map[string]any{
							"resourceType":     "jewel",
							"resourceId":       0,
							"resourceLevel":    0,
							"resourceQuantity": 300,
							"seq":              1,
						},
					},
				},
			},
		},
	}

	handler := newReadyVirtualLiveHandler(cache)
	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/:id", handler.ByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/501", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	rewards := body["virtualLiveRewards"].([]any)
	if len(rewards) != 1 {
		t.Fatalf("expected 1 reward, got %d", len(rewards))
	}
	reward := rewards[0].(map[string]any)
	boxRaw, ok := reward["resourceBox"]
	if !ok || boxRaw == nil {
		t.Fatalf("expected resourceBox to be expanded for virtual_live_reward purpose")
	}
	box := boxRaw.(map[string]any)
	if box["resourceBoxPurpose"] != "virtual_live_reward" {
		t.Fatalf("expected resourceBoxPurpose=virtual_live_reward, got %v", box["resourceBoxPurpose"])
	}
	details := box["details"].([]any)
	if len(details) != 1 {
		t.Fatalf("expected 1 detail, got %d", len(details))
	}
	detail := details[0].(map[string]any)
	if detail["resourceType"] != "jewel" {
		t.Fatalf("expected detail.resourceType=jewel, got %v", detail["resourceType"])
	}
	if int(detail["resourceQuantity"].(float64)) != 300 {
		t.Fatalf("expected detail.resourceQuantity=300 (virtual_live_reward box), got %v", detail["resourceQuantity"])
	}
}

func TestVirtualLiveItemsByIDEndpointReturnsItems(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {
						"id":   501,
						"name": "after live",
						"virtualItems": []any{
							map[string]any{
								"id":                    1,
								"name":                  "Confetti",
								"assetbundleName":       "cracker_001",
								"effectAssetbundleName": "cracker_001",
								"effectExpressionType":  "throw_effect",
								"virtualItemCategory":   "normal",
								"virtualItemLabelType":  "normal",
								"priority":              10,
								"seq":                   10,
								"costJewel":             10,
								"costVirtualCoin":       30,
							},
							map[string]any{
								"id":                    2,
								"name":                  "Balloon",
								"assetbundleName":       "baloon_001",
								"effectAssetbundleName": "baloon_001",
								"effectExpressionType":  "throw_effect",
								"virtualItemCategory":   "normal",
								"virtualItemLabelType":  "normal",
								"priority":              20,
								"seq":                   20,
								"costJewel":             30,
								"costVirtualCoin":       90,
							},
						},
					},
				},
			},
		},
	}

	handler := newReadyVirtualLiveHandler(cache)

	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/:id/items", handler.ItemsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/501/items", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	itemsRaw, ok := body["items"]
	if !ok {
		t.Fatalf("expected items in response")
	}

	items, ok := itemsRaw.([]any)
	if !ok {
		t.Fatalf("expected items array, got %T", itemsRaw)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestVirtualLiveSchedulesByIDEndpointReturnsItems(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {
						"id":   501,
						"name": "after live",
						"virtualLiveSchedules": []any{
							map[string]any{"id": 10, "startAt": 1000},
							map[string]any{"id": 11, "startAt": 2000},
						},
					},
				},
			},
		},
	}

	handler := newReadyVirtualLiveHandler(cache)

	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/:id/schedules", handler.SchedulesByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/501/schedules", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	itemsRaw, ok := body["items"]
	if !ok {
		t.Fatalf("expected items in response")
	}

	items, ok := itemsRaw.([]any)
	if !ok {
		t.Fatalf("expected items array, got %T", itemsRaw)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestVirtualLiveSetlistsByIDEndpointReturnsItems(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {
						"id":   501,
						"name": "after live",
						"virtualLiveSetlists": []any{
							map[string]any{"musicId": 1, "seq": 1},
							map[string]any{"musicId": 2, "seq": 2},
						},
					},
				},
			},
		},
	}

	handler := newReadyVirtualLiveHandler(cache)

	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/:id/setlists", handler.SetlistsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/501/setlists", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	itemsRaw, ok := body["items"]
	if !ok {
		t.Fatalf("expected items in response")
	}

	items, ok := itemsRaw.([]any)
	if !ok {
		t.Fatalf("expected items array, got %T", itemsRaw)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestVirtualLiveAvailableRegionsByIDEndpointReturnsReadyRegionsWithData(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {"id": 501},
				},
			},
			"en": {
				"virtuallives": {
					"501": {"id": 501},
				},
			},
			"tw": {
				"virtuallives": {
					"501": {"id": 501},
				},
			},
		},
	}

	statusStore := &fakeVirtualLiveHandlerStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "tw", Status: "running"},
			{Region: "en", Status: "success"},
			{Region: "jp", Status: "success"},
		},
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	handler := NewVirtualLiveHandler(syncUsecase)

	router := gin.New()
	router.GET("/api/v1/virtualLives/regions/:id/availability", handler.AvailableRegionsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/regions/501/availability", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	var body struct {
		Regions []string `json:"regions"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	expected := []string{"en", "jp"}
	if !reflect.DeepEqual(body.Regions, expected) {
		t.Fatalf("expected regions %v, got %v", expected, body.Regions)
	}
}

func TestVirtualLiveAvailabilityEndpointRequiresRuntimeIndexWhenOnlyEntityRecordsExist(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {"id": 501, "name": "persisted-virtual-live"},
				},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {"virtuallives": true},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}

	handler := newReadyVirtualLiveHandler(cache)
	router := gin.New()
	router.GET("/api/v1/virtualLives/regions/:id/availability", handler.AvailableRegionsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/regions/501/availability", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		Regions []string `json:"regions"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(body.Regions) != 0 {
		t.Fatalf("expected no available regions without runtime index, got %v", body.Regions)
	}
}

func TestVirtualLiveListEndpointReturnsItems(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		listItems: []map[string]any{
			{
				"id":              501,
				"name":            "after live",
				"assetbundleName": "vl_501",
				"virtualLiveType": "normal",
				"virtualItems": []any{
					map[string]any{
						"id":                    1,
						"name":                  "Confetti",
						"assetbundleName":       "cracker_001",
						"effectAssetbundleName": "cracker_001",
						"effectExpressionType":  "throw_effect",
						"virtualItemCategory":   "normal",
						"virtualItemLabelType":  "normal",
						"priority":              10,
						"seq":                   10,
						"costJewel":             10,
						"costVirtualCoin":       30,
					},
				},
				"virtualLiveSchedules": []any{
					map[string]any{"id": 10, "startAt": 1000},
				},
				"virtualLiveSetlists": []any{
					map[string]any{"musicId": 1, "seq": 1},
				},
			},
		},
		listTotal: 1,
	}

	handler := newReadyVirtualLiveHandler(cache)

	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/list", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/list?page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	itemsRaw, ok := body["items"]
	if !ok {
		t.Fatalf("expected items in response")
	}

	items, ok := itemsRaw.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected 1 item, got %T len=%d", itemsRaw, len(items))
	}

	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected item object, got %T", items[0])
	}

	if first["name"] != "after live" {
		t.Fatalf("expected first.name=after live, got %v", first["name"])
	}
	if _, exists := first["virtualItems"]; exists {
		t.Fatalf("expected virtualItems to be omitted from list response")
	}
	if _, exists := first["virtualLiveSchedules"]; exists {
		t.Fatalf("expected virtualLiveSchedules to be omitted from list response")
	}
	if _, exists := first["virtualLiveSetlists"]; exists {
		t.Fatalf("expected virtualLiveSetlists to be omitted from list response")
	}
	if virtualLiveGroup, exists := first["virtualLiveGroup"]; exists && virtualLiveGroup != nil {
		t.Fatalf("expected virtualLiveGroup to be absent or null when not found, got %v", first["virtualLiveGroup"])
	}
	if screenMvMusicVocal, exists := first["screenMvMusicVocal"]; exists && screenMvMusicVocal != nil {
		t.Fatalf("expected screenMvMusicVocal to be absent or null when not found, got %v", first["screenMvMusicVocal"])
	}
	if pamphlet, exists := first["pamphlet"]; !exists || pamphlet != nil {
		t.Fatalf("expected pamphlet to be null when not found, got %v", first["pamphlet"])
	}
	if ticket, exists := first["ticket"]; !exists || ticket != nil {
		t.Fatalf("expected ticket to be null when not found, got %v", first["ticket"])
	}
}

func TestVirtualLivePersistedRecordsReturnDataWhenIndexMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {
						"id":                   501,
						"name":                 "persisted-after-restart",
						"assetbundleName":      "vl_501",
						"startAt":              1000,
						"endAt":                2000,
						"virtualLiveType":      "normal",
						"virtualLiveGroupId":   77,
						"screenMvMusicVocalId": 29,
						"virtualItems": []any{
							map[string]any{
								"id":                    1,
								"name":                  "Confetti",
								"assetbundleName":       "cracker_001",
								"effectAssetbundleName": "cracker_001",
								"effectExpressionType":  "throw_effect",
								"virtualItemCategory":   "normal",
								"virtualItemLabelType":  "normal",
								"priority":              10,
								"seq":                   10,
								"costJewel":             10,
								"costVirtualCoin":       30,
							},
						},
						"virtualLiveSchedules": []any{
							map[string]any{"id": 10, "startAt": 1000},
						},
						"virtualLiveSetlists": []any{
							map[string]any{"musicId": 1, "seq": 1},
						},
					},
				},
			},
		},
		listItems: []map[string]any{
			{
				"id":              501,
				"name":            "persisted-after-restart",
				"assetbundleName": "vl_501",
				"virtualLiveType": "normal",
				"virtualItems": []any{
					map[string]any{
						"id":                    1,
						"name":                  "Confetti",
						"assetbundleName":       "cracker_001",
						"effectAssetbundleName": "cracker_001",
						"effectExpressionType":  "throw_effect",
						"virtualItemCategory":   "normal",
						"virtualItemLabelType":  "normal",
						"priority":              10,
						"seq":                   10,
						"costJewel":             10,
						"costVirtualCoin":       30,
					},
				},
				"virtualLiveSchedules": []any{
					map[string]any{"id": 10, "startAt": 1000},
				},
				"virtualLiveSetlists": []any{
					map[string]any{"musicId": 1, "seq": 1},
				},
			},
		},
		listTotal: 1,
		hasRecords: map[string]map[string]bool{
			"jp": {"virtuallives": true},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}

	handler := newReadyVirtualLiveHandler(cache)
	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/:id", handler.ByID)
	router.GET("/api/v1/virtualLives/:region/:id/items", handler.ItemsByID)
	router.GET("/api/v1/virtualLives/:region/:id/schedules", handler.SchedulesByID)
	router.GET("/api/v1/virtualLives/:region/:id/setlists", handler.SetlistsByID)
	router.GET("/api/v1/virtualLives/:region/list", handler.List)

	testCases := []struct {
		name string
		path string
	}{
		{name: "by id", path: "/api/v1/virtualLives/jp/501"},
		{name: "items", path: "/api/v1/virtualLives/jp/501/items"},
		{name: "schedules", path: "/api/v1/virtualLives/jp/501/schedules"},
		{name: "setlists", path: "/api/v1/virtualLives/jp/501/setlists"},
		{name: "list", path: "/api/v1/virtualLives/jp/list?page=1&page_size=20"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
			}
		})
	}
}

func virtualLiveListFixture() []map[string]any {
	return []map[string]any{
		{
			"id":              501,
			"name":            "After Live Concert",
			"assetbundleName": "vl_501",
			"virtualLiveType": "normal",
		},
		{
			"id":              502,
			"name":            "Beginner Live Show",
			"assetbundleName": "vl_502",
			"virtualLiveType": "beginner",
		},
		{
			"id":              503,
			"name":            "Cheerful Carnival",
			"assetbundleName": "vl_503",
			"virtualLiveType": "cheerful_carnival",
		},
	}
}

func TestVirtualLiveListFilterByNameMatchesNormalizedSubstring(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		listItems: virtualLiveListFixture(),
		listTotal: 3,
	}

	handler := newReadyVirtualLiveHandler(cache)
	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/list", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/list?name=after%20live&page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	items := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 item matching name filter, got %d", len(items))
	}
	item := items[0].(map[string]any)
	if item["id"] != float64(501) {
		t.Fatalf("expected matched id=501, got %v", item["id"])
	}
}

func TestVirtualLiveListFilterByExactID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		listItems: virtualLiveListFixture(),
		listTotal: 3,
	}

	handler := newReadyVirtualLiveHandler(cache)
	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/list", handler.List)

	// id is parsed as a numeric value, so zero-padded (+501, 0501) and plain
	// (502) forms must match the underlying record id exactly.
	cases := []struct {
		name   string
		query  string
		wantID float64
	}{
		{"plain", "id=502", 502},
		{"zero-padded", "id=0501", 501},
		{"explicit-plus", "id=%2B501", 501},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/list?"+tc.query+"&page=1&page_size=20", nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
			}

			var body map[string]any
			if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}
			items := body["items"].([]any)
			if len(items) != 1 || items[0].(map[string]any)["id"] != tc.wantID {
				t.Fatalf("expected exactly id=%v, got %v", tc.wantID, body["items"])
			}
		})
	}
}

func TestVirtualLiveListFilterByTypeUsesORWithinParameterAndANDWithOthers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		listItems: virtualLiveListFixture(),
		listTotal: 3,
	}

	handler := newReadyVirtualLiveHandler(cache)
	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/list", handler.List)

	// Comma-separated OR within the type parameter; trims and de-duplicates.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/list?virtual_live_type=normal,%20beginner%20,normal&page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	items := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 items from OR'd types, got %d", len(items))
	}
	for _, it := range items {
		typ := it.(map[string]any)["virtualLiveType"]
		if typ != "normal" && typ != "beginner" {
			t.Fatalf("unexpected type in OR result: %v", typ)
		}
	}
}

func TestVirtualLiveListFilterByUnknownTypeReturnsNoMatches(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		listItems: virtualLiveListFixture(),
		listTotal: 3,
	}

	handler := newReadyVirtualLiveHandler(cache)
	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/list", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/list?virtual_live_type=does_not_exist&page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	items := body["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("expected 0 items for unknown type, got %d", len(items))
	}
}

func TestVirtualLiveListFilterRejectsNonNumericID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		listItems: virtualLiveListFixture(),
		listTotal: 3,
	}

	handler := newReadyVirtualLiveHandler(cache)
	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/list", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/list?id=abc&page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for non-numeric id, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestVirtualLiveListCombinesFiltersWithAND(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		listItems: virtualLiveListFixture(),
		listTotal: 3,
	}

	handler := newReadyVirtualLiveHandler(cache)
	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/list", handler.List)

	// name "live" matches all three; type filter restricts to beginner only.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/list?name=live&virtual_live_type=beginner&page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	items := body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != float64(502) {
		t.Fatalf("expected exactly id=502 from combined filters, got %v", body["items"])
	}
}

func TestVirtualLiveListDefaultBranchPreservedWhenNoNewFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		listItems: virtualLiveListFixture(),
		listTotal: 3,
	}

	handler := newReadyVirtualLiveHandler(cache)
	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/list", handler.List)

	// No name/id/type filter; spoiler=true (default) and no sort => index-page branch.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/list?page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	items := body["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("expected default branch to return all 3 items, got %d", len(items))
	}
	pagination := body["pagination"].(map[string]any)
	if int(pagination["total"].(float64)) != 3 {
		t.Fatalf("expected pagination.total=3, got %v", pagination["total"])
	}
}

func TestVirtualLiveItemsEndpointPassesThroughRawPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Item includes a sentinel field NOT modeled by VirtualLiveItem DTO plus a
	// zero value, proving the endpoint forwards raw payloads unchanged.
	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {
						"id":   501,
						"name": "after live",
						"virtualItems": []any{
							map[string]any{
								"id":                    1,
								"name":                  "Confetti",
								"assetbundleName":       "cracker_001",
								"effectAssetbundleName": "cracker_001",
								"effectExpressionType":  "throw_effect",
								"virtualItemCategory":   "normal",
								"virtualItemLabelType":  "normal",
								"priority":              10,
								"seq":                   10,
								"costJewel":             10,
								"costVirtualCoin":       30,
								"cheerPoint":            5,
								"unit":                  "",
								"unmodeledSentinel":     "keep-me",
							},
						},
					},
				},
			},
		},
	}

	handler := newReadyVirtualLiveHandler(cache)
	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/:id/items", handler.ItemsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/501/items", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(body.Items))
	}
	item := body.Items[0]
	if item["name"] != "Confetti" {
		t.Fatalf("expected name=Confetti, got %v", item["name"])
	}
	if int(item["cheerPoint"].(float64)) != 5 {
		t.Fatalf("expected cheerPoint=5, got %v", item["cheerPoint"])
	}
	if item["unmodeledSentinel"] != "keep-me" {
		t.Fatalf("expected passthrough of unmodeled sentinel field, got %v", item["unmodeledSentinel"])
	}
	if item["unit"] != "" {
		t.Fatalf("expected zero-value unit to be preserved, got %v", item["unit"])
	}
}
