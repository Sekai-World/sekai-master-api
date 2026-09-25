package lookups

import (
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
	bondsHonorsEntity                  = "bondshonors"
	bondsHonorBondsEntity              = "bonds"
	bondsHonorWordsEntity              = "bondsHonorWords"
	bondsHonorGameCharacterUnitsEntity = "gamecharacterunits"
	bondsHonorQueryErrorCode           = "BONDS_HONOR_QUERY_ERROR"
	bondsHonorNotFoundCode             = "BONDS_HONOR_NOT_FOUND"
	bondsHonorInvalidGameCharacterIDs  = "game_character_ids must contain exactly two different positive integers"
)

var bondsHonorListQueryParameters = map[string]struct{}{
	"page":                    {},
	"page_size":               {},
	"sort_by":                 {},
	"sort_order":              {},
	"bonds_group_id":          {},
	"game_character_unit_id1": {},
	"game_character_unit_id2": {},
	"game_character_ids":      {},
}

var bondsHonorSortableFields = map[string]struct{}{
	"id":                   {},
	"seq":                  {},
	"bondsGroupId":         {},
	"gameCharacterUnitId1": {},
	"gameCharacterUnitId2": {},
	"honorRarity":          {},
	"name":                 {},
}

type bondsHonorListOptions struct {
	page       int
	pageSize   int
	sortBy     string
	descending bool
	filters    bondsHonorFilters
}

type bondsHonorFilters struct {
	bondsGroupID         *int64
	gameCharacterUnitID1 *int64
	gameCharacterUnitID2 *int64
	gameCharacterIDs     *bondsHonorGameCharacterFilter
}

type bondsHonorGameCharacterFilter struct {
	first  int64
	second int64
}

// BondsHonorsByID godoc
// @Summary Get a bonds honor by id
// @Tags bondsHonors
// @Produce json
// @Param region path string true "Region"
// @Param id path int true "Bonds honor ID" minimum(1)
// @Success 200 {object} shared.BondsHonorObjectResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /bondsHonors/{region}/{id} [get]
func (handler *LookupHandler) BondsHonorsByID(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region, ok := parseTypedLookupRegion(c, handler.masterDataSync)
	if !ok {
		return
	}
	if !validateBondsHonorQueryParameters(c, nil) {
		return
	}
	id, ok := parseTypedLookupID(c)
	if !ok {
		return
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, bondsHonorsEntity) {
		return
	}

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, bondsHonorsEntity, id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, bondsHonorQueryErrorCode, "failed to query bonds honor")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, bondsHonorNotFoundCode, "bonds honor not found")
		return
	}

	items := []shared.BondsHonorObjectResponse{projectBondsHonor(record)}
	if err := handler.enrichBondsHonorResponses(c.Request.Context(), region, items, nil); err != nil {
		response.Error(c, http.StatusInternalServerError, bondsHonorQueryErrorCode, "failed to query bonds honor relationships")
		return
	}

	response.JSON(c, http.StatusOK, items[0])
}

// BondsHonorsList godoc
// @Summary List bonds honors by page
// @Tags bondsHonors
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param sort_by query string false "Sort field" Enums(id,seq,bondsGroupId,gameCharacterUnitId1,gameCharacterUnitId2,honorRarity,name)
// @Param sort_order query string false "Sort order" Enums(asc,desc)
// @Param bonds_group_id query int false "Exact bonds group ID" minimum(1)
// @Param game_character_unit_id1 query int false "Exact first game character unit ID" minimum(1)
// @Param game_character_unit_id2 query int false "Exact second game character unit ID" minimum(1)
// @Param game_character_ids query string false "Exactly two distinct underlying game character IDs, comma-separated (for example: 1,2)"
// @Success 200 {object} shared.BondsHonorListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /bondsHonors/{region}/list [get]
func (handler *LookupHandler) BondsHonorsList(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region, ok := parseTypedLookupRegion(c, handler.masterDataSync)
	if !ok {
		return
	}
	options, ok := parseBondsHonorListOptions(c)
	if !ok {
		return
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, bondsHonorsEntity) {
		return
	}

	characterUnits, err := handler.loadBondsHonorFilterCharacterUnits(
		c.Request.Context(),
		region,
		options.filters,
	)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, bondsHonorQueryErrorCode, "failed to query game character units")
		return
	}

	records, err := handler.masterDataSync.ListAll(c.Request.Context(), region, bondsHonorsEntity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, bondsHonorQueryErrorCode, "failed to list bonds honors")
		return
	}

	items := filterBondsHonorRecords(records, options.filters, characterUnits)
	sortBondsHonorResponses(items, options.sortBy, options.descending)

	total := len(items)
	pageItems := paginateBondsHonorResponses(items, options.page, options.pageSize)
	if err := handler.enrichBondsHonorResponses(c.Request.Context(), region, pageItems, characterUnits); err != nil {
		response.Error(c, http.StatusInternalServerError, bondsHonorQueryErrorCode, "failed to query bonds honor relationships")
		return
	}

	response.JSON(c, http.StatusOK, shared.BondsHonorListResponse{
		Items:      pageItems,
		Pagination: lookupPaginationResponse(options.page, options.pageSize, total),
	})
}

func parseBondsHonorListOptions(c *gin.Context) (bondsHonorListOptions, bool) {
	if !validateBondsHonorQueryParameters(c, bondsHonorListQueryParameters) {
		return bondsHonorListOptions{}, false
	}

	query := c.Request.URL.Query()
	page, pageSize, ok := parseBondsHonorPaginationOptions(c, query)
	if !ok {
		return bondsHonorListOptions{}, false
	}

	sortBy, descending, ok := parseBondsHonorSortOptions(c, query)
	if !ok {
		return bondsHonorListOptions{}, false
	}

	filters, ok := parseBondsHonorFilters(c, query)
	if !ok {
		return bondsHonorListOptions{}, false
	}

	return bondsHonorListOptions{
		page:       page,
		pageSize:   pageSize,
		sortBy:     sortBy,
		descending: descending,
		filters:    filters,
	}, true
}

func parseBondsHonorPaginationOptions(c *gin.Context, query map[string][]string) (int, int, bool) {
	page := 1
	pageSize := 20

	if rawPage, exists, ok := bondsHonorQueryValue(c, query, "page"); !ok {
		return 0, 0, false
	} else if exists {
		parsedPage, err := strconv.Atoi(rawPage)
		if err != nil || parsedPage <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "page must be a positive integer")
			return 0, 0, false
		}
		page = parsedPage
	}

	if rawPageSize, exists, ok := bondsHonorQueryValue(c, query, "page_size"); !ok {
		return 0, 0, false
	} else if exists {
		parsedPageSize, err := strconv.Atoi(rawPageSize)
		if err != nil || parsedPageSize <= 0 || parsedPageSize > maxLookupPageSize {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "page_size must be a positive integer no greater than 100")
			return 0, 0, false
		}
		pageSize = parsedPageSize
	}

	return page, pageSize, true
}

func parseBondsHonorSortOptions(c *gin.Context, query map[string][]string) (string, bool, bool) {
	sortBy := "seq"
	if rawSortBy, exists, ok := bondsHonorQueryValue(c, query, "sort_by"); !ok {
		return "", false, false
	} else if exists {
		sortBy = rawSortBy
	}
	if _, valid := bondsHonorSortableFields[sortBy]; !valid {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "sort_by must be one of: bondsGroupId, gameCharacterUnitId1, gameCharacterUnitId2, honorRarity, id, name, seq")
		return "", false, false
	}

	descending := false
	if rawSortOrder, exists, ok := bondsHonorQueryValue(c, query, "sort_order"); !ok {
		return "", false, false
	} else if exists {
		switch strings.ToLower(rawSortOrder) {
		case "asc":
		case "desc":
			descending = true
		default:
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "sort_order must be one of: asc, desc")
			return "", false, false
		}
	}

	return sortBy, descending, true
}

func parseBondsHonorFilters(c *gin.Context, query map[string][]string) (bondsHonorFilters, bool) {
	var filters bondsHonorFilters
	var ok bool

	filters.bondsGroupID, ok = parseBondsHonorIDFilter(c, query, "bonds_group_id")
	if !ok {
		return bondsHonorFilters{}, false
	}
	filters.gameCharacterUnitID1, ok = parseBondsHonorIDFilter(c, query, "game_character_unit_id1")
	if !ok {
		return bondsHonorFilters{}, false
	}
	filters.gameCharacterUnitID2, ok = parseBondsHonorIDFilter(c, query, "game_character_unit_id2")
	if !ok {
		return bondsHonorFilters{}, false
	}
	filters.gameCharacterIDs, ok = parseBondsHonorGameCharacterFilter(c, query)
	if !ok {
		return bondsHonorFilters{}, false
	}

	return filters, true
}

func validateBondsHonorQueryParameters(c *gin.Context, allowed map[string]struct{}) bool {
	for name, values := range c.Request.URL.Query() {
		if _, ok := allowed[name]; !ok {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "unsupported query parameter: "+name)
			return false
		}
		if len(values) != 1 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", name+" must be provided once")
			return false
		}
	}

	return true
}

func bondsHonorQueryValue(c *gin.Context, query map[string][]string, name string) (string, bool, bool) {
	values, exists := query[name]
	if !exists {
		return "", false, true
	}
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", name+" must not be empty")
		return "", true, false
	}

	return strings.TrimSpace(values[0]), true, true
}

func parseBondsHonorIDFilter(c *gin.Context, query map[string][]string, name string) (*int64, bool) {
	rawValue, exists, ok := bondsHonorQueryValue(c, query, name)
	if !ok {
		return nil, false
	}
	if !exists {
		return nil, true
	}

	value, err := strconv.ParseInt(rawValue, 10, 64)
	if err != nil || value <= 0 {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", name+" must be a positive integer")
		return nil, false
	}

	return &value, true
}

func parseBondsHonorGameCharacterFilter(c *gin.Context, query map[string][]string) (*bondsHonorGameCharacterFilter, bool) {
	rawValue, exists, ok := bondsHonorQueryValue(c, query, "game_character_ids")
	if !ok {
		return nil, false
	}
	if !exists {
		return nil, true
	}

	parts := strings.Split(rawValue, ",")
	if len(parts) != 2 {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", bondsHonorInvalidGameCharacterIDs)
		return nil, false
	}

	values := [2]int64{}
	for index, part := range parts {
		value, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || value <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", bondsHonorInvalidGameCharacterIDs)
			return nil, false
		}
		values[index] = value
	}
	if values[0] == values[1] {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", bondsHonorInvalidGameCharacterIDs)
		return nil, false
	}

	return &bondsHonorGameCharacterFilter{first: values[0], second: values[1]}, true
}

func projectBondsHonor(record map[string]any) shared.BondsHonorObjectResponse {
	return shared.BondsHonorObjectResponse{
		ID:                            lookupOptionalInt64(record["id"]),
		Seq:                           lookupOptionalInt64(record["seq"]),
		BondsGroupID:                  lookupOptionalInt64(record["bondsGroupId"]),
		GameCharacterUnitID1:          lookupOptionalInt64(record["gameCharacterUnitId1"]),
		GameCharacterUnitID2:          lookupOptionalInt64(record["gameCharacterUnitId2"]),
		HonorRarity:                   lookupOptionalString(record["honorRarity"]),
		Name:                          lookupOptionalString(record["name"]),
		Pronunciation:                 lookupOptionalString(record["pronunciation"]),
		Description:                   lookupOptionalString(record["description"]),
		ConfigurableUnitVirtualSinger: bondsHonorOptionalBool(record["configurableUnitVirtualSinger"]),
		Levels:                        projectBondsHonorLevels(record["levels"]),
	}
}

func projectBondsHonorLevels(value any) *[]shared.BondsHonorLevelResponse {
	var records []map[string]any
	switch levels := value.(type) {
	case []any:
		for _, level := range levels {
			if record, ok := lookupRecord(level); ok {
				records = append(records, record)
			}
		}
	case []map[string]any:
		records = levels
	default:
		return nil
	}

	items := make([]shared.BondsHonorLevelResponse, 0, len(records))
	for _, record := range records {
		items = append(items, shared.BondsHonorLevelResponse{
			ID:           lookupOptionalInt64(record["id"]),
			BondsHonorID: lookupOptionalInt64(record["bondsHonorId"]),
			Level:        lookupOptionalInt64(record["level"]),
			Description:  lookupOptionalString(record["description"]),
		})
	}

	return &items
}

func bondsHonorOptionalBool(value any) *bool {
	boolean, ok := value.(bool)
	if !ok {
		return nil
	}

	return &boolean
}

func bondsHonorMatchesFilters(item shared.BondsHonorObjectResponse, filters bondsHonorFilters) bool {
	if filters.bondsGroupID != nil && (item.BondsGroupID == nil || *item.BondsGroupID != *filters.bondsGroupID) {
		return false
	}
	if filters.gameCharacterUnitID1 != nil && (item.GameCharacterUnitID1 == nil || *item.GameCharacterUnitID1 != *filters.gameCharacterUnitID1) {
		return false
	}
	if filters.gameCharacterUnitID2 != nil && (item.GameCharacterUnitID2 == nil || *item.GameCharacterUnitID2 != *filters.gameCharacterUnitID2) {
		return false
	}

	return true
}

func filterBondsHonorRecords(
	records []map[string]any,
	filters bondsHonorFilters,
	characterUnits map[int64]*shared.BondsHonorCharacterUnitResponse,
) []shared.BondsHonorObjectResponse {
	items := make([]shared.BondsHonorObjectResponse, 0, len(records))
	for _, record := range records {
		item := projectBondsHonor(record)
		if !bondsHonorMatchesFilters(item, filters) {
			continue
		}
		if filters.gameCharacterIDs != nil && !bondsHonorMatchesGameCharacterFilter(item, *filters.gameCharacterIDs, characterUnits) {
			continue
		}
		items = append(items, item)
	}

	return items
}

func bondsHonorMatchesGameCharacterFilter(
	item shared.BondsHonorObjectResponse,
	filter bondsHonorGameCharacterFilter,
	characterUnits map[int64]*shared.BondsHonorCharacterUnitResponse,
) bool {
	if item.GameCharacterUnitID1 == nil || item.GameCharacterUnitID2 == nil {
		return false
	}

	characterUnit1, found := characterUnits[*item.GameCharacterUnitID1]
	if !found || characterUnit1 == nil || characterUnit1.GameCharacterID == nil {
		return false
	}
	characterUnit2, found := characterUnits[*item.GameCharacterUnitID2]
	if !found || characterUnit2 == nil || characterUnit2.GameCharacterID == nil {
		return false
	}

	firstCharacterID := *characterUnit1.GameCharacterID
	secondCharacterID := *characterUnit2.GameCharacterID
	return (firstCharacterID == filter.first && secondCharacterID == filter.second) ||
		(firstCharacterID == filter.second && secondCharacterID == filter.first)
}

func sortBondsHonorResponses(items []shared.BondsHonorObjectResponse, sortBy string, descending bool) {
	sort.SliceStable(items, func(leftIndex, rightIndex int) bool {
		left := items[leftIndex]
		right := items[rightIndex]
		leftPresent := bondsHonorSortFieldPresent(left, sortBy)
		rightPresent := bondsHonorSortFieldPresent(right, sortBy)
		if leftPresent != rightPresent {
			return leftPresent
		}

		comparison := compareBondsHonorSortField(left, right, sortBy)
		if comparison == 0 {
			return compareBondsHonorIDs(left.ID, right.ID) < 0
		}
		if descending {
			return comparison > 0
		}
		return comparison < 0
	})
}

func bondsHonorSortFieldPresent(item shared.BondsHonorObjectResponse, sortBy string) bool {
	switch sortBy {
	case "id":
		return item.ID != nil
	case "seq":
		return item.Seq != nil
	case "bondsGroupId":
		return item.BondsGroupID != nil
	case "gameCharacterUnitId1":
		return item.GameCharacterUnitID1 != nil
	case "gameCharacterUnitId2":
		return item.GameCharacterUnitID2 != nil
	case "honorRarity":
		return item.HonorRarity != nil
	case "name":
		return item.Name != nil
	default:
		return false
	}
}

func compareBondsHonorSortField(left, right shared.BondsHonorObjectResponse, sortBy string) int {
	switch sortBy {
	case "id":
		return compareBondsHonorInt64(left.ID, right.ID)
	case "seq":
		return compareBondsHonorInt64(left.Seq, right.Seq)
	case "bondsGroupId":
		return compareBondsHonorInt64(left.BondsGroupID, right.BondsGroupID)
	case "gameCharacterUnitId1":
		return compareBondsHonorInt64(left.GameCharacterUnitID1, right.GameCharacterUnitID1)
	case "gameCharacterUnitId2":
		return compareBondsHonorInt64(left.GameCharacterUnitID2, right.GameCharacterUnitID2)
	case "honorRarity":
		return compareBondsHonorStrings(left.HonorRarity, right.HonorRarity)
	case "name":
		return compareBondsHonorStrings(left.Name, right.Name)
	default:
		return 0
	}
}

func compareBondsHonorStrings(left, right *string) int {
	if left == nil || right == nil {
		return 0
	}

	return strings.Compare(shared.NormalizeComparableText(*left), shared.NormalizeComparableText(*right))
}

func compareBondsHonorInt64(left, right *int64) int {
	if left == nil || right == nil {
		return 0
	}
	switch {
	case *left < *right:
		return -1
	case *left > *right:
		return 1
	default:
		return 0
	}
}

func compareBondsHonorIDs(left, right *int64) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return 1
	}
	if right == nil {
		return -1
	}

	return compareBondsHonorInt64(left, right)
}

func paginateBondsHonorResponses(items []shared.BondsHonorObjectResponse, page, pageSize int) []shared.BondsHonorObjectResponse {
	totalPages := (len(items) + pageSize - 1) / pageSize
	if page > totalPages {
		return []shared.BondsHonorObjectResponse{}
	}

	start := (page - 1) * pageSize
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

func (handler *LookupHandler) enrichBondsHonorResponses(
	ctx context.Context,
	region string,
	items []shared.BondsHonorObjectResponse,
	characterUnits map[int64]*shared.BondsHonorCharacterUnitResponse,
) error {
	if len(items) == 0 {
		return nil
	}

	needsBonds, needsCharacterUnits := bondsHonorRelationshipNeeds(items)
	bondsGroups := map[int64]*shared.BondsHonorGroupResponse{}
	if needsBonds {
		var err error
		bondsGroups, err = handler.loadBondsHonorGroups(ctx, region)
		if err != nil {
			return err
		}
	}

	if needsCharacterUnits && characterUnits == nil {
		var err error
		characterUnits, err = handler.loadBondsHonorCharacterUnits(ctx, region)
		if err != nil {
			return err
		}
	}

	wordsByGroup := map[int64][]shared.BondsHonorWordResponse{}
	if needsBonds {
		var err error
		wordsByGroup, err = handler.loadBondsHonorWords(ctx, region)
		if err != nil {
			return err
		}
	}

	applyBondsHonorRelationships(items, bondsGroups, wordsByGroup, characterUnits)

	return nil
}

func bondsHonorRelationshipNeeds(items []shared.BondsHonorObjectResponse) (bool, bool) {
	needsBonds := false
	needsCharacterUnits := false
	for _, item := range items {
		if item.BondsGroupID != nil {
			needsBonds = true
		}
		if item.GameCharacterUnitID1 != nil || item.GameCharacterUnitID2 != nil {
			needsCharacterUnits = true
		}
	}

	return needsBonds, needsCharacterUnits
}

func (handler *LookupHandler) loadBondsHonorGroups(
	ctx context.Context,
	region string,
) (map[int64]*shared.BondsHonorGroupResponse, error) {
	records, err := handler.masterDataSync.ListAll(ctx, region, bondsHonorBondsEntity)
	if err != nil {
		return nil, err
	}

	groups := make(map[int64]*shared.BondsHonorGroupResponse, len(records))
	for _, record := range records {
		groupID, ok := lookupInt64(record["groupId"])
		if !ok {
			continue
		}
		if _, exists := groups[groupID]; exists {
			continue
		}
		groups[groupID] = &shared.BondsHonorGroupResponse{
			GroupID:      lookupOptionalInt64(record["groupId"]),
			CharacterID1: lookupOptionalInt64(record["characterId1"]),
			CharacterID2: lookupOptionalInt64(record["characterId2"]),
		}
	}

	return groups, nil
}

func (handler *LookupHandler) loadBondsHonorWords(
	ctx context.Context,
	region string,
) (map[int64][]shared.BondsHonorWordResponse, error) {
	records, err := handler.masterDataSync.ListAll(ctx, region, bondsHonorWordsEntity)
	if err != nil {
		return nil, err
	}

	wordsByGroup := make(map[int64][]shared.BondsHonorWordResponse)
	for _, record := range records {
		groupID, ok := lookupInt64(record["bondsGroupId"])
		if !ok {
			continue
		}

		wordsByGroup[groupID] = append(wordsByGroup[groupID], shared.BondsHonorWordResponse{
			ID:              lookupOptionalInt64(record["id"]),
			Seq:             lookupOptionalInt64(record["seq"]),
			BondsGroupID:    lookupOptionalInt64(record["bondsGroupId"]),
			AssetbundleName: lookupOptionalString(record["assetbundleName"]),
			Name:            lookupOptionalString(record["name"]),
			Description:     lookupOptionalString(record["description"]),
		})
	}

	for groupID := range wordsByGroup {
		sort.SliceStable(wordsByGroup[groupID], func(leftIndex, rightIndex int) bool {
			left := wordsByGroup[groupID][leftIndex]
			right := wordsByGroup[groupID][rightIndex]
			if comparison := compareBondsHonorIDs(left.Seq, right.Seq); comparison != 0 {
				return comparison < 0
			}
			return compareBondsHonorIDs(left.ID, right.ID) < 0
		})
	}

	return wordsByGroup, nil
}

func applyBondsHonorRelationships(
	items []shared.BondsHonorObjectResponse,
	bondsGroups map[int64]*shared.BondsHonorGroupResponse,
	wordsByGroup map[int64][]shared.BondsHonorWordResponse,
	characterUnits map[int64]*shared.BondsHonorCharacterUnitResponse,
) {
	for index := range items {
		item := &items[index]
		if item.BondsGroupID != nil {
			item.BondsGroup = bondsGroups[*item.BondsGroupID]
			if words := wordsByGroup[*item.BondsGroupID]; len(words) > 0 {
				item.Words = &words
			}
		}
		if item.GameCharacterUnitID1 != nil {
			item.CharacterUnit1 = characterUnits[*item.GameCharacterUnitID1]
		}
		if item.GameCharacterUnitID2 != nil {
			item.CharacterUnit2 = characterUnits[*item.GameCharacterUnitID2]
		}
	}
}

func (handler *LookupHandler) loadBondsHonorFilterCharacterUnits(
	ctx context.Context,
	region string,
	filters bondsHonorFilters,
) (map[int64]*shared.BondsHonorCharacterUnitResponse, error) {
	if filters.gameCharacterIDs == nil {
		return nil, nil
	}

	return handler.loadBondsHonorCharacterUnits(ctx, region)
}

func (handler *LookupHandler) loadBondsHonorCharacterUnits(
	ctx context.Context,
	region string,
) (map[int64]*shared.BondsHonorCharacterUnitResponse, error) {
	records, err := handler.masterDataSync.ListAll(ctx, region, bondsHonorGameCharacterUnitsEntity)
	if err != nil {
		return nil, err
	}

	characterUnits := make(map[int64]*shared.BondsHonorCharacterUnitResponse, len(records))
	for _, record := range records {
		id, ok := lookupInt64(record["id"])
		if !ok {
			continue
		}
		if _, exists := characterUnits[id]; exists {
			continue
		}
		characterUnits[id] = &shared.BondsHonorCharacterUnitResponse{
			ID:              lookupOptionalInt64(record["id"]),
			GameCharacterID: lookupOptionalInt64(record["gameCharacterId"]),
			Unit:            lookupOptionalString(record["unit"]),
			ColorCode:       lookupOptionalString(record["colorCode"]),
		}
	}

	return characterUnits, nil
}
