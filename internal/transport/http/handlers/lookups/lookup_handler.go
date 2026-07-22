package lookups

import (
	"context"
	"net/http"
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
}

type lookupResourceConfig struct {
	entity         string
	queryErrorCode string
	notFoundCode   string
	resourceLabel  string
	sortableFields []string
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

	readyRegions, err := shared.ReadyMasterDataRegions(c.Request.Context(), handler.masterDataSync)
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
// @Param page query int false "Page number"
// @Param page_size query int false "Page size"
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
// @Param page query int false "Page number"
// @Param page_size query int false "Page size"
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
// @Param page query int false "Page number"
// @Param page_size query int false "Page size"
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, config.entity) {
		return
	}

	page := 1
	if rawPage := strings.TrimSpace(c.Query("page")); rawPage != "" {
		parsedPage, err := strconv.Atoi(rawPage)
		if err != nil || parsedPage <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "page must be a positive integer")
			return
		}
		page = parsedPage
	}

	pageSize := 20
	if rawPageSize := strings.TrimSpace(c.Query("page_size")); rawPageSize != "" {
		parsedPageSize, err := strconv.Atoi(rawPageSize)
		if err != nil || parsedPageSize <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "page_size must be a positive integer")
			return
		}
		pageSize = parsedPageSize
	}

	sortOptions, ok := shared.ParseListSortOptions(c)
	if !ok {
		return
	}

	includeSpoilers, ok := shared.ParseSpoilerOption(c)
	if !ok {
		return
	}

	if !includeSpoilers || sortOptions.Enabled {
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
		pagedRecords, pagination := shared.PaginateItems(records, page, pageSize)
		response.JSON(c, http.StatusOK, gin.H{
			"items":      pagedRecords,
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
		"items": records,
		"pagination": gin.H{
			"page":        page,
			"page_size":   pageSize,
			"total":       total,
			"total_pages": totalPages,
			"has_next":    page < totalPages,
		},
	})
}

func (handler *LookupHandler) ensureRegionReady(c *gin.Context, region string) bool {
	if handler == nil || handler.masterDataSync == nil {
		return true
	}

	readyRegions, err := shared.ReadyMasterDataRegions(c.Request.Context(), handler.masterDataSync)
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
