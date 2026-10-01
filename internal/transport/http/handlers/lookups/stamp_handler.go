package lookups

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/cachehint"
	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
)

const (
	stampsEntity            = "stamps"
	stampGameCharacterUnits = "gamecharacterunits"

	// The reverse indexes of the resource boxes that reward a (type, ID) item:
	// embedded details (JP, EN) and separate detail rows (TW, KR, CN).
	resourceBoxItemIndex       = "details[resourceType+resourceId]"
	resourceBoxDetailItemIndex = "resourceType,resourceId"

	stampCategoryCharacter = "character"
	stampCategoryBond      = "bond"
	stampCategoryText      = "text"
	stampCategoryOther     = "other"
)

// The sources a stamp comes from, by the purpose of the resource box that rewards
// it. A stamp with none of these purposes (gifts, login bonuses, episode rewards,
// or no resource box) comes from "other".
const stampSourceOther = "other"

var stampPurposeSources = map[string]string{
	"shop_item":             "shop",
	"material_exchange":     "exchange",
	"virtual_live_reward":   "live",
	"character_rank_reward": "rank",
	"bonds_reward":          "bond",
	"billing_shop_item":     "crystal",
}

var (
	stampSources        = []string{"shop", "exchange", "live", "rank", "bond", "crystal", stampSourceOther}
	stampCategories     = []string{stampCategoryCharacter, stampCategoryBond, stampCategoryText, stampCategoryOther}
	stampSortableFields = []string{"id", "seq"}
	// stampCharacterFields are the character slots of a stamp; the game reads
	// up to five.
	stampCharacterFields = []string{"characterId1", "characterId2", "characterId3", "characterId4", "characterId5"}
)

// stampItem is a stamp's response with its normalized name, for the name filter.
type stampItem struct {
	response shared.StampListItemResponse
	nameKey  string
}

// StampsList godoc
// @Summary List stamps by page
// @Description Lists stamps in stored order, each with its characters and a category derived from them. Stamps whose archivePublishedAt is in the future are hidden unless spoiler=true.
// @Tags stamps
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param name query string false "Case-insensitive substring of the stamp name"
// @Param category query string false "Comma-separated categories (character|bond|text|other)"
// @Param source query string false "Comma-separated sources by the resource box that rewards the stamp (shop|exchange|live|rank|bond|crystal|other)"
// @Param character_id query string false "Comma-separated game character IDs; the stamp must show all of them"
// @Param spoiler query bool false "Include stamps that are not published yet"
// @Param sort_by query string false "Sort field (id|seq)"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.StampListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /stamps/{region}/list [get]
func (handler *LookupHandler) StampsList(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
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
	sortOptions, ok := shared.ParseListSortOptions(c)
	if !ok || (sortOptions.Enabled && !shared.ValidateSortField(c, sortOptions.Field, nil, stampSortableFields)) {
		return
	}
	categories, ok := parseStampChoices(c, "category", stampCategories)
	if !ok {
		return
	}
	sources, ok := parseStampChoices(c, "source", stampSources)
	if !ok {
		return
	}
	characterFilters, ok := shared.ParseRecordFilters(c, map[string]string{"character_id": "characterIds"})
	if !ok {
		return
	}
	includeSpoilers, ok := shared.ParseSpoilerOption(c)
	if !ok {
		return
	}
	nameFilter := shared.NormalizeComparableText(c.Query("name"))
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, stampsEntity) {
		return
	}

	items, err := handler.loadStampItems(c.Request.Context(), region, includeSpoilers)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "STAMP_QUERY_ERROR", "failed to list stamps")
		return
	}
	items = filterStampItems(items, categories, characterFilters["characterIds"], nameFilter)
	items, err = handler.filterStampItemsBySource(c.Request.Context(), region, items, sources)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "STAMP_QUERY_ERROR", "failed to list stamps")
		return
	}
	if sortOptions.Enabled {
		sortStampItems(items, sortOptions.Field, sortOptions.Descending)
	}

	pageItems := make([]shared.StampListItemResponse, 0, pageSize)
	for _, item := range pageSlice(items, page, pageSize) {
		pageItems = append(pageItems, item.response)
	}
	response.JSON(c, http.StatusOK, shared.StampListResponse{
		Items:      pageItems,
		Pagination: lookupPaginationResponse(page, pageSize, len(items)),
	})
}

// parseStampChoices reads a comma-separated query parameter whose values must
// all be among allowed.
func parseStampChoices(c *gin.Context, param string, allowed []string) ([]string, bool) {
	values := parseCommaSeparatedValues(c.Query(param))
	for _, value := range values {
		if !slices.Contains(allowed, value) {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", param+" must be a comma-separated list of "+strings.Join(allowed, ", "))
			return nil, false
		}
	}
	return values, true
}

// loadStampItems reads the stamp list projection and returns every stamp with
// a valid ID once, in stored order. Stamps that are not published yet are
// dropped unless includeSpoilers, and the response cache learns when the
// first of them is.
func (handler *LookupHandler) loadStampItems(ctx context.Context, region string, includeSpoilers bool) ([]stampItem, error) {
	projection, err := handler.masterDataSync.LoadProjection(ctx, region, stampsEntity)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var firstReveal time.Time
	items := make([]stampItem, 0, projection.Len())
	seen := make(map[int64]struct{}, projection.Len())
	for row := range projection.Len() {
		source := projectionRowSource{projection: projection, row: row}
		item, ok := projectStampListItem(source)
		if !ok {
			continue
		}
		if _, exists := seen[item.response.ID]; exists {
			continue
		}
		seen[item.response.ID] = struct{}{}
		if revealAt, unpublished := stampRevealTime(source, now); unpublished && !includeSpoilers {
			if firstReveal.IsZero() || revealAt.Before(firstReveal) {
				firstReveal = revealAt
			}
			continue
		}
		items = append(items, item)
	}
	if err := handler.attachStampUnitCharacters(ctx, region, items); err != nil {
		return nil, err
	}
	cachehint.ValidUntil(ctx, firstReveal)
	return items, nil
}

// attachStampUnitCharacters gives a stamp that fills no character slot the
// character of its game character unit. The character-name text stamps are
// like that: they name a unit only.
func (handler *LookupHandler) attachStampUnitCharacters(ctx context.Context, region string, items []stampItem) error {
	unitIDs := []string{}
	for _, item := range items {
		if len(item.response.CharacterIDs) == 0 && item.response.GameCharacterUnitID != nil {
			unitIDs = append(unitIDs, formatID(*item.response.GameCharacterUnitID))
		}
	}
	if len(unitIDs) == 0 {
		return nil
	}
	units, err := shared.PrefetchRecords(ctx, handler.masterDataSync, region, map[string][]string{stampGameCharacterUnits: unitIDs})
	if err != nil {
		return err
	}
	for position := range items {
		response := &items[position].response
		if len(response.CharacterIDs) > 0 || response.GameCharacterUnitID == nil {
			continue
		}
		unit, ok := units.Record(stampGameCharacterUnits, formatID(*response.GameCharacterUnitID))
		if !ok {
			continue
		}
		if characterID, ok := lookupInt64(unit["gameCharacterId"]); ok && characterID > 0 {
			response.CharacterIDs = []int64{characterID}
			response.Category = stampCategory(response.StampType, 1)
		}
	}
	return nil
}

// stampRevealTime returns when an unpublished stamp is published.
func stampRevealTime(source fieldSource, now time.Time) (time.Time, bool) {
	value, _ := source.value("archivePublishedAt")
	publishedAt, ok := shared.ParseTimestampMillis(value)
	if !ok || publishedAt <= now.UnixMilli() {
		return time.Time{}, false
	}
	return time.UnixMilli(publishedAt), true
}

func projectStampListItem(source fieldSource) (stampItem, bool) {
	id, ok := sourceID(source)
	if !ok {
		return stampItem{}, false
	}
	stampType := sourceString(source, "stampType")
	characterIDs := stampCharacterIDs(source)
	name := sourceString(source, "name")
	return stampItem{
		response: shared.StampListItemResponse{
			ID:                  id,
			Seq:                 sourceOptionalInt64(source, "seq"),
			StampType:           stampType,
			Category:            stampCategory(stampType, len(characterIDs)),
			Name:                name,
			AssetbundleName:     sourceOptionalString(source, "assetbundleName"),
			CharacterIDs:        characterIDs,
			GameCharacterUnitID: sourceOptionalInt64(source, "gameCharacterUnitId"),
			Description:         sourceOptionalString(source, "description"),
		},
		nameKey: shared.NormalizeComparableText(name),
	}, true
}

// stampCharacterIDs returns the stamp's characters in slot order; an empty
// slot is 0 and is skipped.
func stampCharacterIDs(source fieldSource) []int64 {
	ids := []int64{}
	for _, field := range stampCharacterFields {
		if id, ok := sourceInt64(source, field); ok && id > 0 {
			ids = appendUniqueID(ids, id)
		}
	}
	return ids
}

func stampCategory(stampType string, characterCount int) string {
	switch {
	case stampType == "text" || stampType == "cheerful_carnival_message":
		return stampCategoryText
	case characterCount >= 2:
		return stampCategoryBond
	case characterCount == 1:
		return stampCategoryCharacter
	}
	return stampCategoryOther
}

func filterStampItems(items []stampItem, categories []string, characterIDs []float64, nameFilter string) []stampItem {
	if len(categories) == 0 && len(characterIDs) == 0 && nameFilter == "" {
		return items
	}
	filtered := make([]stampItem, 0, len(items))
	for _, item := range items {
		if stampItemMatches(item, categories, characterIDs, nameFilter) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func stampItemMatches(item stampItem, categories []string, characterIDs []float64, nameFilter string) bool {
	if len(categories) > 0 && !slices.Contains(categories, item.response.Category) {
		return false
	}
	for _, characterID := range characterIDs {
		if !slices.Contains(item.response.CharacterIDs, int64(characterID)) {
			return false
		}
	}
	return nameFilter == "" || strings.Contains(item.nameKey, nameFilter)
}

// sortStampItems orders items by field, keeping items without a seq last and
// breaking ties by ID.
func sortStampItems(items []stampItem, field string, descending bool) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i].response, items[j].response
		comparison := 0
		if field == "seq" {
			if (left.Seq == nil) != (right.Seq == nil) {
				return left.Seq != nil
			}
			if left.Seq != nil {
				comparison = cmp.Compare(*left.Seq, *right.Seq)
			}
		}
		if comparison == 0 {
			comparison = cmp.Compare(left.ID, right.ID)
			if field != "id" {
				return comparison < 0
			}
		}
		if descending {
			return comparison > 0
		}
		return comparison < 0
	})
}

// filterStampItemsBySource keeps the stamps that come from any of sources. It
// reads the resource boxes that reward the remaining stamps through the reverse
// indexes, so the filter's cost follows the stamps left, not the boxes.
func (handler *LookupHandler) filterStampItemsBySource(ctx context.Context, region string, items []stampItem, sources []string) ([]stampItem, error) {
	if len(sources) == 0 || len(items) == 0 {
		return items, nil
	}
	purposes, err := handler.loadStampPurposes(ctx, region, items)
	if err != nil {
		return nil, err
	}
	filtered := make([]stampItem, 0, len(items))
	for position, item := range items {
		if stampComesFrom(purposes[position], sources) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

// loadStampPurposes returns, for each stamp, the purposes of the resource boxes
// that reward it. A region keeps its box details either inside the boxes or in
// separate rows, so both are read; the one it lacks is empty.
func (handler *LookupHandler) loadStampPurposes(ctx context.Context, region string, items []stampItem) ([][]string, error) {
	lookups := make([][]any, len(items))
	for position, item := range items {
		lookups[position] = []any{"stamp", item.response.ID}
	}
	boxes, err := handler.masterDataSync.ListByIndex(ctx, region, resourceBoxesEntity, resourceBoxItemIndex, lookups)
	if err != nil {
		return nil, err
	}
	details, err := handler.masterDataSync.ListByIndex(ctx, region, resourceBoxDetailsEntity, resourceBoxDetailItemIndex, lookups)
	if err != nil {
		return nil, err
	}
	purposes := make([][]string, len(items))
	for position := range items {
		for _, record := range slices.Concat(boxes[position], details[position]) {
			if purpose := lookupString(record["resourceBoxPurpose"]); purpose != "" {
				purposes[position] = append(purposes[position], purpose)
			}
		}
	}
	return purposes, nil
}

// stampComesFrom reports whether a stamp rewarded through boxes of the given
// purposes comes from any of sources.
func stampComesFrom(purposes []string, sources []string) bool {
	named := false
	for _, purpose := range purposes {
		if source, ok := stampPurposeSources[purpose]; ok {
			named = true
			if slices.Contains(sources, source) {
				return true
			}
		}
	}
	return !named && slices.Contains(sources, stampSourceOther)
}
