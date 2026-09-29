package lookups

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
)

const (
	mysekaiFixturesEntity                    = "mysekaifixtures"
	mysekaiFixtureMainGenresEntity           = "mysekaifixturemaingenres"
	mysekaiFixtureSubGenresEntity            = "mysekaifixturesubgenres"
	mysekaiFixtureTagsEntity                 = "mysekaifixturetags"
	mysekaiBlueprintsEntity                  = "mysekaiblueprints"
	mysekaiBlueprintMaterialCostsEntity      = "mysekaiblueprintmysekaimaterialcosts"
	mysekaiFixtureCharacterBonusesEntity     = "mysekaifixturegamecharactergroupperformancebonuses"
	mysekaiFixtureCharacterGroupsEntity      = "mysekaifixturegamecharactergroups"
	mysekaiFixtureDisassembleMaterialsEntity = "mysekaifixtureonlydisassemblematerials"

	mysekaiFixtureCraftType = "mysekai_fixture"
)

// mysekaiFixtureFilterTagTypes are the tag types the catalogue filters by, in
// display order. Most other tags are "none" tags that only repeat a fixture's
// own name.
var mysekaiFixtureFilterTagTypes = []string{"series", "unit", "game_character"}

var mysekaiFixtureSortableFields = []string{"id", "seq", "name"}

// MysekaiFixturesByID godoc
// @Summary Get a MySekai fixture by id
// @Description Returns a fixture (furniture) with its genres, tags, crafting blueprint and material costs, character bonus, and the materials only disassembling it returns.
// @Tags mysekaiFixtures
// @Produce json
// @Param region path string true "Region"
// @Param id path int true "Fixture ID" minimum(1)
// @Success 200 {object} shared.MysekaiFixtureDetailResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /mysekaiFixtures/{region}/{id} [get]
func (handler *LookupHandler) MysekaiFixturesByID(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, mysekaiFixturesEntity) {
		return
	}

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, mysekaiFixturesEntity, id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MYSEKAI_FIXTURE_QUERY_ERROR", "failed to query MySekai fixture")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "MYSEKAI_FIXTURE_NOT_FOUND", "MySekai fixture not found")
		return
	}

	detail, err := handler.buildMysekaiFixtureDetail(c.Request.Context(), region, recordSource(record))
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MYSEKAI_FIXTURE_QUERY_ERROR", "failed to query MySekai fixture details")
		return
	}
	response.JSON(c, http.StatusOK, detail)
}

// MysekaiFixturesList godoc
// @Summary List MySekai fixtures by page
// @Description Lists fixtures (furniture) in stored order unless sorted. tag_id keeps fixtures that carry every listed tag; genre filters keep fixtures in any listed genre.
// @Tags mysekaiFixtures
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param name query string false "Case-insensitive substring of the name or its reading"
// @Param main_genre_id query string false "Comma-separated main genre IDs"
// @Param sub_genre_id query string false "Comma-separated sub-genre IDs"
// @Param tag_id query string false "Comma-separated tag IDs, all required"
// @Param sort_by query string false "Sort field (id|seq|name)"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.MysekaiFixtureListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /mysekaiFixtures/{region}/list [get]
func (handler *LookupHandler) MysekaiFixturesList(c *gin.Context) {
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
	if !ok {
		return
	}
	if sortOptions.Enabled && !shared.ValidateSortField(c, sortOptions.Field, nil, mysekaiFixtureSortableFields) {
		return
	}
	filters, ok := shared.ParseRecordFilters(c, map[string]string{
		"main_genre_id": "mysekaiFixtureMainGenreId",
		"sub_genre_id":  "mysekaiFixtureSubGenreId",
		"tag_id":        "tagIds",
	})
	if !ok {
		return
	}
	nameFilter := shared.NormalizeComparableText(c.Query("name"))
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, mysekaiFixturesEntity) {
		return
	}

	items, err := handler.loadMysekaiFixtureItems(c.Request.Context(), region)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MYSEKAI_FIXTURE_QUERY_ERROR", "failed to list MySekai fixtures")
		return
	}
	items = filterMysekaiFixtureItems(items, filters, nameFilter)
	if sortOptions.Enabled {
		sortMysekaiFixtureItems(items, sortOptions.Field, sortOptions.Descending)
	}

	total := len(items)
	pageItems := make([]shared.MysekaiFixtureListItemResponse, 0, pageSize)
	for _, item := range pageSlice(items, page, pageSize) {
		pageItems = append(pageItems, item.response)
	}
	response.JSON(c, http.StatusOK, shared.MysekaiFixtureListResponse{
		Items:      pageItems,
		Pagination: lookupPaginationResponse(page, pageSize, total),
	})
}

// MysekaiFixtureFilters godoc
// @Summary List the MySekai fixture catalogue filters
// @Description Returns the main genres with their sub-genres, and the series, unit, and character tags, that at least one fixture of the region uses.
// @Tags mysekaiFixtures
// @Produce json
// @Param region path string true "Region"
// @Success 200 {object} shared.MysekaiFixtureFiltersResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /mysekaiFixtures/{region}/filters [get]
func (handler *LookupHandler) MysekaiFixtureFilters(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region, ok := parseTypedLookupRegion(c, handler.masterDataSync)
	if !ok {
		return
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, mysekaiFixturesEntity) {
		return
	}

	filters, err := handler.buildMysekaiFixtureFilters(c.Request.Context(), region)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MYSEKAI_FIXTURE_QUERY_ERROR", "failed to list MySekai fixture filters")
		return
	}
	response.JSON(c, http.StatusOK, filters)
}

// mysekaiFixtureItem is one listed fixture: its projection row and response.
type mysekaiFixtureItem struct {
	source   fieldSource
	response shared.MysekaiFixtureListItemResponse
}

// loadMysekaiFixtureItems reads the fixture list projection and returns every
// fixture with a valid ID once, in stored order.
func (handler *LookupHandler) loadMysekaiFixtureItems(ctx context.Context, region string) ([]mysekaiFixtureItem, error) {
	projection, err := handler.masterDataSync.LoadProjection(ctx, region, mysekaiFixturesEntity)
	if err != nil {
		return nil, err
	}
	items := make([]mysekaiFixtureItem, 0, projection.Len())
	seen := make(map[int64]struct{}, projection.Len())
	for row := range projection.Len() {
		source := projectionRowSource{projection: projection, row: row}
		response, ok := projectMysekaiFixtureListItem(source)
		if !ok {
			continue
		}
		if _, exists := seen[response.ID]; exists {
			continue
		}
		seen[response.ID] = struct{}{}
		items = append(items, mysekaiFixtureItem{source: source, response: response})
	}
	return items, nil
}

func filterMysekaiFixtureItems(items []mysekaiFixtureItem, filters map[string][]float64, nameFilter string) []mysekaiFixtureItem {
	if len(filters) == 0 && nameFilter == "" {
		return items
	}
	filtered := make([]mysekaiFixtureItem, 0, len(items))
	for _, item := range items {
		if mysekaiFixtureItemMatches(item, filters, nameFilter) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func mysekaiFixtureItemMatches(item mysekaiFixtureItem, filters map[string][]float64, nameFilter string) bool {
	for field, values := range filters {
		switch field {
		case "tagIds":
			for _, tagID := range values {
				if !slices.Contains(item.response.TagIDs, int64(tagID)) {
					return false
				}
			}
		case "mysekaiFixtureMainGenreId":
			if !optionalIDIn(item.response.MysekaiFixtureMainGenreID, values) {
				return false
			}
		case "mysekaiFixtureSubGenreId":
			if !optionalIDIn(item.response.MysekaiFixtureSubGenreID, values) {
				return false
			}
		}
	}
	return matchesName(item.source, nameFilter, "name", "pronunciation")
}

func optionalIDIn(id *int64, values []float64) bool {
	return id != nil && slices.Contains(values, float64(*id))
}

// sortMysekaiFixtureItems orders items by field, keeping items without it last
// and breaking ties by ID.
func sortMysekaiFixtureItems(items []mysekaiFixtureItem, field string, descending bool) {
	names := make(map[int64]string, len(items))
	if field == "name" {
		for _, item := range items {
			names[item.response.ID] = shared.NormalizeComparableText(item.response.Name)
		}
	}
	sort.SliceStable(items, func(i int, j int) bool {
		left, right := items[i].response, items[j].response
		comparison := 0
		switch field {
		case "name":
			comparison = strings.Compare(names[left.ID], names[right.ID])
		case "seq":
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

// pageSlice returns page (1-based) of items.
func pageSlice[T any](items []T, page int, pageSize int) []T {
	start := (page - 1) * pageSize
	if start >= len(items) {
		return nil
	}
	return items[start:min(start+pageSize, len(items))]
}

func projectMysekaiFixtureListItem(source fieldSource) (shared.MysekaiFixtureListItemResponse, bool) {
	id, ok := sourceID(source)
	if !ok {
		return shared.MysekaiFixtureListItemResponse{}, false
	}
	return shared.MysekaiFixtureListItemResponse{
		ID:                        id,
		Seq:                       sourceOptionalInt64(source, "seq"),
		Name:                      sourceString(source, "name"),
		Pronunciation:             sourceOptionalString(source, "pronunciation"),
		MysekaiFixtureType:        sourceOptionalString(source, "mysekaiFixtureType"),
		MysekaiFixtureMainGenreID: sourceOptionalInt64(source, "mysekaiFixtureMainGenreId"),
		MysekaiFixtureSubGenreID:  sourceOptionalInt64(source, "mysekaiFixtureSubGenreId"),
		MysekaiSettableLayoutType: sourceOptionalString(source, "mysekaiSettableLayoutType"),
		GridSize:                  mysekaiFixtureGridSize(source),
		TagIDs:                    mysekaiFixtureTagIDs(source),
		AssetbundleName:           sourceOptionalString(source, "assetbundleName"),
	}, true
}

func mysekaiFixtureGridSize(source fieldSource) *shared.MysekaiFixtureGridSizeResponse {
	value, _ := source.value("gridSize")
	size, ok := lookupRecord(value)
	if !ok {
		return nil
	}
	width, widthOK := lookupInt64(size["width"])
	depth, depthOK := lookupInt64(size["depth"])
	height, heightOK := lookupInt64(size["height"])
	if !widthOK || !depthOK || !heightOK {
		return nil
	}
	return &shared.MysekaiFixtureGridSizeResponse{Width: width, Depth: depth, Height: height}
}

// mysekaiFixtureTagIDs returns the tag IDs of the fixture's tag group
// (mysekaiFixtureTagId1, mysekaiFixtureTagId2, ...) in slot order.
func mysekaiFixtureTagIDs(source fieldSource) []int64 {
	value, _ := source.value("mysekaiFixtureTagGroup")
	group, ok := lookupRecord(value)
	if !ok {
		return []int64{}
	}
	const prefix = "mysekaiFixtureTagId"
	type slot struct {
		index int64
		tagID int64
	}
	slots := make([]slot, 0, len(group))
	for key, raw := range group {
		index, ok := lookupInt64(strings.TrimPrefix(key, prefix))
		if !strings.HasPrefix(key, prefix) || !ok {
			continue
		}
		if tagID, ok := lookupInt64(raw); ok && tagID > 0 {
			slots = append(slots, slot{index: index, tagID: tagID})
		}
	}
	slices.SortFunc(slots, func(left slot, right slot) int { return cmp.Compare(left.index, right.index) })
	tagIDs := make([]int64, 0, len(slots))
	for _, slot := range slots {
		tagIDs = appendUniqueID(tagIDs, slot.tagID)
	}
	return tagIDs
}

func (handler *LookupHandler) buildMysekaiFixtureDetail(ctx context.Context, region string, source fieldSource) (shared.MysekaiFixtureDetailResponse, error) {
	item, _ := projectMysekaiFixtureListItem(source)
	detail := shared.MysekaiFixtureDetailResponse{
		ID:                        item.ID,
		Seq:                       item.Seq,
		Name:                      item.Name,
		Pronunciation:             item.Pronunciation,
		MysekaiFixtureType:        item.MysekaiFixtureType,
		MysekaiFixtureMainGenreID: item.MysekaiFixtureMainGenreID,
		MysekaiFixtureSubGenreID:  item.MysekaiFixtureSubGenreID,
		MysekaiSettableLayoutType: item.MysekaiSettableLayoutType,
		GridSize:                  item.GridSize,
		TagIDs:                    item.TagIDs,
		AssetbundleName:           item.AssetbundleName,
		FlavorText:                sourceOptionalString(source, "flavorText"),
		ColorCode:                 sourceOptionalString(source, "colorCode"),
		AnotherColors:             mysekaiFixtureColors(source),
		MysekaiSettableSiteType:   sourceOptionalString(source, "mysekaiSettableSiteType"),
		IsAssembled:               sourceOptionalBool(source, "isAssembled"),
		IsDisassembled:            sourceOptionalBool(source, "isDisassembled"),
		FirstPutCost:              sourceOptionalInt64(source, "firstPutCost"),
		SecondPutCost:             sourceOptionalInt64(source, "secondPutCost"),
		Tags:                      []shared.MysekaiFixtureTagResponse{},
		DisassembleMaterials:      []shared.MysekaiMaterialQuantityResponse{},
	}

	idsByEntity := map[string][]string{mysekaiFixtureTagsEntity: formatIDs(item.TagIDs)}
	if item.MysekaiFixtureMainGenreID != nil {
		idsByEntity[mysekaiFixtureMainGenresEntity] = []string{formatID(*item.MysekaiFixtureMainGenreID)}
	}
	if item.MysekaiFixtureSubGenreID != nil {
		idsByEntity[mysekaiFixtureSubGenresEntity] = []string{formatID(*item.MysekaiFixtureSubGenreID)}
	}
	bonusID := sourceOptionalInt64(source, "mysekaiFixtureGameCharacterGroupPerformanceBonusId")
	if bonusID != nil {
		idsByEntity[mysekaiFixtureCharacterBonusesEntity] = []string{formatID(*bonusID)}
	}
	prefetched, err := shared.PrefetchRecords(ctx, handler.masterDataSync, region, idsByEntity)
	if err != nil {
		return detail, err
	}

	if item.MysekaiFixtureMainGenreID != nil {
		if genre, ok := prefetched.Record(mysekaiFixtureMainGenresEntity, formatID(*item.MysekaiFixtureMainGenreID)); ok {
			detail.MainGenre = projectMysekaiFixtureGenre(recordSource(genre))
		}
	}
	if item.MysekaiFixtureSubGenreID != nil {
		if genre, ok := prefetched.Record(mysekaiFixtureSubGenresEntity, formatID(*item.MysekaiFixtureSubGenreID)); ok {
			detail.SubGenre = projectMysekaiFixtureGenre(recordSource(genre))
		}
	}
	for _, tagID := range item.TagIDs {
		if tag, ok := prefetched.Record(mysekaiFixtureTagsEntity, formatID(tagID)); ok {
			if projected, ok := projectMysekaiFixtureTag(recordSource(tag)); ok {
				detail.Tags = append(detail.Tags, projected)
			}
		}
	}

	blueprint, costs, err := handler.loadMysekaiFixtureBlueprint(ctx, region, item.ID)
	if err != nil {
		return detail, err
	}
	disassembled, err := handler.masterDataSync.ListByIndex(ctx, region, mysekaiFixtureDisassembleMaterialsEntity, "mysekaiFixtureId", [][]any{{item.ID}})
	if err != nil {
		return detail, err
	}
	disassembleRecords := disassembled[0]
	sortRecordsBySeq(disassembleRecords)

	materials, err := handler.prefetchMysekaiMaterials(ctx, region, append(slices.Clone(costs), disassembleRecords...))
	if err != nil {
		return detail, err
	}
	if blueprint != nil {
		detail.Blueprint = &shared.MysekaiBlueprintResponse{
			ID:                           lookupRequiredInt64(blueprint["id"]),
			IsEnableSketch:               sourceOptionalBool(recordSource(blueprint), "isEnableSketch"),
			IsObtainedByConvert:          sourceOptionalBool(recordSource(blueprint), "isObtainedByConvert"),
			IsAvailableWithoutPossession: sourceOptionalBool(recordSource(blueprint), "isAvailableWithoutPossession"),
			CraftCountLimit:              sourceOptionalInt64(recordSource(blueprint), "craftCountLimit"),
			MaterialCosts:                mysekaiMaterialQuantities(costs, materials),
		}
	}
	detail.DisassembleMaterials = mysekaiMaterialQuantities(disassembleRecords, materials)

	if bonusID != nil {
		if bonus, ok := prefetched.Record(mysekaiFixtureCharacterBonusesEntity, formatID(*bonusID)); ok {
			characterBonus, err := handler.loadMysekaiFixtureCharacterBonus(ctx, region, recordSource(bonus))
			if err != nil {
				return detail, err
			}
			detail.CharacterBonus = characterBonus
		}
	}
	return detail, nil
}

func mysekaiFixtureColors(source fieldSource) []shared.MysekaiFixtureColorResponse {
	value, _ := source.value("mysekaiFixtureAnotherColors")
	rawColors, _ := value.([]any)
	colors := make([]shared.MysekaiFixtureColorResponse, 0, len(rawColors))
	for _, raw := range rawColors {
		color, ok := lookupRecord(raw)
		if !ok {
			continue
		}
		colors = append(colors, shared.MysekaiFixtureColorResponse{
			TextureID: lookupOptionalInt64(color["textureId"]),
			ColorCode: sourceOptionalString(recordSource(color), "colorCode"),
		})
	}
	return colors
}

// loadMysekaiFixtureBlueprint returns the blueprint crafting the fixture and
// its material costs in cost order, or nil when no blueprint crafts it.
func (handler *LookupHandler) loadMysekaiFixtureBlueprint(ctx context.Context, region string, fixtureID int64) (map[string]any, []map[string]any, error) {
	blueprints, err := handler.masterDataSync.ListByIndex(ctx, region, mysekaiBlueprintsEntity, "mysekaiCraftType,craftTargetId", [][]any{{mysekaiFixtureCraftType, fixtureID}})
	if err != nil {
		return nil, nil, err
	}
	if len(blueprints[0]) == 0 {
		return nil, nil, nil
	}
	blueprint := blueprints[0][0]
	blueprintID, ok := lookupInt64(blueprint["id"])
	if !ok {
		return nil, nil, nil
	}
	costs, err := handler.masterDataSync.ListByIndex(ctx, region, mysekaiBlueprintMaterialCostsEntity, "mysekaiBlueprintId", [][]any{{blueprintID}})
	if err != nil {
		return nil, nil, err
	}
	sortRecordsBySeq(costs[0])
	return blueprint, costs[0], nil
}

// prefetchMysekaiMaterials reads the materials the quantity records name.
func (handler *LookupHandler) prefetchMysekaiMaterials(ctx context.Context, region string, quantities []map[string]any) (*shared.PrefetchedRecords, error) {
	ids := make([]string, 0, len(quantities))
	for _, quantity := range quantities {
		ids = append(ids, shared.NormalizeAnyID(quantity["mysekaiMaterialId"]))
	}
	return shared.PrefetchRecords(ctx, handler.masterDataSync, region, map[string][]string{mysekaiMaterialsEntity: ids})
}

// mysekaiMaterialQuantities pairs each quantity record with its material,
// skipping records whose material is missing.
func mysekaiMaterialQuantities(records []map[string]any, materials *shared.PrefetchedRecords) []shared.MysekaiMaterialQuantityResponse {
	quantities := make([]shared.MysekaiMaterialQuantityResponse, 0, len(records))
	for _, record := range records {
		material, ok := materials.Record(mysekaiMaterialsEntity, shared.NormalizeAnyID(record["mysekaiMaterialId"]))
		if !ok {
			continue
		}
		summary, ok := projectMysekaiMaterialSummary(recordSource(material))
		if !ok {
			continue
		}
		quantities = append(quantities, shared.MysekaiMaterialQuantityResponse{
			Material: summary,
			Quantity: lookupRequiredInt64(record["quantity"]),
		})
	}
	return quantities
}

// loadMysekaiFixtureCharacterBonus resolves a performance bonus to the
// characters of its character group, in group order.
func (handler *LookupHandler) loadMysekaiFixtureCharacterBonus(ctx context.Context, region string, bonus fieldSource) (*shared.MysekaiFixtureCharacterBonusResponse, error) {
	result := &shared.MysekaiFixtureCharacterBonusResponse{
		BonusRate:        sourceOptionalFloat64(bonus, "bonusRate"),
		GameCharacterIDs: []int64{},
	}
	groupID, ok := sourceInt64(bonus, "mysekaiFixtureGameCharacterGroupId")
	if !ok {
		return result, nil
	}
	members, err := handler.masterDataSync.ListByIndex(ctx, region, mysekaiFixtureCharacterGroupsEntity, "groupId", [][]any{{groupID}})
	if err != nil {
		return nil, err
	}
	for _, member := range members[0] {
		if characterID, ok := lookupInt64(member["gameCharacterId"]); ok && characterID > 0 {
			result.GameCharacterIDs = appendUniqueID(result.GameCharacterIDs, characterID)
		}
	}
	return result, nil
}

func projectMysekaiFixtureGenre(source fieldSource) *shared.MysekaiFixtureGenreResponse {
	id, ok := sourceID(source)
	if !ok {
		return nil
	}
	return &shared.MysekaiFixtureGenreResponse{
		ID:              id,
		Name:            sourceString(source, "name"),
		AssetbundleName: sourceOptionalString(source, "assetbundleName"),
	}
}

func projectMysekaiFixtureTag(source fieldSource) (shared.MysekaiFixtureTagResponse, bool) {
	id, ok := sourceID(source)
	if !ok {
		return shared.MysekaiFixtureTagResponse{}, false
	}
	return shared.MysekaiFixtureTagResponse{
		ID:                    id,
		Name:                  sourceString(source, "name"),
		MysekaiFixtureTagType: sourceString(source, "mysekaiFixtureTagType"),
		ExternalID:            sourceOptionalInt64(source, "externalId"),
		Pronunciation:         sourceOptionalString(source, "pronunciation"),
	}, true
}

// buildMysekaiFixtureFilters collects the genres and filter tags the region's
// fixtures use from the fixture list projection, then reads only those.
func (handler *LookupHandler) buildMysekaiFixtureFilters(ctx context.Context, region string) (shared.MysekaiFixtureFiltersResponse, error) {
	result := shared.MysekaiFixtureFiltersResponse{
		MainGenres: []shared.MysekaiFixtureMainGenreResponse{},
		Tags:       []shared.MysekaiFixtureTagResponse{},
	}
	items, err := handler.loadMysekaiFixtureItems(ctx, region)
	if err != nil {
		return result, err
	}

	mainGenreIDs := []int64{}
	subGenreIDs := []int64{}
	subGenresByMain := map[int64][]int64{}
	usedTags := map[int64]struct{}{}
	for _, item := range items {
		for _, tagID := range item.response.TagIDs {
			usedTags[tagID] = struct{}{}
		}
		if item.response.MysekaiFixtureMainGenreID == nil {
			continue
		}
		mainID := *item.response.MysekaiFixtureMainGenreID
		mainGenreIDs = appendUniqueID(mainGenreIDs, mainID)
		if item.response.MysekaiFixtureSubGenreID != nil {
			subID := *item.response.MysekaiFixtureSubGenreID
			subGenreIDs = appendUniqueID(subGenreIDs, subID)
			subGenresByMain[mainID] = appendUniqueID(subGenresByMain[mainID], subID)
		}
	}

	prefetched, err := shared.PrefetchRecords(ctx, handler.masterDataSync, region, map[string][]string{
		mysekaiFixtureMainGenresEntity: formatIDs(mainGenreIDs),
		mysekaiFixtureSubGenresEntity:  formatIDs(subGenreIDs),
	})
	if err != nil {
		return result, err
	}
	slices.Sort(mainGenreIDs)
	for _, mainID := range mainGenreIDs {
		record, ok := prefetched.Record(mysekaiFixtureMainGenresEntity, formatID(mainID))
		if !ok {
			continue
		}
		genre := projectMysekaiFixtureGenre(recordSource(record))
		if genre == nil {
			continue
		}
		mainGenre := shared.MysekaiFixtureMainGenreResponse{
			ID:              genre.ID,
			Name:            genre.Name,
			AssetbundleName: genre.AssetbundleName,
			SubGenres:       []shared.MysekaiFixtureGenreResponse{},
		}
		subIDs := subGenresByMain[mainID]
		slices.Sort(subIDs)
		for _, subID := range subIDs {
			if record, ok := prefetched.Record(mysekaiFixtureSubGenresEntity, formatID(subID)); ok {
				if subGenre := projectMysekaiFixtureGenre(recordSource(record)); subGenre != nil {
					mainGenre.SubGenres = append(mainGenre.SubGenres, *subGenre)
				}
			}
		}
		result.MainGenres = append(result.MainGenres, mainGenre)
	}

	lookups := make([][]any, 0, len(mysekaiFixtureFilterTagTypes))
	for _, tagType := range mysekaiFixtureFilterTagTypes {
		lookups = append(lookups, []any{tagType})
	}
	tagsByType, err := handler.masterDataSync.ListByIndex(ctx, region, mysekaiFixtureTagsEntity, "mysekaiFixtureTagType", lookups)
	if err != nil {
		return result, err
	}
	for _, tags := range tagsByType {
		projected := make([]shared.MysekaiFixtureTagResponse, 0, len(tags))
		for _, record := range tags {
			tag, ok := projectMysekaiFixtureTag(recordSource(record))
			if _, used := usedTags[tag.ID]; ok && used {
				projected = append(projected, tag)
			}
		}
		slices.SortStableFunc(projected, compareMysekaiFixtureTags)
		result.Tags = append(result.Tags, projected...)
	}
	return result, nil
}

// compareMysekaiFixtureTags orders tags of one type by external ID (character
// or unit order), then ID.
func compareMysekaiFixtureTags(left shared.MysekaiFixtureTagResponse, right shared.MysekaiFixtureTagResponse) int {
	if left.ExternalID != nil && right.ExternalID != nil && *left.ExternalID != *right.ExternalID {
		return cmp.Compare(*left.ExternalID, *right.ExternalID)
	}
	return cmp.Compare(left.ID, right.ID)
}
