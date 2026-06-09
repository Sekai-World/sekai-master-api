package cards

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/logging"
	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type CardHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
}

var sortableCardFields = []string{
	"id",
	"seq",
	"attr",
	"supportUnit",
	"cardSkillName",
	"prefix",
	"assetbundleName",
	"gachaPhrase",
	"flavorText",
	"releaseAt",
	"archivePublishedAt",
	"initialSpecialTrainingStatus",
}

func NewCardHandler(masterDataSync *usecase.MasterDataSyncUsecase) *CardHandler {
	return &CardHandler{masterDataSync: masterDataSync}
}

// ByID godoc
// @Summary Get card basic info by id
// @Tags cards
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Card ID"
// @Success 200 {object} shared.CardObjectResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /cards/{region}/{id} [get]
func (handler *CardHandler) ByID(c *gin.Context) {
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
	if !handler.ensureRegionReady(c, region) {
		return
	}

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "cards", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to query card")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "CARD_NOT_FOUND", "card not found")
		return
	}

	response.JSON(c, http.StatusOK, handler.buildCardBase(c.Request.Context(), region, record))
}

// AvailableRegionsByID godoc
// @Summary Get available regions for a card id
// @Tags cards
// @Produce json
// @Param id path string true "Card ID"
// @Success 200 {object} shared.RegionAvailabilityResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /cards/regions/{id}/availability [get]
func (handler *CardHandler) AvailableRegionsByID(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "id is required")
		return
	}

	regions, err := shared.AvailableRegionsByID(c.Request.Context(), handler.masterDataSync, "cards", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to query card available regions")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{"regions": regions})
}

// ParamsByID godoc
// @Summary Get card params by id
// @Tags cards
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Card ID"
// @Success 200 {object} shared.CardParamsResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /cards/{region}/{id}/params [get]
func (handler *CardHandler) ParamsByID(c *gin.Context) {
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
	if !handler.ensureRegionReady(c, region) {
		return
	}

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "cards", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to query card params")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "CARD_NOT_FOUND", "card not found")
		return
	}

	response.JSON(c, http.StatusOK, buildCardParams(record))
}

// EpisodesByID godoc
// @Summary Get card episodes by card id
// @Tags cards
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Card ID"
// @Success 200 {object} shared.RecordItemsResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /cards/{region}/{id}/episodes [get]
func (handler *CardHandler) EpisodesByID(c *gin.Context) {
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
	if !handler.ensureRegionReady(c, region) {
		return
	}

	_, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "cards", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to query card")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "CARD_NOT_FOUND", "card not found")
		return
	}

	matches, err := handler.masterDataSync.Search(c.Request.Context(), region, "cardepisodes", id, []string{"cardId"}, 1000000)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to query card episodes")
		return
	}

	items := make([]map[string]any, 0, len(matches))
	targetCardID := shared.NormalizeAnyID(id)
	for _, match := range matches {
		if shared.NormalizeAnyID(match.Item["cardId"]) != targetCardID {
			continue
		}
		items = append(items, shared.BuildRecordWithReleaseCondition(c.Request.Context(), handler.masterDataSync, region, match.Item))
	}

	response.JSON(c, http.StatusOK, gin.H{
		"items": items,
	})
}

// List godoc
// @Summary List cards by page
// @Tags cards
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number"
// @Param page_size query int false "Page size"
// @Param spoiler query bool false "Include spoiler content"
// @Param unit query string false "Comma-separated character units"
// @Param character query string false "Comma-separated character IDs"
// @Param skill query string false "Comma-separated skill descriptionSpriteName values"
// @Param type query string false "Comma-separated card supply IDs or card supply types"
// @Param attr query string false "Comma-separated card attributes"
// @Param rarity query string false "Comma-separated card rarity types"
// @Param supportUnit query string false "Comma-separated support units"
// @Param has3dmvCutIn query bool false "Include cards that have another3dmvCutIns entries"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.CardListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /cards/{region}/list [get]
func (handler *CardHandler) List(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	if region == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region is required")
		return
	}
	if !handler.ensureRegionReady(c, region) {
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

	filterOptions, ok := parseCardListFilterOptions(c)
	if !ok {
		return
	}

	if !includeSpoilers || sortOptions.Enabled || filterOptions.Enabled() {
		records, err := handler.masterDataSync.ListAll(c.Request.Context(), region, "cards")
		if err != nil {
			response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to list cards")
			return
		}
		if !includeSpoilers {
			records = shared.FilterSpoilerItems(records, time.Now().UTC())
		}
		if filterOptions.Enabled() {
			records, err = handler.filterCards(c.Request.Context(), region, records, filterOptions)
			if err != nil {
				response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to filter cards")
				return
			}
		}
		if sortOptions.Enabled {
			if !shared.ValidateSortField(c, sortOptions.Field, records, sortableCardFields) {
				return
			}
			shared.SortResponseItems(records, sortOptions.Field, sortOptions.Descending)
		}
		pagedRecords, pagination := shared.PaginateItems(records, page, pageSize)
		items := make([]map[string]any, 0, len(pagedRecords))
		for _, record := range pagedRecords {
			items = append(items, handler.buildCardBase(c.Request.Context(), region, record))
		}
		response.JSON(c, http.StatusOK, gin.H{
			"items":      items,
			"pagination": pagination,
		})
		return
	}

	records, total, err := handler.masterDataSync.ListByPage(c.Request.Context(), region, "cards", page, pageSize)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to list cards")
		return
	}

	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, handler.buildCardBase(c.Request.Context(), region, record))
	}

	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	hasNext := page < totalPages

	response.JSON(c, http.StatusOK, gin.H{
		"items": items,
		"pagination": gin.H{
			"page":        page,
			"page_size":   pageSize,
			"total":       total,
			"total_pages": totalPages,
			"has_next":    hasNext,
		},
	})
}

type cardListFilterOptions struct {
	Units        map[string]struct{}
	Characters   map[string]struct{}
	Skills       map[string]struct{}
	Types        map[string]struct{}
	Attrs        map[string]struct{}
	Rarities     map[string]struct{}
	SupportUnits map[string]struct{}
	Has3dmvCutIn bool
}

func (options cardListFilterOptions) Enabled() bool {
	return len(options.Units) > 0 ||
		len(options.Characters) > 0 ||
		len(options.Skills) > 0 ||
		len(options.Types) > 0 ||
		len(options.Attrs) > 0 ||
		len(options.Rarities) > 0 ||
		len(options.SupportUnits) > 0 ||
		options.Has3dmvCutIn
}

func parseCardListFilterOptions(c *gin.Context) (cardListFilterOptions, bool) {
	options := cardListFilterOptions{
		Units:        parseCardListQuerySet(c, "unit"),
		Characters:   parseCardListQuerySet(c, "character"),
		Skills:       parseCardListQuerySet(c, "skill"),
		Types:        parseCardListQuerySet(c, "type"),
		Attrs:        parseCardListQuerySet(c, "attr"),
		Rarities:     parseCardListQuerySet(c, "rarity"),
		SupportUnits: parseCardListQuerySet(c, "supportUnit"),
	}

	has3dmvCutIn, ok := parseCardListBool(c, "has3dmvCutIn")
	if !ok {
		return cardListFilterOptions{}, false
	}

	options.Has3dmvCutIn = has3dmvCutIn
	return options, true
}

func parseCardListQuerySet(c *gin.Context, key string) map[string]struct{} {
	values := c.QueryArray(key)
	if len(values) == 0 {
		return nil
	}

	result := map[string]struct{}{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			normalized := shared.NormalizeComparableText(part)
			if normalized == "" {
				continue
			}
			result[normalized] = struct{}{}
		}
	}
	if len(result) == 0 {
		return nil
	}

	return result
}

func parseCardListBool(c *gin.Context, key string) (bool, bool) {
	rawValue := strings.TrimSpace(c.Query(key))
	if rawValue == "" {
		return false, true
	}

	parsed, err := strconv.ParseBool(rawValue)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", key+" must be a boolean")
		return false, false
	}

	return parsed, true
}

func (handler *CardHandler) filterCards(ctx context.Context, region string, records []map[string]any, options cardListFilterOptions) ([]map[string]any, error) {
	if len(records) == 0 || !options.Enabled() {
		return records, nil
	}

	var characterUnits map[string]string
	if len(options.Units) > 0 {
		var err error
		characterUnits, err = handler.loadCharacterUnits(ctx, region)
		if err != nil {
			return nil, err
		}
	}

	var skillTypes map[string]string
	if len(options.Skills) > 0 {
		var err error
		skillTypes, err = handler.loadSkillTypes(ctx, region)
		if err != nil {
			return nil, err
		}
	}

	var cardSupplyTypes map[string]string
	if len(options.Types) > 0 {
		var err error
		cardSupplyTypes, err = handler.loadCardSupplyTypes(ctx, region)
		if err != nil {
			return nil, err
		}
	}

	var cutInCardIDs map[string]struct{}
	if options.Has3dmvCutIn {
		var err error
		cutInCardIDs, err = handler.load3dmvCutInCardIDs(ctx, region)
		if err != nil {
			return nil, err
		}
	}

	filtered := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if !cardMatchesFilterOptions(record, options, characterUnits, skillTypes, cardSupplyTypes, cutInCardIDs) {
			continue
		}
		filtered = append(filtered, record)
	}

	return filtered, nil
}

func (handler *CardHandler) loadCharacterUnits(ctx context.Context, region string) (map[string]string, error) {
	characters, err := handler.masterDataSync.ListAll(ctx, region, "gamecharacters")
	if err != nil {
		return nil, err
	}

	units := make(map[string]string, len(characters))
	for _, character := range characters {
		id := shared.NormalizeAnyID(character["id"])
		if id == "" {
			continue
		}
		units[id] = shared.NormalizeComparableText(character["unit"])
	}

	return units, nil
}

func (handler *CardHandler) loadSkillTypes(ctx context.Context, region string) (map[string]string, error) {
	skills, err := handler.masterDataSync.ListAll(ctx, region, "skills")
	if err != nil {
		return nil, err
	}

	types := make(map[string]string, len(skills))
	for _, skill := range skills {
		id := shared.NormalizeAnyID(skill["id"])
		if id == "" {
			continue
		}
		types[id] = shared.NormalizeComparableText(skill["descriptionSpriteName"])
	}

	return types, nil
}

func (handler *CardHandler) loadCardSupplyTypes(ctx context.Context, region string) (map[string]string, error) {
	cardSupplies, err := handler.masterDataSync.ListAll(ctx, region, "cardsupplies")
	if err != nil {
		return nil, err
	}

	types := make(map[string]string, len(cardSupplies))
	for _, cardSupply := range cardSupplies {
		id := shared.NormalizeAnyID(cardSupply["id"])
		if id == "" {
			continue
		}
		types[id] = shared.NormalizeComparableText(cardSupply["cardSupplyType"])
	}

	return types, nil
}

func (handler *CardHandler) load3dmvCutInCardIDs(ctx context.Context, region string) (map[string]struct{}, error) {
	cutIns, err := handler.masterDataSync.ListAll(ctx, region, "another3dmvcutins")
	if err != nil {
		return nil, err
	}

	cardIDs := make(map[string]struct{}, len(cutIns))
	for _, cutIn := range cutIns {
		cardID := shared.NormalizeAnyID(cutIn["cardId"])
		if cardID == "" {
			continue
		}
		cardIDs[cardID] = struct{}{}
	}

	return cardIDs, nil
}

func cardMatchesFilterOptions(record map[string]any, options cardListFilterOptions, characterUnits map[string]string, skillTypes map[string]string, cardSupplyTypes map[string]string, cutInCardIDs map[string]struct{}) bool {
	characterID := shared.NormalizeComparableText(record["characterId"])
	if len(options.Characters) > 0 && !setContains(options.Characters, characterID) {
		return false
	}

	if len(options.Units) > 0 && !setContains(options.Units, characterUnits[shared.NormalizeAnyID(record["characterId"])]) {
		return false
	}

	if len(options.Attrs) > 0 && !setContains(options.Attrs, shared.NormalizeComparableText(record["attr"])) {
		return false
	}

	if len(options.Rarities) > 0 {
		rarityType := shared.NormalizeComparableText(record["cardRarityType"])
		rarityNumber := strings.TrimPrefix(rarityType, "rarity_")
		if !setContains(options.Rarities, rarityType) && !setContains(options.Rarities, rarityNumber) {
			return false
		}
	}

	if len(options.SupportUnits) > 0 && !setContains(options.SupportUnits, shared.NormalizeComparableText(record["supportUnit"])) {
		return false
	}

	if len(options.Skills) > 0 {
		skillID := shared.NormalizeAnyID(record["skillId"])
		if !setContains(options.Skills, skillTypes[skillID]) {
			return false
		}
	}

	if len(options.Types) > 0 {
		cardSupplyID := shared.NormalizeAnyID(record["cardSupplyId"])
		if !setContains(options.Types, shared.NormalizeComparableText(cardSupplyID)) && !setContains(options.Types, cardSupplyTypes[cardSupplyID]) {
			return false
		}
	}

	if options.Has3dmvCutIn {
		if _, ok := cutInCardIDs[shared.NormalizeAnyID(record["id"])]; !ok {
			return false
		}
	}

	return true
}

func setContains(set map[string]struct{}, value string) bool {
	if value == "" {
		return false
	}
	_, ok := set[value]
	return ok
}

func (handler *CardHandler) buildCardBase(ctx context.Context, region string, record map[string]any) map[string]any {
	if record == nil {
		return map[string]any{}
	}

	keys := []string{
		"id",
		"seq",
		"attr",
		"supportUnit",
		"cardSkillName",
		"prefix",
		"assetbundleName",
		"gachaPhrase",
		"flavorText",
		"releaseAt",
		"archivePublishedAt",
		"initialSpecialTrainingStatus",
	}

	result := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := record[key]; ok {
			result[key] = value
		}
	}

	if handler == nil || handler.masterDataSync == nil {
		return result
	}

	if cardSupplyID, ok := record["cardSupplyId"]; ok {
		lookupID := shared.NormalizeAnyID(cardSupplyID)
		if lookupID == "" {
			result["cardSupply"] = nil
		} else {
			cardSupply, found, err := handler.masterDataSync.GetByID(ctx, region, "cardsupplies", lookupID)
			if err != nil || !found {
				result["cardSupply"] = nil
			} else {
				result["cardSupply"] = cardSupply
			}
		}
	}

	if skillID, ok := record["skillId"]; ok {
		skillLookupID := shared.NormalizeAnyID(skillID)
		if skillLookupID == "" {
			result["skill"] = nil
		} else {
			skill, found, err := handler.masterDataSync.GetByID(ctx, region, "skills", skillLookupID)
			if err != nil || !found {
				result["skill"] = nil
			} else {
				result["skill"] = skill
			}
		}
	}

	if characterID, ok := record["characterId"]; ok {
		characterLookupID := shared.NormalizeAnyID(characterID)
		if characterLookupID == "" {
			result["character"] = nil
		} else {
			character, found, err := handler.masterDataSync.GetByID(ctx, region, "gamecharacters", characterLookupID)
			if err != nil || !found {
				result["character"] = nil
			} else {
				result["character"] = sanitizeGameCharacter(character)
			}
		}
	}

	if cardRarityType, ok := record["cardRarityType"]; ok {
		cardID := shared.NormalizeAnyID(record["id"])
		rarityTypeLookup := shared.NormalizeComparableText(cardRarityType)
		logger := logging.FromContext(ctx)
		logger.Debugw(
			"card rarity lookup start",
			"component", "card-handler",
			"region", region,
			"card_id", cardID,
			"raw_type", cardRarityType,
			"normalized_type", rarityTypeLookup,
		)
		if rarityTypeLookup == "" {
			logger.Warnw("card rarity lookup empty type", "component", "card-handler", "region", region, "card_id", cardID)
			result["cardRarity"] = nil
		} else {
			matches, err := handler.masterDataSync.Search(ctx, region, "cardrarities", rarityTypeLookup, []string{"cardRarityType"}, 20)
			if err != nil {
				logger.Debugw("card rarity lookup search error", "component", "card-handler", "region", region, "card_id", cardID, "type", rarityTypeLookup, "error", err)
				result["cardRarity"] = nil
			} else {
				logger.Debugw("card rarity lookup search done", "component", "card-handler", "region", region, "card_id", cardID, "type", rarityTypeLookup, "matches", len(matches))
				rarity := findExactCardRarityByType(matches, rarityTypeLookup)
				if rarity == nil {
					logger.Warnw("card rarity lookup not found", "component", "card-handler", "region", region, "card_id", cardID, "type", rarityTypeLookup)
					result["cardRarity"] = nil
				} else {
					logger.Debugw("card rarity lookup found", "component", "card-handler", "region", region, "card_id", cardID, "type", rarityTypeLookup, "rarity_id", rarity["id"])
					result["cardRarity"] = rarity
				}
			}
		}
	} else {
		logging.FromContext(ctx).Warnw("card rarity lookup skipped", "component", "card-handler", "reason", "missing_card_rarity_type", "region", region, "card_id", shared.NormalizeAnyID(record["id"]))
	}

	return result
}

func (handler *CardHandler) ensureRegionReady(c *gin.Context, region string) bool {
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

func sanitizeGameCharacter(character map[string]any) map[string]any {
	if character == nil {
		return map[string]any{}
	}

	result := make(map[string]any, len(character))
	for key, value := range character {
		switch key {
		case "live2dHeightAdjustment", "figure", "breastSize", "modelName":
			continue
		default:
			result[key] = value
		}
	}

	return result
}

func findExactCardRarityByType(matches []masterdata.SearchMatch, rarityType string) map[string]any {
	if rarityType == "" || len(matches) == 0 {
		return nil
	}

	for _, match := range matches {
		candidateType := shared.NormalizeComparableText(match.Item["cardRarityType"])
		if candidateType == rarityType {
			return match.Item
		}
	}

	return nil
}

func buildCardParams(record map[string]any) map[string]any {
	if record == nil {
		return map[string]any{}
	}

	result := map[string]any{}
	for _, key := range []string{
		"id",
		"specialTrainingPower1BonusFixed",
		"specialTrainingPower2BonusFixed",
		"specialTrainingPower3BonusFixed",
		"cardParameters",
	} {
		if value, ok := record[key]; ok {
			result[key] = value
		}
	}

	return result
}
