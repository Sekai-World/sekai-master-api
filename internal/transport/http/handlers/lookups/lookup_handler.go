package lookups

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type LookupHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase

	// Process-local reward lookups shared by mission and character-rank
	// endpoints, revalidated against each entity's persisted revision.
	resourceBoxIndexes       revisionCache[map[int64][]missionResourceBoxCandidate]
	resourceBoxDetailIndexes revisionCache[map[string][]map[string]any]
	// Keyed by region and item entity; see shared.RewardItemEntities.
	rewardItemIndexes revisionCache[map[int64]missionRewardItem]
	// Cumulative EXP per character rank, keyed by region.
	characterRankTotalExps revisionCache[map[int64]int64]
}

type lookupResourceConfig struct {
	entity                 string
	queryErrorCode         string
	notFoundCode           string
	resourceLabel          string
	sortableFields         []string
	expandReleaseCondition bool
	// filterableFields maps query params to exact-match numeric record
	// fields. Filtered requests always load the full entity and filter in
	// memory, so they bypass the database paging fast path.
	filterableFields map[string]string
}

var unitProfilesConfig = lookupResourceConfig{
	entity:         "unitprofiles",
	queryErrorCode: "UNIT_PROFILE_QUERY_ERROR",
	notFoundCode:   "UNIT_PROFILE_NOT_FOUND",
	resourceLabel:  "unit profile",
	sortableFields: []string{"id", "unit", "unitName", "colorCode"},
}

var gameCharacterUnitsConfig = lookupResourceConfig{
	entity:         "gamecharacterunits",
	queryErrorCode: "GAME_CHARACTER_UNIT_QUERY_ERROR",
	notFoundCode:   "GAME_CHARACTER_UNIT_NOT_FOUND",
	resourceLabel:  "game character unit",
	sortableFields: []string{"id", "gameCharacterId", "unit", "colorCode"},
}

var gameCharactersConfig = lookupResourceConfig{
	entity:         "gamecharacters",
	queryErrorCode: "GAME_CHARACTER_QUERY_ERROR",
	notFoundCode:   "GAME_CHARACTER_NOT_FOUND",
	resourceLabel:  "game character",
	sortableFields: []string{"id", "seq", "firstName", "givenName", "unit", "height"},
}

var gameCharacterProfilesConfig = lookupResourceConfig{
	entity:         "characterprofiles",
	queryErrorCode: "GAME_CHARACTER_PROFILE_QUERY_ERROR",
	notFoundCode:   "GAME_CHARACTER_PROFILE_NOT_FOUND",
	resourceLabel:  "game character profile",
}

var worldBloomsConfig = lookupResourceConfig{
	entity:         "worldblooms",
	queryErrorCode: "WORLD_BLOOM_QUERY_ERROR",
	notFoundCode:   "WORLD_BLOOM_NOT_FOUND",
	resourceLabel:  "world bloom",
	sortableFields: []string{"id", "eventId", "startAt", "endAt"},
}

var unitStoriesConfig = lookupResourceConfig{
	entity:         "unitstories",
	queryErrorCode: "UNIT_STORY_QUERY_ERROR",
	notFoundCode:   "UNIT_STORY_NOT_FOUND",
	resourceLabel:  "unit story",
	sortableFields: []string{"unit", "seq"},
}

var eventStoriesConfig = lookupResourceConfig{
	entity:         "eventstories",
	queryErrorCode: "EVENT_STORY_QUERY_ERROR",
	notFoundCode:   "EVENT_STORY_NOT_FOUND",
	resourceLabel:  "event story",
	sortableFields: []string{"id", "eventId"},
	filterableFields: map[string]string{
		"event_id": "eventId",
	},
}

var characterProfilesConfig = lookupResourceConfig{
	entity:         "characterprofiles",
	queryErrorCode: "CHARACTER_PROFILE_QUERY_ERROR",
	notFoundCode:   "CHARACTER_PROFILE_NOT_FOUND",
	resourceLabel:  "character profile",
	sortableFields: []string{"characterId"},
}

var cardEpisodesConfig = lookupResourceConfig{
	entity:                 "cardepisodes",
	queryErrorCode:         "CARD_EPISODE_QUERY_ERROR",
	notFoundCode:           "CARD_EPISODE_NOT_FOUND",
	resourceLabel:          "card episode",
	sortableFields:         []string{"id", "cardId", "seq"},
	expandReleaseCondition: true,
	filterableFields:       map[string]string{"card_id": "cardId"},
}

var actionSetsConfig = lookupResourceConfig{
	entity:                 "actionsets",
	queryErrorCode:         "ACTION_SET_QUERY_ERROR",
	notFoundCode:           "ACTION_SET_NOT_FOUND",
	resourceLabel:          "action set",
	sortableFields:         []string{"id", "areaId"},
	expandReleaseCondition: true,
}

var specialStoriesConfig = lookupResourceConfig{
	entity:         "specialstories",
	queryErrorCode: "SPECIAL_STORY_QUERY_ERROR",
	notFoundCode:   "SPECIAL_STORY_NOT_FOUND",
	resourceLabel:  "special story",
	sortableFields: []string{"id", "seq", "startAt", "endAt"},
}

var character2DsConfig = lookupResourceConfig{
	entity:         "character2ds",
	queryErrorCode: "CHARACTER_2D_QUERY_ERROR",
	notFoundCode:   "CHARACTER_2D_NOT_FOUND",
	resourceLabel:  "character 2D",
	sortableFields: []string{"id", "characterId"},
}

var mobCharactersConfig = lookupResourceConfig{
	entity:         "mobcharacters",
	queryErrorCode: "MOB_CHARACTER_QUERY_ERROR",
	notFoundCode:   "MOB_CHARACTER_NOT_FOUND",
	resourceLabel:  "mob character",
	sortableFields: []string{"id", "seq"},
}

var subGameCharactersConfig = lookupResourceConfig{
	entity:         "subgamecharacters",
	queryErrorCode: "SUB_GAME_CHARACTER_QUERY_ERROR",
	notFoundCode:   "SUB_GAME_CHARACTER_NOT_FOUND",
	resourceLabel:  "sub game character",
	sortableFields: []string{"id", "seq"},
}

var unitStoryEpisodeGroupsConfig = lookupResourceConfig{
	entity:         "unitstoryepisodegroups",
	queryErrorCode: "UNIT_STORY_EPISODE_GROUP_QUERY_ERROR",
	notFoundCode:   "UNIT_STORY_EPISODE_GROUP_NOT_FOUND",
	resourceLabel:  "unit story episode group",
	sortableFields: []string{"id", "unit", "unitEpisodeCategory"},
}

var areasConfig = lookupResourceConfig{
	entity:         "areas",
	queryErrorCode: "AREA_QUERY_ERROR",
	notFoundCode:   "AREA_NOT_FOUND",
	resourceLabel:  "area",
	sortableFields: []string{"id", "groupId", "name"},
}

func NewLookupHandler(masterDataSync *usecase.MasterDataSyncUsecase) *LookupHandler {
	return &LookupHandler{masterDataSync: masterDataSync}
}

// UnitProfilesByUnit godoc
// @Summary Get unit profile by unit
// @Tags unitProfiles
// @Produce json
// @Param region path string true "Region"
// @Param unit path string true "Unit"
// @Success 200 {object} shared.UnitProfileObjectResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /unitProfiles/{region}/{unit} [get]
func (handler *LookupHandler) UnitProfilesByUnit(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	unit := strings.TrimSpace(c.Param("unit"))
	if region == "" || unit == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region and unit are required")
		return
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, unitProfilesConfig.entity) {
		return
	}

	record, found, err := handler.findUnitProfileByUnit(c.Request.Context(), region, unit)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, unitProfilesConfig.queryErrorCode, "failed to query "+unitProfilesConfig.resourceLabel)
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, unitProfilesConfig.notFoundCode, unitProfilesConfig.resourceLabel+" not found")
		return
	}

	response.JSON(c, http.StatusOK, record)
}

// UnitProfileMembers godoc
// @Summary List members for a unit profile
// @Tags unitProfiles
// @Produce json
// @Param region path string true "Region"
// @Param unit path string true "Unit"
// @Success 200 {object} shared.UnitProfileMembersResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /unitProfiles/{region}/{unit}/members [get]
func (handler *LookupHandler) UnitProfileMembers(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	unit := strings.TrimSpace(c.Param("unit"))
	if region == "" || unit == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region and unit are required")
		return
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, unitProfilesConfig.entity) {
		return
	}

	_, found, err := handler.findUnitProfileByUnit(c.Request.Context(), region, unit)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, unitProfilesConfig.queryErrorCode, "failed to query "+unitProfilesConfig.resourceLabel)
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, unitProfilesConfig.notFoundCode, unitProfilesConfig.resourceLabel+" not found")
		return
	}

	items, err := handler.unitProfileMembers(c.Request.Context(), region, unit)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, gameCharacterUnitsConfig.queryErrorCode, "failed to query unit profile members")
		return
	}

	response.JSON(c, http.StatusOK, shared.UnitProfileMembersResponse{Items: items})
}

// UnitProfilesAvailableRegionsByUnit godoc
// @Summary Get available regions for a unit profile unit
// @Tags unitProfiles
// @Produce json
// @Param unit path string true "Unit"
// @Success 200 {object} shared.RegionAvailabilityResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /unitProfiles/regions/{unit}/availability [get]
func (handler *LookupHandler) UnitProfilesAvailableRegionsByUnit(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	unit := strings.TrimSpace(c.Param("unit"))
	if unit == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "unit is required")
		return
	}

	readyRegions, err := shared.RuntimeSearchIndexReadyRegions(c.Request.Context(), handler.masterDataSync)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, unitProfilesConfig.queryErrorCode, "failed to query "+unitProfilesConfig.resourceLabel+" available regions")
		return
	}

	regions := make([]string, 0, len(readyRegions))
	for _, region := range readyRegions {
		_, found, err := handler.findUnitProfileByUnit(c.Request.Context(), region, unit)
		if err != nil {
			response.Error(c, http.StatusInternalServerError, unitProfilesConfig.queryErrorCode, "failed to query "+unitProfilesConfig.resourceLabel+" available regions")
			return
		}
		if found {
			regions = append(regions, region)
		}
	}

	response.JSON(c, http.StatusOK, gin.H{"regions": regions})
}

// UnitProfilesList godoc
// @Summary List unit profiles by page
// @Tags unitProfiles
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param spoiler query bool false "Include spoiler content"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.UnitProfileListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /unitProfiles/{region}/list [get]
func (handler *LookupHandler) UnitProfilesList(c *gin.Context) {
	handler.list(c, unitProfilesConfig)
}

// GameCharacterUnitsByID godoc
// @Summary Get game character unit by id
// @Tags gameCharacterUnits
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Game Character Unit ID"
// @Success 200 {object} shared.GameCharacterUnitObjectResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /gameCharacterUnits/{region}/{id} [get]
func (handler *LookupHandler) GameCharacterUnitsByID(c *gin.Context) {
	handler.byID(c, gameCharacterUnitsConfig)
}

// GameCharacterUnitsAvailableRegionsByID godoc
// @Summary Get available regions for a game character unit id
// @Tags gameCharacterUnits
// @Produce json
// @Param id path string true "Game Character Unit ID"
// @Success 200 {object} shared.RegionAvailabilityResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /gameCharacterUnits/regions/{id}/availability [get]
func (handler *LookupHandler) GameCharacterUnitsAvailableRegionsByID(c *gin.Context) {
	handler.availableRegionsByID(c, gameCharacterUnitsConfig)
}

// GameCharacterUnitsList godoc
// @Summary List game character units by page
// @Tags gameCharacterUnits
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param spoiler query bool false "Include spoiler content"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.GameCharacterUnitListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /gameCharacterUnits/{region}/list [get]
func (handler *LookupHandler) GameCharacterUnitsList(c *gin.Context) {
	handler.list(c, gameCharacterUnitsConfig)
}

// GameCharactersByID godoc
// @Summary Get game character by id
// @Tags gameCharacters
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Game Character ID"
// @Success 200 {object} shared.GameCharacterObjectResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /gameCharacters/{region}/{id} [get]
func (handler *LookupHandler) GameCharactersByID(c *gin.Context) {
	handler.byID(c, gameCharactersConfig)
}

// GameCharacterProfilesByID godoc
// @Summary Get game character profile by character id
// @Tags gameCharacters
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Game Character ID"
// @Success 200 {object} shared.GameCharacterProfileResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /gameCharacters/{region}/{id}/profile [get]
func (handler *LookupHandler) GameCharacterProfilesByID(c *gin.Context) {
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
	characterID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || characterID <= 0 {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "id must be a positive integer")
		return
	}
	id = strconv.FormatInt(characterID, 10)
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, gameCharacterProfilesConfig.entity) {
		return
	}

	records, err := handler.masterDataSync.ListAll(c.Request.Context(), region, gameCharacterProfilesConfig.entity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, gameCharacterProfilesConfig.queryErrorCode, "failed to query "+gameCharacterProfilesConfig.resourceLabel)
		return
	}

	for _, record := range records {
		if shared.NormalizeAnyID(record["characterId"]) != id {
			continue
		}

		response.JSON(c, http.StatusOK, shared.GameCharacterProfileResponse{
			Birthday:       shared.NormalizeAnyID(record["birthday"]),
			CharacterVoice: shared.NormalizeAnyID(record["characterVoice"]),
			FavoriteFood:   shared.NormalizeAnyID(record["favoriteFood"]),
			HatedFood:      shared.NormalizeAnyID(record["hatedFood"]),
			Height:         shared.NormalizeAnyID(record["height"]),
			Hobby:          shared.NormalizeAnyID(record["hobby"]),
			Introduction:   shared.NormalizeAnyID(record["introduction"]),
			School:         shared.NormalizeAnyID(record["school"]),
			SchoolYear:     shared.NormalizeAnyID(record["schoolYear"]),
			SpecialSkill:   shared.NormalizeAnyID(record["specialSkill"]),
			Weak:           shared.NormalizeAnyID(record["weak"]),
		})
		return
	}

	response.Error(c, http.StatusNotFound, gameCharacterProfilesConfig.notFoundCode, gameCharacterProfilesConfig.resourceLabel+" not found")
}

// GameCharactersAvailableRegionsByID godoc
// @Summary Get available regions for a game character id
// @Tags gameCharacters
// @Produce json
// @Param id path string true "Game Character ID"
// @Success 200 {object} shared.RegionAvailabilityResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /gameCharacters/regions/{id}/availability [get]
func (handler *LookupHandler) GameCharactersAvailableRegionsByID(c *gin.Context) {
	handler.availableRegionsByID(c, gameCharactersConfig)
}

// GameCharactersList godoc
// @Summary List game characters by page
// @Tags gameCharacters
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param spoiler query bool false "Include spoiler content"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.GameCharacterListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /gameCharacters/{region}/list [get]
func (handler *LookupHandler) GameCharactersList(c *gin.Context) {
	handler.list(c, gameCharactersConfig)
}

// WorldBloomsList godoc
// @Summary List world blooms by page
// @Tags worldBlooms
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param spoiler query bool false "Include spoiler content"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.WorldBloomListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /worldBlooms/{region}/list [get]
func (handler *LookupHandler) WorldBloomsList(c *gin.Context) {
	handler.list(c, worldBloomsConfig)
}

// UnitStoriesList godoc
// @Summary List unit stories by page
// @Tags unitStories
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param spoiler query bool false "Include spoiler content"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.GenericRecordListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /unitStories/{region}/list [get]
func (handler *LookupHandler) UnitStoriesList(c *gin.Context) {
	handler.list(c, unitStoriesConfig)
}

// EventStoriesList godoc
// @Summary List event stories by page
// @Tags eventStories
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param spoiler query bool false "Include spoiler content"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Param event_id query string false "Comma-separated event ids to keep"
// @Success 200 {object} shared.GenericRecordListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /eventStories/{region}/list [get]
func (handler *LookupHandler) EventStoriesList(c *gin.Context) {
	handler.list(c, eventStoriesConfig)
}

// CharacterProfilesList godoc
// @Summary List character profiles by page
// @Tags characterProfiles
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param spoiler query bool false "Include spoiler content"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.GenericRecordListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /characterProfiles/{region}/list [get]
func (handler *LookupHandler) CharacterProfilesList(c *gin.Context) {
	handler.list(c, characterProfilesConfig)
}

// CardEpisodesList godoc
// @Summary List card episodes by page
// @Tags cardEpisodes
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param spoiler query bool false "Include spoiler content"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Param card_id query string false "Comma-separated card ids to keep"
// @Success 200 {object} shared.GenericRecordListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /cardEpisodes/{region}/list [get]
func (handler *LookupHandler) CardEpisodesList(c *gin.Context) {
	handler.list(c, cardEpisodesConfig)
}

// ActionSetsList godoc
// @Summary List action sets by page
// @Tags actionSets
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param spoiler query bool false "Include spoiler content"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.GenericRecordListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /actionSets/{region}/list [get]
func (handler *LookupHandler) ActionSetsList(c *gin.Context) {
	handler.list(c, actionSetsConfig)
}

// SpecialStoriesList godoc
// @Summary List special stories by page
// @Tags specialStories
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param spoiler query bool false "Include spoiler content"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.GenericRecordListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /specialStories/{region}/list [get]
func (handler *LookupHandler) SpecialStoriesList(c *gin.Context) {
	handler.list(c, specialStoriesConfig)
}

// Character2DsList godoc
// @Summary List character 2Ds by page
// @Tags character2ds
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param spoiler query bool false "Include spoiler content"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.GenericRecordListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /character2ds/{region}/list [get]
func (handler *LookupHandler) Character2DsList(c *gin.Context) {
	handler.list(c, character2DsConfig)
}

// MobCharactersList godoc
// @Summary List mob characters by page
// @Tags mobCharacters
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param spoiler query bool false "Include spoiler content"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.GenericRecordListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /mobCharacters/{region}/list [get]
func (handler *LookupHandler) MobCharactersList(c *gin.Context) {
	handler.list(c, mobCharactersConfig)
}

// SubGameCharactersList godoc
// @Summary List sub game characters by page
// @Tags subGameCharacters
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param spoiler query bool false "Include spoiler content"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.GenericRecordListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /subGameCharacters/{region}/list [get]
func (handler *LookupHandler) SubGameCharactersList(c *gin.Context) {
	handler.list(c, subGameCharactersConfig)
}

// UnitStoryEpisodeGroupsList godoc
// @Summary List unit story episode groups by page
// @Tags unitStoryEpisodeGroups
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param spoiler query bool false "Include spoiler content"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.GenericRecordListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /unitStoryEpisodeGroups/{region}/list [get]
func (handler *LookupHandler) UnitStoryEpisodeGroupsList(c *gin.Context) {
	handler.list(c, unitStoryEpisodeGroupsConfig)
}

// AreasList godoc
// @Summary List areas by page
// @Tags areas
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param spoiler query bool false "Include spoiler content"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.GenericRecordListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /areas/{region}/list [get]
func (handler *LookupHandler) AreasList(c *gin.Context) {
	handler.list(c, areasConfig)
}

func (handler *LookupHandler) byID(c *gin.Context, config lookupResourceConfig) {
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, config.entity) {
		return
	}

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, config.entity, id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, config.queryErrorCode, "failed to query "+config.resourceLabel)
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, config.notFoundCode, config.resourceLabel+" not found")
		return
	}

	response.JSON(c, http.StatusOK, record)
}

func (handler *LookupHandler) availableRegionsByID(c *gin.Context, config lookupResourceConfig) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "id is required")
		return
	}

	regions, err := shared.AvailableRegionsByID(c.Request.Context(), handler.masterDataSync, config.entity, id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, config.queryErrorCode, "failed to query "+config.resourceLabel+" available regions")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{"regions": regions})
}

func (handler *LookupHandler) list(c *gin.Context, config lookupResourceConfig) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	if region == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region is required")
		return
	}
	page, pageSize, ok := parseLookupPagination(c)
	if !ok {
		return
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, config.entity) {
		return
	}

	sortOptions, ok := shared.ParseListSortOptions(c)
	if !ok {
		return
	}

	includeSpoilers, ok := shared.ParseSpoilerOption(c)
	if !ok {
		return
	}

	recordFilters, ok := shared.ParseRecordFilters(c, config.filterableFields)
	if !ok {
		return
	}

	// Filtered requests bypass the database paging fast path: filtering and
	// paging must apply to the same in-memory record set.
	if !includeSpoilers || sortOptions.Enabled || len(recordFilters) > 0 {
		records, err := handler.masterDataSync.ListAll(c.Request.Context(), region, config.entity)
		if err != nil {
			response.Error(c, http.StatusInternalServerError, config.queryErrorCode, "failed to list "+config.resourceLabel+"s")
			return
		}
		if !includeSpoilers {
			records = shared.FilterSpoilerItems(records, time.Now().UTC())
		}
		if sortOptions.Enabled {
			if !shared.ValidateSortField(c, sortOptions.Field, records, config.sortableFields) {
				return
			}
			shared.SortResponseItems(records, sortOptions.Field, sortOptions.Descending)
		}
		if len(recordFilters) > 0 {
			records = shared.FilterRecordsByNumbers(records, recordFilters)
		}
		pagedRecords, pagination := shared.PaginateItems(records, page, pageSize)
		response.JSON(c, http.StatusOK, gin.H{
			"items":      handler.expandRecords(c.Request.Context(), region, config, pagedRecords),
			"pagination": pagination,
		})
		return
	}

	records, total, err := handler.masterDataSync.ListByPage(c.Request.Context(), region, config.entity, page, pageSize)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, config.queryErrorCode, "failed to list "+config.resourceLabel+"s")
		return
	}

	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}

	response.JSON(c, http.StatusOK, gin.H{
		"items": handler.expandRecords(c.Request.Context(), region, config, records),
		"pagination": gin.H{
			"page":        page,
			"page_size":   pageSize,
			"total":       total,
			"total_pages": totalPages,
			"has_next":    page < totalPages,
		},
	})
}

// expandRecords expands the top-level releaseConditionId on each record of
// the current page only, so release condition lookups stay bounded by the
// page size even for large collections.
func (handler *LookupHandler) expandRecords(ctx context.Context, region string, config lookupResourceConfig, records []map[string]any) []map[string]any {
	if !config.expandReleaseCondition {
		return records
	}

	expanded := make([]map[string]any, 0, len(records))
	for _, record := range records {
		expanded = append(expanded, shared.BuildRecordWithReleaseCondition(ctx, handler.masterDataSync, region, record))
	}
	return expanded
}

func (handler *LookupHandler) ensureRegionReady(c *gin.Context, region string) bool {
	if handler == nil || handler.masterDataSync == nil {
		return true
	}

	readyRegions, err := shared.RuntimeSearchIndexReadyRegions(c.Request.Context(), handler.masterDataSync)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MASTER_DATA_STATUS_ERROR", "failed to check master data sync status")
		return false
	}

	normalizedRegion := strings.ToLower(strings.TrimSpace(region))
	for _, readyRegion := range readyRegions {
		if readyRegion == normalizedRegion {
			return true
		}
	}

	response.Error(c, http.StatusServiceUnavailable, "REGION_DATA_NOT_READY", "region data is updating or unavailable, please try again later")
	return false
}

func (handler *LookupHandler) findUnitProfileByUnit(ctx context.Context, region string, unit string) (map[string]any, bool, error) {
	records, err := handler.masterDataSync.ListAll(ctx, region, unitProfilesConfig.entity)
	if err != nil {
		return nil, false, err
	}

	targetUnit := shared.NormalizeComparableText(unit)
	for _, record := range records {
		if shared.NormalizeComparableText(record["unit"]) == targetUnit {
			return record, true, nil
		}
	}

	return nil, false, nil
}

func (handler *LookupHandler) unitProfileMembers(ctx context.Context, region string, unit string) ([]shared.UnitProfileMemberResponse, error) {
	characterRecords, err := handler.masterDataSync.ListAll(ctx, region, gameCharactersConfig.entity)
	if err != nil {
		return nil, err
	}
	charactersByID := make(map[string]map[string]any, len(characterRecords))
	for _, character := range characterRecords {
		if characterID := shared.NormalizeAnyID(character["id"]); characterID != "" {
			charactersByID[characterID] = character
		}
	}

	membershipRecords, err := handler.masterDataSync.ListAll(ctx, region, gameCharacterUnitsConfig.entity)
	if err != nil {
		return nil, err
	}
	targetUnit := shared.NormalizeComparableText(unit)
	items := make([]shared.UnitProfileMemberResponse, 0)
	for _, membership := range membershipRecords {
		if shared.NormalizeComparableText(membership["unit"]) != targetUnit {
			continue
		}

		gameCharacterID, err := strconv.ParseInt(shared.NormalizeAnyID(membership["gameCharacterId"]), 10, 64)
		if err != nil || gameCharacterID <= 0 {
			continue
		}
		character, found := charactersByID[strconv.FormatInt(gameCharacterID, 10)]
		if !found {
			continue
		}

		items = append(items, shared.UnitProfileMemberResponse{
			ID:               membership["id"],
			GameCharacterID:  gameCharacterID,
			Unit:             membership["unit"],
			ColorCode:        membership["colorCode"],
			FirstName:        character["firstName"],
			GivenName:        character["givenName"],
			FirstNameEnglish: character["firstNameEnglish"],
			GivenNameEnglish: character["givenNameEnglish"],
			ResourceID:       character["resourceId"],
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		return items[i].GameCharacterID < items[j].GameCharacterID
	})
	return items, nil
}
