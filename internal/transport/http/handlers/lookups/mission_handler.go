package lookups

import (
	"cmp"
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
)

const (
	storyMissionsFamily                     = "storyMissions"
	characterMissionV2sFamily               = "characterMissionV2s"
	normalMissionsFamily                    = "normalMissions"
	storyMissionsEntity                     = "storymissions"
	characterMissionV2sEntity               = "charactermissionv2s"
	characterMissionV2ParameterGroupsEntity = "charactermissionv2parametergroups"
	characterMissionV2ExJsonsEntity         = "charactermissionv2exjsons"
	characterRanksEntity                    = "characterranks"
	normalMissionsEntity                    = "normalmissions"
	resourceBoxesEntity                     = "resourceboxes"
	resourceBoxDetailsEntity                = "resourceboxdetails"
	missionQueryErrorCode                   = "MISSION_QUERY_ERROR"
	missionNotFoundCode                     = "MISSION_NOT_FOUND"
	missionParameterGroupNotFoundCode       = "MISSION_PARAMETER_GROUP_NOT_FOUND"
	missionFamilyAllowlistError             = "family must be one of: storyMissions, characterMissionV2s, normalMissions"
	missionRewardResolvedStatus             = "resolved"
	missionRewardUnresolvedStatus           = "unresolved"
)

type missionFamilyConfig struct {
	family         string
	entity         string
	sortableFields []string
	filterable     map[string]string
}

var missionFamilyConfigs = map[string]missionFamilyConfig{
	storyMissionsFamily: {
		family:         storyMissionsFamily,
		entity:         storyMissionsEntity,
		sortableFields: []string{"id", "seq", "sentence", "progressSentence", "requirement", "eventId", "resourceBoxId", "storyMissionType"},
		filterable:     map[string]string{"id": "id", "event_id": "eventId"},
	},
	characterMissionV2sFamily: {
		family:         characterMissionV2sFamily,
		entity:         characterMissionV2sEntity,
		sortableFields: []string{"id", "seq", "sentence", "progressSentence", "requirement", "characterId", "parameterGroupId", "eventId", "resourceBoxId", "characterMissionType", "isAchievementMission"},
		filterable:     map[string]string{"id": "id", "character_id": "characterId", "event_id": "eventId"},
	},
	normalMissionsFamily: {
		family:         normalMissionsFamily,
		entity:         normalMissionsEntity,
		sortableFields: []string{"id", "seq", "sentence", "progressSentence", "requirement", "eventId", "resourceBoxId", "normalMissionType"},
		filterable:     map[string]string{"id": "id", "event_id": "eventId"},
	},
}

type missionItem struct {
	response       shared.MissionResponse
	rewardRefs     []missionRewardReference
	rewardsPresent bool
}

type missionRewardReference struct {
	response    shared.MissionRewardResponse
	embeddedBox map[string]any
}

type missionResourceBoxCandidate struct {
	record  map[string]any
	id      int64
	purpose string
}

type missionRewardCatalog struct {
	boxesByID    map[int64][]missionResourceBoxCandidate
	detailsByBox map[string][]map[string]any
}

type missionRewardResolutionRequest struct {
	region string
	items  []missionItem
}

type missionOperands[T any] struct {
	left  T
	right T
}

type missionRewardCatalogRequest struct {
	boxes   []map[string]any
	details []map[string]any
}

type missionParameterGroupCatalog map[int64][]shared.MissionParameterGroupLevelResponse

// MissionsList godoc
// @Summary List missions by family and page
// @Tags missions
// @Produce json
// @Param region path string true "Region"
// @Param family query string true "Mission family" Enums(storyMissions,characterMissionV2s,normalMissions)
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param id query string false "Comma-separated positive mission IDs"
// @Param character_id query string false "Comma-separated positive character IDs (characterMissionV2s only)"
// @Param event_id query string false "Comma-separated positive event IDs"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.MissionListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Router /missions/{region}/list [get]
func (handler *LookupHandler) MissionsList(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	if !validateMissionQueryParameters(c, true) {
		return
	}
	config, ok := missionFamilyFromQuery(c)
	if !ok {
		return
	}
	region, ok := parseTypedLookupRegion(c, handler.masterDataSync)
	if !ok {
		return
	}
	page, pageSize, ok := parseLookupPagination(c)
	if !ok {
		return
	}
	filters, ok := parseMissionFilters(c, config)
	if !ok {
		return
	}
	sortOptions, ok := shared.ParseListSortOptions(c)
	if !ok {
		return
	}
	if sortOptions.Enabled && !shared.ValidateSortField(c, sortOptions.Field, nil, config.sortableFields) {
		return
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, config.entity) {
		return
	}

	records, err := handler.masterDataSync.ListAll(c.Request.Context(), region, config.entity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, missionQueryErrorCode, "failed to list missions")
		return
	}

	items := normalizeMissionRecords(config.family, records)
	items = filterMissionItems(items, filters)
	if sortOptions.Enabled {
		sortMissionItems(items, sortOptions.Field, sortOptions.Descending)
	}

	total := len(items)
	pagedItems := paginateMissionItems(items, page, pageSize)
	rewardRequest := missionRewardResolutionRequest{region: region, items: pagedItems}
	if err := handler.resolveMissionRewards(c.Request.Context(), rewardRequest); err != nil {
		response.Error(c, http.StatusInternalServerError, missionQueryErrorCode, "failed to resolve mission rewards")
		return
	}

	itemsResponse := make([]shared.MissionResponse, 0, len(pagedItems))
	if err := handler.resolveMissionParameterGroups(c.Request.Context(), region, config.family, pagedItems); err != nil {
		response.Error(c, http.StatusInternalServerError, missionQueryErrorCode, "failed to resolve mission parameter groups")
		return
	}
	for _, item := range pagedItems {
		itemsResponse = append(itemsResponse, item.response)
	}

	response.JSON(c, http.StatusOK, shared.MissionListResponse{
		Items:      itemsResponse,
		Pagination: lookupPaginationResponse(page, pageSize, total),
	})
}

// MissionByID godoc
// @Summary Get a mission by family and id
// @Tags missions
// @Produce json
// @Param region path string true "Region"
// @Param family path string true "Mission family" Enums(storyMissions,characterMissionV2s,normalMissions)
// @Param id path int true "Mission ID" minimum(1)
// @Success 200 {object} shared.MissionResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Router /missions/{region}/{family}/{id} [get]
func (handler *LookupHandler) MissionByID(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	if !validateMissionQueryParameters(c, false) {
		return
	}
	config, ok := missionFamilyFromPath(c)
	if !ok {
		return
	}
	region, ok := parseTypedLookupRegion(c, handler.masterDataSync)
	if !ok {
		return
	}
	id, ok := parseTypedLookupID(c)
	if !ok {
		return
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, config.entity) {
		return
	}

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, config.entity, id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, missionQueryErrorCode, "failed to query mission")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, missionNotFoundCode, "mission not found")
		return
	}

	items := normalizeMissionRecords(config.family, []map[string]any{record})
	if len(items) != 1 {
		response.Error(c, http.StatusInternalServerError, missionQueryErrorCode, "failed to normalize mission")
		return
	}
	if err := handler.resolveMissionParameterGroups(c.Request.Context(), region, config.family, items); err != nil {
		response.Error(c, http.StatusInternalServerError, missionQueryErrorCode, "failed to resolve mission parameter groups")
		return
	}
	rewardRequest := missionRewardResolutionRequest{region: region, items: items}
	if err := handler.resolveMissionRewards(c.Request.Context(), rewardRequest); err != nil {
		response.Error(c, http.StatusInternalServerError, missionQueryErrorCode, "failed to resolve mission rewards")
		return
	}

	response.JSON(c, http.StatusOK, items[0].response)
}

// CharacterMissionV2ParameterGroupLevels godoc
// @Summary List Character Mission V2 levels for a parameter group
// @Tags missions
// @Produce json
// @Param region path string true "Region"
// @Param id path int true "Parameter group ID" minimum(1)
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Success 200 {object} shared.MissionParameterGroupLevelsResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Router /characterMissionV2ParameterGroups/{region}/{id}/levels [get]
func (handler *LookupHandler) CharacterMissionV2ParameterGroupLevels(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region, ok := parseTypedLookupRegion(c, handler.masterDataSync)
	if !ok {
		return
	}
	groupID, ok := parseTypedLookupID(c)
	if !ok {
		return
	}
	page, pageSize, ok := parseLookupPagination(c)
	if !ok {
		return
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, characterMissionV2ParameterGroupsEntity) {
		return
	}

	records, err := handler.masterDataSync.ListAll(c.Request.Context(), region, characterMissionV2ParameterGroupsEntity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, missionQueryErrorCode, "failed to list mission parameter group levels")
		return
	}
	parsedID, _ := strconv.ParseInt(groupID, 10, 64)
	levels := buildMissionParameterGroupCatalog(records)[parsedID]
	if len(levels) == 0 {
		response.Error(c, http.StatusNotFound, missionParameterGroupNotFoundCode, "mission parameter group not found")
		return
	}

	resourceTypes, err := handler.loadMissionParameterGroupResourceTypes(c.Request.Context(), region)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, missionQueryErrorCode, "failed to resolve mission parameter group rewards")
		return
	}
	levels = addMissionParameterGroupRewards(levels, resourceTypes[parsedID])
	pageLevels := paginateMissionParameterGroupLevels(levels, page, pageSize)
	response.JSON(c, http.StatusOK, shared.MissionParameterGroupLevelsResponse{
		Items:      pageLevels,
		Pagination: lookupPaginationResponse(page, pageSize, len(levels)),
	})
}

// CharacterRanksList godoc
// @Summary List Character Rank references for a character
// @Tags characterRanks
// @Produce json
// @Param region path string true "Region"
// @Param character_id query int true "Character ID" minimum(1)
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Success 200 {object} shared.CharacterRankListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Router /characterRanks/{region}/list [get]
func (handler *LookupHandler) CharacterRanksList(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}
	region, ok := parseTypedLookupRegion(c, handler.masterDataSync)
	if !ok {
		return
	}
	characterID, ok := parsePositiveQueryID(c, "character_id")
	if !ok {
		return
	}
	page, pageSize, ok := parseLookupPagination(c)
	if !ok {
		return
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, characterRanksEntity) {
		return
	}

	ranks, err := handler.masterDataSync.ListAll(c.Request.Context(), region, characterRanksEntity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CHARACTER_RANK_QUERY_ERROR", "failed to list character ranks")
		return
	}
	boxes, err := handler.masterDataSync.ListAll(c.Request.Context(), region, resourceBoxesEntity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CHARACTER_RANK_QUERY_ERROR", "failed to resolve character rank rewards")
		return
	}
	items := projectCharacterRankResponses(ranks, boxes, characterID)
	response.JSON(c, http.StatusOK, shared.CharacterRankListResponse{
		Items:      paginateCharacterRankResponses(items, page, pageSize),
		Pagination: lookupPaginationResponse(page, pageSize, len(items)),
	})
}

func parsePositiveQueryID(c *gin.Context, key string) (int64, bool) {
	rawValue, exists := c.GetQuery(key)
	if !exists || strings.TrimSpace(rawValue) == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", key+" must be a positive integer")
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimSpace(rawValue), 10, 64)
	if err != nil || id <= 0 {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", key+" must be a positive integer")
		return 0, false
	}
	return id, true
}

func projectCharacterRankResponses(
	rankRecords []map[string]any,
	resourceBoxRecords []map[string]any,
	characterID int64,
) []shared.CharacterRankResponse {
	rewardBoxesByID := make(map[int64][]map[string]any)
	for _, record := range resourceBoxRecords {
		id, ok := lookupInt64(record["id"])
		if !ok || id <= 0 || !strings.EqualFold(strings.TrimSpace(lookupString(record["resourceBoxPurpose"])), "character_rank_reward") {
			continue
		}
		rewardBoxesByID[id] = append(rewardBoxesByID[id], record)
	}

	items := make([]shared.CharacterRankResponse, 0)
	for _, record := range rankRecords {
		recordCharacterID, ok := lookupInt64(record["characterId"])
		if !ok || recordCharacterID != characterID {
			continue
		}
		id, ok := lookupInt64(record["id"])
		if !ok {
			continue
		}
		rank, ok := lookupInt64(record["characterRank"])
		if !ok {
			continue
		}
		item := shared.CharacterRankResponse{
			ID:                  id,
			CharacterID:         recordCharacterID,
			CharacterRank:       rank,
			Power1BonusRate:     lookupOptionalFloat64(record["power1BonusRate"]),
			Power2BonusRate:     lookupOptionalFloat64(record["power2BonusRate"]),
			Power3BonusRate:     lookupOptionalFloat64(record["power3BonusRate"]),
			RewardResourceBoxes: make([]shared.MissionResourceBoxResponse, 0),
		}
		for _, resourceBoxID := range lookupPositiveInt64List(record["rewardResourceBoxIds"]) {
			for _, boxRecord := range rewardBoxesByID[resourceBoxID] {
				if box := projectMissionResourceBox(boxRecord); box != nil {
					item.RewardResourceBoxes = append(item.RewardResourceBoxes, *box)
				}
			}
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(left, right int) bool {
		return items[left].CharacterRank < items[right].CharacterRank
	})
	return items
}

func lookupPositiveInt64List(value any) []int64 {
	values := make([]int64, 0)
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if parsed, ok := lookupInt64(item); ok && parsed > 0 {
				values = append(values, parsed)
			}
		}
	case []int64:
		for _, item := range typed {
			if item > 0 {
				values = append(values, item)
			}
		}
	}
	return values
}

func paginateCharacterRankResponses(items []shared.CharacterRankResponse, page int, pageSize int) []shared.CharacterRankResponse {
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []shared.CharacterRankResponse{}
	}
	return items[start:minInt(start+pageSize, len(items))]
}

func validateMissionQueryParameters(c *gin.Context, allowFamily bool) bool {
	allowed := map[string]struct{}{
		"page":         {},
		"page_size":    {},
		"id":           {},
		"character_id": {},
		"event_id":     {},
		"sort_by":      {},
		"sort_order":   {},
	}
	if allowFamily {
		allowed["family"] = struct{}{}
	}

	for key := range c.Request.URL.Query() {
		if _, ok := allowed[key]; ok {
			continue
		}
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "unknown mission query parameter: "+key)
		return false
	}

	return true
}

func missionFamilyFromQuery(c *gin.Context) (missionFamilyConfig, bool) {
	values := c.QueryArray("family")
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", missionFamilyAllowlistError)
		return missionFamilyConfig{}, false
	}

	return missionFamilyByName(c, values[0])
}

func missionFamilyFromPath(c *gin.Context) (missionFamilyConfig, bool) {
	family := strings.TrimSpace(c.Param("family"))
	if family == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", missionFamilyAllowlistError)
		return missionFamilyConfig{}, false
	}

	return missionFamilyByName(c, family)
}

func missionFamilyByName(c *gin.Context, family string) (missionFamilyConfig, bool) {
	config, ok := missionFamilyConfigs[strings.TrimSpace(family)]
	if ok {
		return config, true
	}

	response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", missionFamilyAllowlistError)
	return missionFamilyConfig{}, false
}

func parseMissionFilters(c *gin.Context, config missionFamilyConfig) (map[string]map[int64]struct{}, bool) {
	if !validateMissionFilterParameters(c, config) {
		return nil, false
	}

	filters := make(map[string]map[int64]struct{})
	for parameter, field := range config.filterable {
		values, exists := c.Request.URL.Query()[parameter]
		if !exists {
			continue
		}

		parsedValues, ok := parseMissionFilterParameter(c, parameter, values)
		if !ok {
			return nil, false
		}
		filters[field] = parsedValues
	}

	return filters, true
}

func validateMissionFilterParameters(c *gin.Context, config missionFamilyConfig) bool {
	for _, parameter := range []string{"id", "character_id", "event_id"} {
		if _, exists := c.Request.URL.Query()[parameter]; !exists {
			continue
		}
		if _, allowed := config.filterable[parameter]; allowed {
			continue
		}
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", parameter+" is not supported for "+config.family)
		return false
	}

	return true
}

func parseMissionFilterParameter(c *gin.Context, parameter string, values []string) (map[int64]struct{}, bool) {
	parsedValues := make(map[int64]struct{})
	for _, rawValue := range values {
		if strings.TrimSpace(rawValue) == "" {
			return nil, rejectInvalidMissionFilter(c, parameter)
		}
		for _, part := range strings.Split(rawValue, ",") {
			value, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
			if err != nil || value <= 0 {
				return nil, rejectInvalidMissionFilter(c, parameter)
			}
			parsedValues[value] = struct{}{}
		}
	}

	return parsedValues, true
}

func rejectInvalidMissionFilter(c *gin.Context, parameter string) bool {
	response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", parameter+" must contain positive integer IDs")
	return false
}

func normalizeMissionRecords(family string, records []map[string]any) []missionItem {
	items := make([]missionItem, 0, len(records))
	for _, record := range records {
		item, ok := normalizeMissionRecord(family, record)
		if ok {
			items = append(items, item)
		}
	}

	return items
}

func normalizeMissionRecord(family string, record map[string]any) (missionItem, bool) {
	id, ok := lookupInt64(record["id"])
	if !ok || id <= 0 {
		return missionItem{}, false
	}

	item := missionItem{
		response: shared.MissionResponse{
			ID:     id,
			Family: family,
		},
	}

	setMissionInt64(&item.response.Seq, record, "seq")
	setMissionString(&item.response.Sentence, record, "sentence")
	setMissionString(&item.response.ProgressSentence, record, "progressSentence")
	setMissionInt64(&item.response.Requirement, record, "requirement")
	setMissionInt64(&item.response.EventID, record, "eventId")
	setMissionInt64(&item.response.ResourceBoxID, record, "resourceBoxId")

	switch family {
	case storyMissionsFamily:
		setMissionString(&item.response.StoryMissionType, record, "storyMissionType")
	case characterMissionV2sFamily:
		setMissionString(&item.response.CharacterMissionType, record, "characterMissionType")
		setMissionInt64(&item.response.CharacterID, record, "characterId")
		setMissionInt64(&item.response.ParameterGroupID, record, "parameterGroupId")
		setMissionBool(&item.response.IsAchievementMission, record, "isAchievementMission")
	case normalMissionsFamily:
		setMissionString(&item.response.NormalMissionType, record, "normalMissionType")
	}

	if rewards, exists := record["rewards"]; exists {
		item.rewardsPresent = true
		item.rewardRefs = normalizeMissionRewards(rewards)
	}
	if !item.rewardsPresent && item.response.ResourceBoxID != nil {
		item.rewardsPresent = true
		item.rewardRefs = []missionRewardReference{{
			response: shared.MissionRewardResponse{
				ResourceBoxID: item.response.ResourceBoxID,
				Status:        missionRewardUnresolvedStatus,
			},
		}}
	}

	return item, true
}

func setMissionInt64(target **int64, record map[string]any, field string) {
	if value, ok := record[field]; ok {
		*target = lookupOptionalInt64(value)
	}
}

func setMissionString(target **string, record map[string]any, field string) {
	if value, ok := record[field]; ok {
		*target = lookupOptionalString(value)
	}
}

func setMissionBool(target **bool, record map[string]any, field string) {
	value, ok := record[field].(bool)
	if ok {
		*target = &value
	}
}

func normalizeMissionRewards(value any) []missionRewardReference {
	items := make([]missionRewardReference, 0)
	switch typed := value.(type) {
	case []any:
		for _, entry := range typed {
			items = append(items, normalizeMissionRewardEntry(entry)...)
		}
	case []map[string]any:
		for _, entry := range typed {
			items = append(items, normalizeMissionRewardEntry(entry)...)
		}
	case map[string]any:
		items = append(items, normalizeMissionRewardEntry(typed)...)
	default:
		items = append(items, normalizeMissionRewardEntry(value)...)
	}

	return items
}

func normalizeMissionRewardEntry(value any) []missionRewardReference {
	switch typed := value.(type) {
	case map[string]any:
		return []missionRewardReference{normalizeMissionRewardMap(typed)}
	case []any:
		return normalizeMissionRewardGroup(typed)
	case []map[string]any:
		items := make([]missionRewardReference, 0, len(typed))
		for _, entry := range typed {
			items = append(items, normalizeMissionRewardMap(entry))
		}
		return items
	default:
		resourceBoxID, ok := lookupInt64(value)
		if !ok || resourceBoxID <= 0 {
			return []missionRewardReference{}
		}
		return []missionRewardReference{{
			response: shared.MissionRewardResponse{
				ResourceBoxID: &resourceBoxID,
				Status:        missionRewardUnresolvedStatus,
			},
		}}
	}
}

func normalizeMissionRewardGroup(values []any) []missionRewardReference {
	resourceBoxIDs := make([]int64, 0, len(values))
	items := make([]missionRewardReference, 0)
	for _, value := range values {
		if record, ok := lookupRecord(value); ok {
			items = append(items, normalizeMissionRewardMap(record))
			continue
		}
		resourceBoxID, ok := lookupInt64(value)
		if ok && resourceBoxID > 0 {
			resourceBoxIDs = append(resourceBoxIDs, resourceBoxID)
		}
	}
	if len(items) > 0 {
		return items
	}
	if len(resourceBoxIDs) == 0 {
		return []missionRewardReference{}
	}

	reference := missionRewardReference{
		response: shared.MissionRewardResponse{
			Status: missionRewardUnresolvedStatus,
		},
	}
	if len(resourceBoxIDs) == 1 {
		reference.response.ResourceBoxID = &resourceBoxIDs[0]
	} else {
		reference.response.ResourceBoxIDs = resourceBoxIDs
	}

	return []missionRewardReference{reference}
}

func normalizeMissionRewardMap(record map[string]any) missionRewardReference {
	response := shared.MissionRewardResponse{Status: missionRewardUnresolvedStatus}
	looksLikeResourceBox := record["details"] != nil || record["resourceBoxType"] != nil
	if !looksLikeResourceBox {
		setMissionRewardInt64(&response.ID, record, "id")
	}
	setMissionRewardString(&response.MissionType, record, "missionType")
	setMissionRewardInt64(&response.MissionID, record, "missionId")
	setMissionRewardInt64(&response.Seq, record, "seq")
	setMissionRewardInt64(&response.ResourceBoxID, record, "resourceBoxId")
	setMissionRewardString(&response.ResourceBoxPurpose, record, "resourceBoxPurpose")
	setMissionRewardString(&response.ResourceType, record, "resourceType")
	setMissionRewardInt64(&response.ResourceID, record, "resourceId")
	setMissionRewardInt64(&response.ResourceLevel, record, "resourceLevel")
	setMissionRewardInt64(&response.ResourceQuantity, record, "resourceQuantity")

	var embeddedBox map[string]any
	if resourceBox, ok := lookupRecord(record["resourceBox"]); ok {
		embeddedBox = resourceBox
	}
	if looksLikeResourceBox {
		embeddedBox = record
		if response.ResourceBoxID == nil {
			setMissionRewardInt64(&response.ResourceBoxID, record, "id")
		}
	}

	return missionRewardReference{
		response:    response,
		embeddedBox: embeddedBox,
	}
}

func setMissionRewardInt64(target **int64, record map[string]any, field string) {
	if value, ok := record[field]; ok {
		*target = lookupOptionalInt64(value)
	}
}

func setMissionRewardString(target **string, record map[string]any, field string) {
	if value, ok := record[field]; ok {
		*target = lookupOptionalString(value)
	}
}

func filterMissionItems(items []missionItem, filters map[string]map[int64]struct{}) []missionItem {
	if len(filters) == 0 {
		return items
	}

	filtered := make([]missionItem, 0, len(items))
	for _, item := range items {
		matches := true
		for field, values := range filters {
			value, ok := missionInt64Field(item.response, field)
			if !ok {
				matches = false
				break
			}
			if _, exists := values[value]; !exists {
				matches = false
				break
			}
		}
		if matches {
			filtered = append(filtered, item)
		}
	}

	return filtered
}

func missionInt64Field(item shared.MissionResponse, field string) (int64, bool) {
	switch field {
	case "id":
		return item.ID, true
	case "seq":
		return missionOptionalInt64Value(item.Seq)
	case "requirement":
		return missionOptionalInt64Value(item.Requirement)
	case "characterId":
		return missionOptionalInt64Value(item.CharacterID)
	case "parameterGroupId":
		return missionOptionalInt64Value(item.ParameterGroupID)
	case "eventId":
		return missionOptionalInt64Value(item.EventID)
	case "resourceBoxId":
		return missionOptionalInt64Value(item.ResourceBoxID)
	default:
		return 0, false
	}
}

func missionOptionalInt64Value(value *int64) (int64, bool) {
	if value == nil {
		return 0, false
	}
	return *value, true
}

func sortMissionItems(items []missionItem, field string, descending bool) {
	sort.SliceStable(items, func(leftIndex, rightIndex int) bool {
		left := missionSortValue(items[leftIndex].response, field)
		right := missionSortValue(items[rightIndex].response, field)
		comparison := compareMissionValues(missionOperands[any]{left: left, right: right})
		if comparison == 0 {
			comparison = compareMissionInt64(missionOperands[int64]{
				left:  items[leftIndex].response.ID,
				right: items[rightIndex].response.ID,
			})
		}
		if comparison == 0 {
			comparison = compareMissionOptionalInt64(missionOperands[*int64]{
				left:  items[leftIndex].response.Seq,
				right: items[rightIndex].response.Seq,
			})
		}
		if descending {
			return comparison > 0
		}
		return comparison < 0
	})
}

func missionSortValue(item shared.MissionResponse, field string) any {
	switch field {
	case "id":
		return item.ID
	case "seq":
		return optionalMissionValue(item.Seq)
	case "sentence":
		return optionalMissionValue(item.Sentence)
	case "progressSentence":
		return optionalMissionValue(item.ProgressSentence)
	case "requirement":
		return optionalMissionValue(item.Requirement)
	case "characterId":
		return optionalMissionValue(item.CharacterID)
	case "parameterGroupId":
		return optionalMissionValue(item.ParameterGroupID)
	case "eventId":
		return optionalMissionValue(item.EventID)
	case "resourceBoxId":
		return optionalMissionValue(item.ResourceBoxID)
	case "storyMissionType":
		return optionalMissionValue(item.StoryMissionType)
	case "normalMissionType":
		return optionalMissionValue(item.NormalMissionType)
	case "characterMissionType":
		return optionalMissionValue(item.CharacterMissionType)
	case "isAchievementMission":
		return optionalMissionValue(item.IsAchievementMission)
	default:
		return nil
	}
}

func optionalMissionValue[T any](value *T) any {
	if value == nil {
		return nil
	}
	return *value
}

func compareMissionValues(operands missionOperands[any]) int {
	if operands.left == nil || operands.right == nil {
		return compareMissionNilValues(operands)
	}

	if comparison, ok := compareMissionNumericValues(operands); ok {
		return comparison
	}

	if comparison, ok := compareMissionBooleanValues(operands); ok {
		return comparison
	}

	return cmp.Compare(
		shared.NormalizeComparableText(operands.left),
		shared.NormalizeComparableText(operands.right),
	)
}

func compareMissionNilValues(operands missionOperands[any]) int {
	switch {
	case operands.left == nil && operands.right == nil:
		return 0
	case operands.left == nil:
		return -1
	default:
		return 1
	}
}

func compareMissionNumericValues(operands missionOperands[any]) (int, bool) {
	leftNumber, ok := lookupInt64(operands.left)
	if !ok {
		return 0, false
	}
	rightNumber, ok := lookupInt64(operands.right)
	if !ok {
		return 0, false
	}

	return compareMissionInt64(missionOperands[int64]{left: leftNumber, right: rightNumber}), true
}

func compareMissionBooleanValues(operands missionOperands[any]) (int, bool) {
	leftBool, ok := operands.left.(bool)
	if !ok {
		return 0, false
	}
	rightBool, ok := operands.right.(bool)
	if !ok {
		return 0, false
	}
	switch {
	case leftBool == rightBool:
		return 0, true
	case !leftBool:
		return -1, true
	default:
		return 1, true
	}
}

func compareMissionInt64(operands missionOperands[int64]) int {
	return cmp.Compare(operands.left, operands.right)
}

func compareMissionOptionalInt64(operands missionOperands[*int64]) int {
	left, right := operands.left, operands.right
	if left == nil || right == nil {
		switch {
		case left == nil && right == nil:
			return 0
		case left == nil:
			return 1
		default:
			return -1
		}
	}

	return cmp.Compare(*left, *right)
}

func paginateMissionItems(items []missionItem, page int, pageSize int) []missionItem {
	totalPages := (len(items) + pageSize - 1) / pageSize
	if page > totalPages {
		return []missionItem{}
	}

	start := (page - 1) * pageSize
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}

	return items[start:end]
}

func (handler *LookupHandler) resolveMissionParameterGroups(ctx context.Context, region string, family string, items []missionItem) error {
	if family != characterMissionV2sFamily {
		return nil
	}

	records, err := handler.masterDataSync.ListAll(ctx, region, characterMissionV2ParameterGroupsEntity)
	if err != nil {
		return err
	}

	groups := buildMissionParameterGroupCatalog(records)
	resourceTypes, err := handler.loadMissionParameterGroupResourceTypes(ctx, region)
	if err != nil {
		return err
	}
	for index := range items {
		parameterGroupID := items[index].response.ParameterGroupID
		if parameterGroupID == nil {
			continue
		}

		levels, ok := groups[*parameterGroupID]
		if ok {
			items[index].response.ParameterGroup = buildMissionParameterGroupResponse(*parameterGroupID, addMissionParameterGroupRewards(levels, resourceTypes[*parameterGroupID]))
		}
	}

	return nil
}

func buildMissionParameterGroupCatalog(records []map[string]any) missionParameterGroupCatalog {
	groups := make(missionParameterGroupCatalog)
	for _, record := range records {
		id, ok := lookupInt64(record["id"])
		if !ok || id <= 0 {
			continue
		}
		seq, ok := lookupInt64(record["seq"])
		if !ok {
			continue
		}
		requirement, ok := lookupInt64(record["requirement"])
		if !ok {
			continue
		}

		level := shared.MissionParameterGroupLevelResponse{
			Seq:         seq,
			Requirement: requirement,
		}
		if value, exists := record["exp"]; exists {
			level.Exp = lookupOptionalInt64(value)
		}
		if value, exists := record["quantity"]; exists {
			level.Quantity = lookupOptionalInt64(value)
		}
		groups[id] = append(groups[id], level)
	}

	for id, levels := range groups {
		sort.SliceStable(levels, func(leftIndex, rightIndex int) bool {
			return levels[leftIndex].Seq < levels[rightIndex].Seq
		})
		groups[id] = levels
	}

	return groups
}

func buildMissionParameterGroupResponse(id int64, levels []shared.MissionParameterGroupLevelResponse) *shared.MissionParameterGroupResponse {
	previewCount := minInt(len(levels), 3)
	preview := append([]shared.MissionParameterGroupLevelResponse(nil), levels[:previewCount]...)
	var last *shared.MissionParameterGroupLevelResponse
	if len(levels) > 0 {
		lastValue := levels[len(levels)-1]
		last = &lastValue
	}
	return &shared.MissionParameterGroupResponse{
		ID:            id,
		TotalLevels:   len(levels),
		PreviewLevels: preview,
		LastLevel:     last,
	}
}

func paginateMissionParameterGroupLevels(levels []shared.MissionParameterGroupLevelResponse, page int, pageSize int) []shared.MissionParameterGroupLevelResponse {
	start := (page - 1) * pageSize
	if start >= len(levels) {
		return []shared.MissionParameterGroupLevelResponse{}
	}
	end := minInt(start+pageSize, len(levels))
	return levels[start:end]
}

func addMissionParameterGroupRewards(levels []shared.MissionParameterGroupLevelResponse, resourceType string) []shared.MissionParameterGroupLevelResponse {
	result := append([]shared.MissionParameterGroupLevelResponse(nil), levels...)
	for index := range result {
		quantity := result[index].Quantity
		if resourceType == "" || quantity == nil || *quantity <= 0 {
			continue
		}
		result[index].Reward = &shared.MissionParameterGroupLevelRewardResponse{
			ResourceQuantity: quantity,
			ResourceType:     resourceType,
		}
	}
	return result
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func (handler *LookupHandler) loadMissionParameterGroupResourceTypes(ctx context.Context, region string) (map[int64]string, error) {
	missions, err := handler.masterDataSync.ListAll(ctx, region, characterMissionV2sEntity)
	if err != nil {
		return nil, err
	}
	exRecords, err := handler.masterDataSync.ListAll(ctx, region, characterMissionV2ExJsonsEntity)
	if err != nil {
		return nil, err
	}
	exTypes := make(map[string]string, len(exRecords))
	for _, record := range exRecords {
		missionType := strings.TrimSpace(lookupString(record["characterMissionType"]))
		resourceType := strings.TrimSpace(lookupString(record["resourceType"]))
		if missionType != "" && resourceType != "" {
			exTypes[missionType] = resourceType
		}
	}
	resourceTypes := make(map[int64]string)
	for _, mission := range missions {
		missionGroupID, ok := lookupInt64(mission["parameterGroupId"])
		if !ok || missionGroupID <= 0 {
			continue
		}
		if resourceType := exTypes[strings.TrimSpace(lookupString(mission["characterMissionType"]))]; resourceType != "" {
			resourceTypes[missionGroupID] = resourceType
		}
	}
	return resourceTypes, nil
}

func (handler *LookupHandler) resolveMissionRewards(ctx context.Context, request missionRewardResolutionRequest) error {
	catalog := missionRewardCatalog{}
	if missionRewardCatalogRequired(request.items) {
		var err error
		catalog, err = handler.loadMissionRewardCatalog(ctx, request.region)
		if err != nil {
			return err
		}
	}

	for index := range request.items {
		item := &request.items[index]
		if !item.rewardsPresent {
			continue
		}

		rewards := resolveMissionRewardReferences(catalog, item.rewardRefs)
		item.response.Rewards = &rewards
	}

	return nil
}

func missionRewardCatalogRequired(items []missionItem) bool {
	for _, item := range items {
		for _, reference := range item.rewardRefs {
			if missionRewardReferenceRequiresCatalog(reference) {
				return true
			}
		}
	}

	return false
}

func missionRewardReferenceRequiresCatalog(reference missionRewardReference) bool {
	return reference.response.ResourceBoxID != nil ||
		(reference.embeddedBox != nil && !missionRecordHasField(reference.embeddedBox, "details"))
}

func resolveMissionRewardReferences(catalog missionRewardCatalog, references []missionRewardReference) []shared.MissionRewardResponse {
	rewards := make([]shared.MissionRewardResponse, 0, len(references))
	for _, reference := range references {
		rewards = append(rewards, resolveMissionRewardReference(catalog, reference))
	}

	return rewards
}

func resolveMissionRewardReference(catalog missionRewardCatalog, reference missionRewardReference) shared.MissionRewardResponse {
	reward := reference.response
	reward.Status = missionRewardUnresolvedStatus
	resolveEmbeddedMissionReward(&reward, reference.embeddedBox)
	resolveCatalogMissionReward(&reward, catalog)
	return reward
}

func resolveEmbeddedMissionReward(reward *shared.MissionRewardResponse, embeddedBox map[string]any) {
	if box := projectMissionResourceBox(embeddedBox); box != nil {
		reward.ResourceBox = box
		reward.Status = missionRewardResolvedStatus
		if reward.ResourceBoxID == nil {
			reward.ResourceBoxID = box.ID
		}
		if reward.ResourceBoxPurpose == nil {
			reward.ResourceBoxPurpose = box.ResourceBoxPurpose
		}
	}
}

func resolveCatalogMissionReward(reward *shared.MissionRewardResponse, catalog missionRewardCatalog) {
	if reward.ResourceBox != nil || reward.ResourceBoxID == nil || len(reward.ResourceBoxIDs) != 0 {
		return
	}

	box, found := catalog.resolve(*reward.ResourceBoxID, reward.ResourceBoxPurpose)
	if !found {
		return
	}

	reward.ResourceBox = box
	reward.Status = missionRewardResolvedStatus
	if reward.ResourceBoxPurpose == nil {
		reward.ResourceBoxPurpose = box.ResourceBoxPurpose
	}
}

func (handler *LookupHandler) loadMissionRewardCatalog(ctx context.Context, region string) (missionRewardCatalog, error) {
	boxes, err := handler.masterDataSync.ListAll(ctx, region, resourceBoxesEntity)
	if err != nil {
		return missionRewardCatalog{}, err
	}
	details, err := handler.masterDataSync.ListAll(ctx, region, resourceBoxDetailsEntity)
	if err != nil {
		return missionRewardCatalog{}, err
	}

	return buildMissionRewardCatalog(missionRewardCatalogRequest{boxes: boxes, details: details}), nil
}

func buildMissionRewardCatalog(request missionRewardCatalogRequest) missionRewardCatalog {
	catalog := missionRewardCatalog{
		boxesByID:    make(map[int64][]missionResourceBoxCandidate),
		detailsByBox: make(map[string][]map[string]any),
	}
	addMissionResourceBoxCandidates(&catalog, request.boxes)
	addMissionResourceBoxDetails(&catalog, request.details)
	sortMissionResourceBoxDetails(catalog.detailsByBox)

	return catalog
}

func addMissionResourceBoxCandidates(catalog *missionRewardCatalog, boxes []map[string]any) {
	for _, record := range boxes {
		id, ok := lookupInt64(record["id"])
		if !ok || id <= 0 {
			continue
		}
		purpose := strings.TrimSpace(lookupString(record["resourceBoxPurpose"]))
		catalog.boxesByID[id] = append(catalog.boxesByID[id], missionResourceBoxCandidate{
			record:  record,
			id:      id,
			purpose: purpose,
		})
	}
}

func addMissionResourceBoxDetails(catalog *missionRewardCatalog, details []map[string]any) {
	for _, record := range details {
		parentID, ok := lookupInt64(record["resourceBoxId"])
		if !ok || parentID <= 0 {
			continue
		}
		purpose := strings.TrimSpace(lookupString(record["resourceBoxPurpose"]))
		if purpose == "" {
			continue
		}
		key := missionResourceBoxKey(purpose, parentID)
		catalog.detailsByBox[key] = append(catalog.detailsByBox[key], record)
	}
}

func sortMissionResourceBoxDetails(detailsByBox map[string][]map[string]any) {
	for key := range detailsByBox {
		sort.SliceStable(detailsByBox[key], func(leftIndex, rightIndex int) bool {
			return missionResourceBoxDetailLess(missionOperands[map[string]any]{
				left:  detailsByBox[key][leftIndex],
				right: detailsByBox[key][rightIndex],
			})
		})
	}
}

func missionResourceBoxDetailLess(operands missionOperands[map[string]any]) bool {
	leftSequence, leftOK := lookupInt64(operands.left["seq"])
	rightSequence, rightOK := lookupInt64(operands.right["seq"])
	if leftOK != rightOK {
		return leftOK
	}

	return leftOK && cmp.Compare(leftSequence, rightSequence) < 0
}

func (catalog missionRewardCatalog) resolve(id int64, purpose *string) (*shared.MissionResourceBoxResponse, bool) {
	candidates := catalog.boxesByID[id]
	if len(candidates) == 0 {
		return nil, false
	}

	matching := make([]missionResourceBoxCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if purpose != nil && strings.TrimSpace(*purpose) != "" && !strings.EqualFold(candidate.purpose, strings.TrimSpace(*purpose)) {
			continue
		}
		matching = append(matching, candidate)
	}
	if len(matching) != 1 {
		return nil, false
	}

	candidate := matching[0]
	box := projectMissionResourceBox(candidate.record)
	if box == nil {
		return nil, false
	}
	if !missionRecordHasField(candidate.record, "details") {
		for _, detail := range catalog.detailsByBox[missionResourceBoxKey(candidate.purpose, candidate.id)] {
			box.Details = append(box.Details, projectMissionResourceBoxDetail(detail))
		}
	}

	return box, true
}

func missionResourceBoxKey(purpose string, id int64) string {
	return strings.ToLower(strings.TrimSpace(purpose)) + "\x00" + strconv.FormatInt(id, 10)
}

func projectMissionResourceBox(record map[string]any) *shared.MissionResourceBoxResponse {
	if record == nil {
		return nil
	}
	box := &shared.MissionResourceBoxResponse{
		ID:                 lookupOptionalInt64(record["id"]),
		ResourceBoxPurpose: lookupOptionalString(record["resourceBoxPurpose"]),
		ResourceBoxType:    lookupOptionalString(record["resourceBoxType"]),
	}
	if details, exists := record["details"]; exists {
		box.Details = projectMissionResourceBoxDetails(details)
	}

	return box
}

func projectMissionResourceBoxDetails(value any) []shared.MissionResourceBoxDetailResponse {
	records := make([]map[string]any, 0)
	switch typed := value.(type) {
	case []any:
		for _, entry := range typed {
			if record, ok := lookupRecord(entry); ok {
				records = append(records, record)
			}
		}
	case []map[string]any:
		records = append(records, typed...)
	}

	items := make([]shared.MissionResourceBoxDetailResponse, 0, len(records))
	for _, record := range records {
		items = append(items, projectMissionResourceBoxDetail(record))
	}
	sort.SliceStable(items, func(leftIndex, rightIndex int) bool {
		return compareMissionOptionalInt64(missionOperands[*int64]{
			left:  items[leftIndex].Seq,
			right: items[rightIndex].Seq,
		}) < 0
	})

	return items
}

func projectMissionResourceBoxDetail(record map[string]any) shared.MissionResourceBoxDetailResponse {
	return shared.MissionResourceBoxDetailResponse{
		ResourceBoxPurpose: lookupOptionalString(record["resourceBoxPurpose"]),
		ResourceBoxID:      lookupOptionalInt64(record["resourceBoxId"]),
		Seq:                lookupOptionalInt64(record["seq"]),
		ResourceType:       lookupOptionalString(record["resourceType"]),
		ResourceID:         lookupOptionalInt64(record["resourceId"]),
		ResourceLevel:      lookupOptionalInt64(record["resourceLevel"]),
		ResourceQuantity:   lookupOptionalInt64(record["resourceQuantity"]),
	}
}

func missionRecordHasField(record map[string]any, field string) bool {
	_, exists := record[field]
	return exists
}
