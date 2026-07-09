package cards

import (
	"context"
	"math"
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
	if !handler.ensureRegionReadyForCardRecords(c, region) {
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
	if !handler.ensureRegionReadyForCardRecords(c, region) {
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

	params, err := handler.buildCardParams(c.Request.Context(), region, id, record)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to query card params")
		return
	}

	response.JSON(c, http.StatusOK, params)
}

// EpisodesByID godoc
// @Summary Get card episodes by card id
// @Tags cards
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Card ID"
// @Success 200 {object} shared.CardEpisodesResponse
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

// EventsByID godoc
// @Summary Get card events by card id
// @Tags cards
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Card ID"
// @Success 200 {object} shared.CardEventsResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /cards/{region}/{id}/events [get]
func (handler *CardHandler) EventsByID(c *gin.Context) {
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

	cardRecord, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "cards", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to query card")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "CARD_NOT_FOUND", "card not found")
		return
	}

	matches, err := handler.masterDataSync.Search(c.Request.Context(), region, "eventcards", id, []string{"cardId"}, 1000000)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to query card events")
		return
	}

	items := make([]map[string]any, 0, len(matches))
	targetCardID := shared.NormalizeAnyID(id)
	for _, match := range matches {
		if shared.NormalizeAnyID(match.Item["cardId"]) != targetCardID {
			continue
		}

		item := shared.BuildRecordWithReleaseCondition(c.Request.Context(), handler.masterDataSync, region, match.Item)

		eventID := shared.NormalizeAnyID(match.Item["eventId"])
		if eventID != "" {
			eventRecord, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "events", eventID)
			if err == nil && found {
				eventSummary := make(map[string]any, 7)
				for _, key := range []string{"id", "name", "eventType", "assetbundleName", "startAt", "aggregateAt", "closedAt"} {
					if value, ok := eventRecord[key]; ok {
						eventSummary[key] = value
					}
				}
				item["event"] = eventSummary
			}

			bonusMin, bonusMax, ok, err := handler.buildCardEventBonusRange(c.Request.Context(), region, eventID, cardRecord, match.Item)
			if err != nil {
				response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to query card event bonuses")
				return
			}
			if ok {
				item["finalBonusRateMin"] = normalizeBonusRateValue(bonusMin)
				item["finalBonusRateMax"] = normalizeBonusRateValue(bonusMax)
			}
		}

		items = append(items, item)
	}

	response.JSON(c, http.StatusOK, gin.H{
		"items": items,
	})
}

// GachaByID godoc
// @Summary Get card gacha banners by card id
// @Description Returns gacha banners where the specified card appears as a pickup card
// @Tags cards
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Card ID"
// @Success 200 {object} shared.CardGachaResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /cards/{region}/{id}/gachas [get]
func (handler *CardHandler) GachaByID(c *gin.Context) {
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

	allGachas, err := handler.masterDataSync.ListAll(c.Request.Context(), region, "gachas")
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "GACHA_QUERY_ERROR", "failed to query gachas")
		return
	}

	targetCardID := shared.NormalizeAnyID(id)
	gachas := make([]map[string]any, 0)

	for _, gacha := range allGachas {
		pickupsRaw, ok := gacha["gachaPickups"]
		if !ok {
			continue
		}

		pickups, ok := pickupsRaw.([]any)
		if !ok || len(pickups) == 0 {
			continue
		}

		for _, pickupRaw := range pickups {
			pickup, ok := pickupRaw.(map[string]any)
			if !ok {
				continue
			}

			if shared.NormalizeAnyID(pickup["cardId"]) == targetCardID {
				gachaSummary := make(map[string]any, 4)
				if value, ok := gacha["id"]; ok {
					gachaSummary["id"] = value
				}
				if value, ok := gacha["name"]; ok {
					gachaSummary["name"] = value
				}
				if value, ok := gacha["assetbundleName"]; ok {
					gachaSummary["assetbundleName"] = value
				}
				if value, ok := gacha["startAt"]; ok {
					gachaSummary["startAt"] = value
				}
				gachas = append(gachas, gachaSummary)
				break
			}
		}
	}

	response.JSON(c, http.StatusOK, gin.H{
		"gachas": gachas,
	})
}

func (handler *CardHandler) buildCardEventBonusRange(ctx context.Context, region string, eventID string, card map[string]any, eventCard map[string]any) (float64, float64, bool, error) {
	eventIDNumber, ok := intFromAny(eventID)
	if !ok {
		return 0, 0, false, nil
	}

	characterID, ok := intFromAny(card["characterId"])
	if !ok {
		return 0, 0, false, nil
	}

	rarityType, ok := stringFromAny(card["cardRarityType"])
	if !ok {
		return 0, 0, false, nil
	}

	baseBonus := numberFromAnyOrZero(eventCard["bonusRate"])
	deckBonus, specialBonus, err := handler.cardEventDeckBonus(ctx, region, eventIDNumber, card, characterID)
	if err != nil {
		return 0, 0, false, err
	}
	baseBonus += deckBonus + specialBonus

	rarityMin, rarityMax, ok := masterRankBonusRange(eventIDNumber, rarityType)
	if !ok {
		return 0, 0, false, nil
	}

	return baseBonus + rarityMin, baseBonus + rarityMax, true, nil
}

func (handler *CardHandler) cardEventDeckBonus(ctx context.Context, region string, eventID int, card map[string]any, characterID int) (float64, float64, error) {
	deckBonuses, err := handler.masterDataSync.ListAll(ctx, region, "eventdeckbonuses")
	if err != nil {
		return 0, 0, err
	}
	gameCharacterUnits, err := handler.masterDataSync.ListAll(ctx, region, "gamecharacterunits")
	if err != nil {
		return 0, 0, err
	}

	unitByID := make(map[int]map[string]any, len(gameCharacterUnits))
	for _, unit := range gameCharacterUnits {
		unitID, ok := intFromAny(unit["id"])
		if ok {
			unitByID[unitID] = unit
		}
	}

	cardAttr, _ := stringFromAny(card["attr"])
	cardSupportUnit, _ := stringFromAny(card["supportUnit"])
	var matchedDeckBonus float64
	matchedDeckBonusFound := false
	virtualSingerSpecialBonus := 0.0
	virtualSingerSpecialFound := false

	for _, deckBonus := range deckBonuses {
		bonusEventID, ok := intFromAny(deckBonus["eventId"])
		if !ok || bonusEventID != eventID {
			continue
		}

		deckBonusRate := numberFromAnyOrZero(deckBonus["bonusRate"])
		deckAttr, hasDeckAttr := stringFromAny(deckBonus["cardAttr"])
		gameCharacterUnitID, hasGameCharacterUnitID := intFromAny(deckBonus["gameCharacterUnitId"])

		if !hasGameCharacterUnitID {
			if hasDeckAttr && deckAttr == cardAttr && !matchedDeckBonusFound {
				matchedDeckBonus = deckBonusRate
				matchedDeckBonusFound = true
			}
			continue
		}

		unit, found := unitByID[gameCharacterUnitID]
		if !found {
			continue
		}
		unitCharacterID, ok := intFromAny(unit["gameCharacterId"])
		if !ok || unitCharacterID != characterID {
			continue
		}

		unitCode, _ := stringFromAny(unit["unit"])
		if characterID >= 21 && !virtualSingerSpecialFound && (unitCode == "piapro" || cardSupportUnit == "none") {
			virtualSingerSpecialBonus = 15
			if eventID >= 135 {
				virtualSingerSpecialBonus += 10
			}
			virtualSingerSpecialFound = true
		}

		attrMatches := !hasDeckAttr || deckAttr == cardAttr
		if !attrMatches || matchedDeckBonusFound {
			continue
		}

		if characterID < 21 || unitCode == "piapro" || cardSupportUnit == unitCode {
			matchedDeckBonus = deckBonusRate
			matchedDeckBonusFound = true
		}
	}

	return matchedDeckBonus, virtualSingerSpecialBonus, nil
}

func masterRankBonusRange(eventID int, rarityType string) (float64, float64, bool) {
	masterRankBonusVersions := []struct {
		maxEventID int
		bonuses    map[string][2]float64
	}{
		{
			maxEventID: 35,
			bonuses: map[string][2]float64{
				"rarity_1":        {0, 0},
				"rarity_2":        {0, 0},
				"rarity_3":        {0, 0},
				"rarity_4":        {0, 0},
				"rarity_birthday": {0, 0},
			},
		},
		{
			maxEventID: 53,
			bonuses: map[string][2]float64{
				"rarity_1":        {0, 0.5},
				"rarity_2":        {0, 1},
				"rarity_3":        {0, 5},
				"rarity_4":        {0, 10},
				"rarity_birthday": {0, 7.5},
			},
		},
		{
			maxEventID: 107,
			bonuses: map[string][2]float64{
				"rarity_1":        {0, 0.5},
				"rarity_2":        {0, 1},
				"rarity_3":        {0, 5},
				"rarity_4":        {0, 15},
				"rarity_birthday": {0, 10},
			},
		},
		{
			maxEventID: 999,
			bonuses: map[string][2]float64{
				"rarity_1":        {0, 0.5},
				"rarity_2":        {0, 1},
				"rarity_3":        {0, 5},
				"rarity_4":        {10, 25},
				"rarity_birthday": {5, 15},
			},
		},
	}

	for _, version := range masterRankBonusVersions {
		if eventID <= version.maxEventID {
			bonus, ok := version.bonuses[rarityType]
			return bonus[0], bonus[1], ok
		}
	}

	return 0, 0, false
}

func stringFromAny(value any) (string, bool) {
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	return text, text != ""
}

func intFromAny(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		if math.Trunc(typed) == typed {
			return int(typed), true
		}
	case float32:
		floatValue := float64(typed)
		if math.Trunc(floatValue) == floatValue {
			return int(floatValue), true
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed, true
		}
	}

	return 0, false
}

func numberFromAnyOrZero(value any) float64 {
	switch typed := value.(type) {
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case float64:
		return typed
	case float32:
		return float64(typed)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err == nil {
			return parsed
		}
	}

	return 0
}

func normalizeBonusRateValue(value float64) any {
	if math.Trunc(value) == value {
		return int(value)
	}

	return value
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
	if !handler.ensureRegionReadyForCardRecords(c, region) {
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

func (handler *CardHandler) ensureRegionReadyForCardRecords(c *gin.Context, region string) bool {
	if handler == nil || handler.masterDataSync == nil {
		return true
	}

	hasCards, err := handler.masterDataSync.HasEntityRecords(c.Request.Context(), region, "cards")
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MASTER_DATA_STATUS_ERROR", "failed to check master data sync status")
		return false
	}
	if hasCards {
		return true
	}

	return handler.ensureRegionReady(c, region)
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

func (handler *CardHandler) buildCardParams(ctx context.Context, region string, id string, record map[string]any) (map[string]any, error) {
	if record == nil {
		return map[string]any{}, nil
	}

	result := map[string]any{}
	for _, key := range []string{
		"id",
		"specialTrainingPower1BonusFixed",
		"specialTrainingPower2BonusFixed",
		"specialTrainingPower3BonusFixed",
	} {
		if value, ok := record[key]; ok {
			result[key] = value
		}
	}

	if handler == nil || handler.masterDataSync == nil {
		if value, ok := record["cardParameters"]; ok {
			result["cardParameters"] = value
		}
		return result, nil
	}

	// cardParameters is embedded in the card record; avoid Search on a
	// non-existent standalone entity which would trigger a full region index
	// rebuild (~10 s). Only fall back to Search if the field is absent.
	if value, ok := record["cardParameters"]; ok {
		result["cardParameters"] = value
	} else {
		matches, err := handler.masterDataSync.Search(ctx, region, "cardparameters", id, []string{"cardId"}, 1000000)
		if err != nil {
			return nil, err
		}

		items := make([]map[string]any, 0, len(matches))
		targetCardID := shared.NormalizeAnyID(id)
		for _, match := range matches {
			if shared.NormalizeAnyID(match.Item["cardId"]) != targetCardID {
				continue
			}
			items = append(items, match.Item)
		}

		if len(items) > 0 {
			result["cardParameters"] = items
		}
	}

	return result, nil
}

// DetailByID godoc
// @Summary Get card detail composite by id
// @Description Returns card base info, params, episodes, events, and gachas in a single response
// @Tags cards
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Card ID"
// @Success 200 {object} shared.CardDetailResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /cards/{region}/{id}/detail [get]
func (handler *CardHandler) DetailByID(c *gin.Context) {
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

	ctx := c.Request.Context()

	card, found, err := handler.masterDataSync.GetByID(ctx, region, "cards", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "CARD_QUERY_ERROR", "failed to query card")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "CARD_NOT_FOUND", "card not found")
		return
	}

	cardBase := handler.buildCardBase(ctx, region, card)

	cardParams, err := handler.buildCardParams(ctx, region, id, card)
	if err != nil {
		logging.FromContext(ctx).Warnw("card detail params error", "component", "card-handler", "region", region, "card_id", id, "error", err)
		cardParams = nil
	}

	episodes := handler.buildCardDetailEpisodes(ctx, region, id)

	events := handler.buildCardDetailEvents(ctx, region, id, card)

	gachas := handler.buildCardDetailGachas(ctx, region, id)

	response.JSON(c, http.StatusOK, gin.H{
		"card":     cardBase,
		"params":   cardParams,
		"episodes": episodes,
		"events":   events,
		"gachas":   gachas,
	})
}

func (handler *CardHandler) buildCardDetailEpisodes(ctx context.Context, region string, id string) []map[string]any {
	matches, err := handler.masterDataSync.Search(ctx, region, "cardepisodes", id, []string{"cardId"}, 1000000)
	if err != nil {
		return nil
	}

	targetCardID := shared.NormalizeAnyID(id)
	items := make([]map[string]any, 0, len(matches))
	for _, match := range matches {
		if shared.NormalizeAnyID(match.Item["cardId"]) != targetCardID {
			continue
		}
		items = append(items, shared.BuildRecordWithReleaseCondition(ctx, handler.masterDataSync, region, match.Item))
	}
	return items
}

func (handler *CardHandler) buildCardDetailEvents(ctx context.Context, region string, id string, card map[string]any) []map[string]any {
	matches, err := handler.masterDataSync.Search(ctx, region, "eventcards", id, []string{"cardId"}, 1000000)
	if err != nil {
		return nil
	}

	targetCardID := shared.NormalizeAnyID(id)
	items := make([]map[string]any, 0, len(matches))
	for _, match := range matches {
		if shared.NormalizeAnyID(match.Item["cardId"]) != targetCardID {
			continue
		}

		item := shared.BuildRecordWithReleaseCondition(ctx, handler.masterDataSync, region, match.Item)

		eventID := shared.NormalizeAnyID(match.Item["eventId"])
		if eventID != "" {
			eventRecord, found, err := handler.masterDataSync.GetByID(ctx, region, "events", eventID)
			if err == nil && found {
				eventSummary := make(map[string]any, 7)
				for _, key := range []string{"id", "name", "eventType", "assetbundleName", "startAt", "aggregateAt", "closedAt"} {
					if value, ok := eventRecord[key]; ok {
						eventSummary[key] = value
					}
				}
				item["event"] = eventSummary
			}

			bonusMin, bonusMax, ok, err := handler.buildCardEventBonusRange(ctx, region, eventID, card, match.Item)
			if err == nil && ok {
				item["finalBonusRateMin"] = normalizeBonusRateValue(bonusMin)
				item["finalBonusRateMax"] = normalizeBonusRateValue(bonusMax)
			}
		}

		items = append(items, item)
	}
	return items
}

func (handler *CardHandler) buildCardDetailGachas(ctx context.Context, region string, id string) []map[string]any {
	allGachas, err := handler.masterDataSync.ListAll(ctx, region, "gachas")
	if err != nil {
		return nil
	}

	targetCardID := shared.NormalizeAnyID(id)
	gachas := make([]map[string]any, 0)

	for _, gacha := range allGachas {
		pickupsRaw, ok := gacha["gachaPickups"]
		if !ok {
			continue
		}

		pickups, ok := pickupsRaw.([]any)
		if !ok || len(pickups) == 0 {
			continue
		}

		for _, pickupRaw := range pickups {
			pickup, ok := pickupRaw.(map[string]any)
			if !ok {
				continue
			}

			if shared.NormalizeAnyID(pickup["cardId"]) == targetCardID {
				gachaSummary := make(map[string]any, 4)
				if value, ok := gacha["id"]; ok {
					gachaSummary["id"] = value
				}
				if value, ok := gacha["name"]; ok {
					gachaSummary["name"] = value
				}
				if value, ok := gacha["assetbundleName"]; ok {
					gachaSummary["assetbundleName"] = value
				}
				if value, ok := gacha["startAt"]; ok {
					gachaSummary["startAt"] = value
				}
				gachas = append(gachas, gachaSummary)
				break
			}
		}
	}
	return gachas
}
