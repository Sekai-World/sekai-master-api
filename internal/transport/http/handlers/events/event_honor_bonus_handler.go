package events

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
)

const (
	eventHonorBonusesEntity            = "eventhonorbonuses"
	eventHonorBonusHonorsEntity        = "honors"
	eventHonorBonusHonorGroupsEntity   = "honorgroups"
	eventHonorBonusGameCharacterEntity = "gamecharacterunits"
)

// HonorBonusesByID godoc
// @Summary Get typed event honor bonuses by event id
// @Tags events
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Event ID"
// @Success 200 {object} shared.EventHonorBonusListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /events/{region}/{id}/honor-bonuses [get]
func (handler *EventHandler) HonorBonusesByID(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	id := strings.TrimSpace(c.Param("id"))
	if region == "" || id == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region and id are required")
		return
	}
	if len(c.Request.URL.Query()) > 0 {
		for name := range c.Request.URL.Query() {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "unsupported query parameter: "+name)
			return
		}
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, "events") {
		return
	}
	if !handler.ensureEventExists(c, region, id) {
		return
	}

	items, err := handler.buildEventHonorBonusResponses(c.Request.Context(), region, id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "EVENT_QUERY_ERROR", "failed to query event honor bonuses")
		return
	}

	response.JSON(c, http.StatusOK, shared.EventHonorBonusListResponse{Items: items})
}

func (handler *EventHandler) buildEventHonorBonusResponses(
	ctx context.Context,
	region string,
	eventID string,
) ([]shared.EventHonorBonusObjectResponse, error) {
	records, err := handler.masterDataSync.ListAll(ctx, region, eventHonorBonusesEntity)
	if err != nil {
		return nil, err
	}

	targetEventID := shared.NormalizeAnyID(eventID)
	items := make([]shared.EventHonorBonusObjectResponse, 0, len(records))
	for _, record := range records {
		if shared.NormalizeAnyID(record["eventId"]) != targetEventID {
			continue
		}
		items = append(items, projectEventHonorBonus(record))
	}

	sort.SliceStable(items, func(i, j int) bool {
		return eventHonorBonusIDLess(items[i].ID, items[j].ID)
	})
	if len(items) == 0 {
		return items, nil
	}

	if err := handler.enrichEventHonorBonusResponses(ctx, region, items); err != nil {
		return nil, err
	}

	return items, nil
}

func (handler *EventHandler) enrichEventHonorBonusResponses(
	ctx context.Context,
	region string,
	items []shared.EventHonorBonusObjectResponse,
) error {
	honorRecords, err := handler.masterDataSync.ListAll(ctx, region, eventHonorBonusHonorsEntity)
	if err != nil {
		return err
	}

	honorsByID := indexEventHonorBonusHonors(honorRecords)
	groupIDs := enrichEventHonorBonusHonors(items, honorsByID)

	groupsByID, err := loadEventHonorBonusGroups(
		ctx,
		handler.masterDataSync,
		region,
		groupIDs,
	)
	if err != nil {
		return err
	}
	enrichEventHonorBonusHonorGroups(items, groupsByID)

	leaderIDs := eventHonorBonusLeaderGameCharacterIDs(items)
	unitsByID, err := loadEventHonorBonusGameCharacterUnits(
		ctx,
		handler.masterDataSync,
		region,
		leaderIDs,
	)
	if err != nil {
		return err
	}
	enrichEventHonorBonusLeaderGameCharacterUnits(items, unitsByID)

	return nil
}

func indexEventHonorBonusHonors(records []map[string]any) map[string]map[string]any {
	honorsByID := make(map[string]map[string]any, len(records))
	for _, record := range records {
		id := shared.NormalizeAnyID(record["id"])
		if id == "" {
			continue
		}
		honorsByID[id] = record
	}

	return honorsByID
}

func enrichEventHonorBonusHonors(
	items []shared.EventHonorBonusObjectResponse,
	honorsByID map[string]map[string]any,
) map[string]struct{} {
	groupIDs := make(map[string]struct{})
	for index := range items {
		item := &items[index]
		if item.HonorID == nil {
			continue
		}

		honorRecord, found := honorsByID[strconv.FormatInt(*item.HonorID, 10)]
		if !found {
			continue
		}

		honor := projectEventHonorBonusHonor(honorRecord)
		item.Honor = &honor
		if honor.GroupID != nil {
			groupIDs[strconv.FormatInt(*honor.GroupID, 10)] = struct{}{}
		}
	}

	return groupIDs
}

func enrichEventHonorBonusHonorGroups(
	items []shared.EventHonorBonusObjectResponse,
	groupsByID map[string]shared.EventHonorBonusHonorGroupResponse,
) {
	for index := range items {
		honor := items[index].Honor
		if honor == nil || honor.GroupID == nil {
			continue
		}

		group, found := groupsByID[strconv.FormatInt(*honor.GroupID, 10)]
		if !found {
			continue
		}
		honor.Group = &group
	}
}

func eventHonorBonusLeaderGameCharacterIDs(items []shared.EventHonorBonusObjectResponse) map[string]struct{} {
	leaderIDs := make(map[string]struct{})
	for _, item := range items {
		if item.LeaderGameCharacterID == nil {
			continue
		}
		leaderIDs[strconv.FormatInt(*item.LeaderGameCharacterID, 10)] = struct{}{}
	}

	return leaderIDs
}

func enrichEventHonorBonusLeaderGameCharacterUnits(
	items []shared.EventHonorBonusObjectResponse,
	unitsByID map[string]shared.EventHonorBonusLeaderGameCharacterUnitResponse,
) {
	for index := range items {
		item := &items[index]
		if item.LeaderGameCharacterID == nil {
			continue
		}

		unit, found := unitsByID[strconv.FormatInt(*item.LeaderGameCharacterID, 10)]
		if !found {
			continue
		}
		item.LeaderGameCharacterUnit = &unit
	}
}

func loadEventHonorBonusGroups(
	ctx context.Context,
	masterDataSync interface {
		ListAll(context.Context, string, string) ([]map[string]any, error)
	},
	region string,
	groupIDs map[string]struct{},
) (map[string]shared.EventHonorBonusHonorGroupResponse, error) {
	groupsByID := make(map[string]shared.EventHonorBonusHonorGroupResponse)
	if len(groupIDs) == 0 {
		return groupsByID, nil
	}

	records, err := masterDataSync.ListAll(ctx, region, eventHonorBonusHonorGroupsEntity)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		id := shared.NormalizeAnyID(record["id"])
		if _, needed := groupIDs[id]; needed {
			groupsByID[id] = projectEventHonorBonusHonorGroup(record)
		}
	}

	return groupsByID, nil
}

func loadEventHonorBonusGameCharacterUnits(
	ctx context.Context,
	masterDataSync interface {
		ListAll(context.Context, string, string) ([]map[string]any, error)
	},
	region string,
	unitIDs map[string]struct{},
) (map[string]shared.EventHonorBonusLeaderGameCharacterUnitResponse, error) {
	unitsByID := make(map[string]shared.EventHonorBonusLeaderGameCharacterUnitResponse)
	if len(unitIDs) == 0 {
		return unitsByID, nil
	}

	records, err := masterDataSync.ListAll(ctx, region, eventHonorBonusGameCharacterEntity)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		id := shared.NormalizeAnyID(record["id"])
		if _, needed := unitIDs[id]; needed {
			unitsByID[id] = projectEventHonorBonusLeaderGameCharacterUnit(record)
		}
	}

	return unitsByID, nil
}

func projectEventHonorBonus(record map[string]any) shared.EventHonorBonusObjectResponse {
	return shared.EventHonorBonusObjectResponse{
		ID:                    eventHonorBonusOptionalInt64(record["id"]),
		EventID:               eventHonorBonusOptionalInt64(record["eventId"]),
		HonorID:               eventHonorBonusOptionalInt64(record["honorId"]),
		LeaderGameCharacterID: eventHonorBonusOptionalInt64(record["leaderGameCharacterId"]),
		BonusRate:             eventHonorBonusOptionalInt64(record["bonusRate"]),
	}
}

func projectEventHonorBonusHonor(record map[string]any) shared.EventHonorBonusHonorResponse {
	return shared.EventHonorBonusHonorResponse{
		ID:               eventHonorBonusOptionalInt64(record["id"]),
		GroupID:          eventHonorBonusOptionalInt64(record["groupId"]),
		Name:             eventHonorBonusOptionalString(record["name"]),
		HonorRarity:      eventHonorBonusOptionalString(record["honorRarity"]),
		HonorMissionType: eventHonorBonusOptionalString(record["honorMissionType"]),
		HonorTypeID:      eventHonorBonusOptionalInt64(record["honorTypeId"]),
		AssetbundleName:  eventHonorBonusOptionalString(record["assetbundleName"]),
	}
}

func projectEventHonorBonusHonorGroup(record map[string]any) shared.EventHonorBonusHonorGroupResponse {
	return shared.EventHonorBonusHonorGroupResponse{
		ID:                        eventHonorBonusOptionalInt64(record["id"]),
		Name:                      eventHonorBonusOptionalString(record["name"]),
		HonorType:                 eventHonorBonusOptionalString(record["honorType"]),
		BackgroundAssetbundleName: eventHonorBonusOptionalString(record["backgroundAssetbundleName"]),
		FrameName:                 eventHonorBonusOptionalString(record["frameName"]),
	}
}

func projectEventHonorBonusLeaderGameCharacterUnit(record map[string]any) shared.EventHonorBonusLeaderGameCharacterUnitResponse {
	return shared.EventHonorBonusLeaderGameCharacterUnitResponse{
		ID:              eventHonorBonusOptionalInt64(record["id"]),
		GameCharacterID: eventHonorBonusOptionalInt64(record["gameCharacterId"]),
		Unit:            eventHonorBonusOptionalString(record["unit"]),
	}
}

func eventHonorBonusIDLess(left, right *int64) bool {
	if left == nil {
		return false
	}
	if right == nil {
		return true
	}

	return *left < *right
}

func eventHonorBonusOptionalInt64(value any) *int64 {
	parsed, ok := eventHonorBonusInt64(value)
	if !ok {
		return nil
	}

	return &parsed
}

func eventHonorBonusInt64(value any) (int64, bool) {
	switch number := value.(type) {
	case int:
		return int64(number), true
	case int32:
		return int64(number), true
	case int64:
		return number, true
	case float32:
		return eventHonorBonusFloat64Int64(float64(number))
	case float64:
		return eventHonorBonusFloat64Int64(number)
	case json.Number:
		parsed, err := strconv.ParseInt(number.String(), 10, 64)
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(number), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func eventHonorBonusFloat64Int64(number float64) (int64, bool) {
	if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number {
		return 0, false
	}
	if number < float64(math.MinInt64) || number >= float64(math.MaxInt64) {
		return 0, false
	}

	return int64(number), true
}

func eventHonorBonusOptionalString(value any) *string {
	text, ok := value.(string)
	if !ok {
		return nil
	}

	return &text
}
