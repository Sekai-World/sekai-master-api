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
	bondsHonorGameCharacterUnitsEntity = "gamecharacterunits"
	bondsHonorQueryErrorCode           = "BONDS_HONOR_QUERY_ERROR"
	bondsHonorNotFoundCode             = "BONDS_HONOR_NOT_FOUND"
)

var bondsHonorListQueryParameters = map[string]struct{}{
	"page":                    {},
	"page_size":               {},
	"sort_by":                 {},
	"sort_order":              {},
	"bonds_group_id":          {},
	"game_character_unit_id1": {},
	"game_character_unit_id2": {},
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
	if err := handler.enrichBondsHonorResponses(c.Request.Context(), region, items); err != nil {
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

	records, err := handler.masterDataSync.ListAll(c.Request.Context(), region, bondsHonorsEntity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, bondsHonorQueryErrorCode, "failed to list bonds honors")
		return
	}

	items := make([]shared.BondsHonorObjectResponse, 0, len(records))
	for _, record := range records {
		item := projectBondsHonor(record)
		if bondsHonorMatchesFilters(item, options.filters) {
			items = append(items, item)
		}
	}
	sortBondsHonorResponses(items, options.sortBy, options.descending)

	total := len(items)
	pageItems := paginateBondsHonorResponses(items, options.page, options.pageSize)
	if err := handler.enrichBondsHonorResponses(c.Request.Context(), region, pageItems); err != nil {
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
	options := bondsHonorListOptions{
		page:     1,
		pageSize: 20,
		sortBy:   "seq",
	}

	if rawPage, exists, ok := bondsHonorQueryValue(c, query, "page"); !ok {
		return bondsHonorListOptions{}, false
	} else if exists {
		page, err := strconv.Atoi(rawPage)
		if err != nil || page <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "page must be a positive integer")
			return bondsHonorListOptions{}, false
		}
		options.page = page
	}

	if rawPageSize, exists, ok := bondsHonorQueryValue(c, query, "page_size"); !ok {
		return bondsHonorListOptions{}, false
	} else if exists {
		pageSize, err := strconv.Atoi(rawPageSize)
		if err != nil || pageSize <= 0 || pageSize > maxLookupPageSize {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "page_size must be a positive integer no greater than 100")
			return bondsHonorListOptions{}, false
		}
		options.pageSize = pageSize
	}

	if rawSortBy, exists, ok := bondsHonorQueryValue(c, query, "sort_by"); !ok {
		return bondsHonorListOptions{}, false
	} else if exists {
		options.sortBy = rawSortBy
	}
	if _, valid := bondsHonorSortableFields[options.sortBy]; !valid {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "sort_by must be one of: bondsGroupId, gameCharacterUnitId1, gameCharacterUnitId2, honorRarity, id, name, seq")
		return bondsHonorListOptions{}, false
	}

	if rawSortOrder, exists, ok := bondsHonorQueryValue(c, query, "sort_order"); !ok {
		return bondsHonorListOptions{}, false
	} else if exists {
		switch strings.ToLower(rawSortOrder) {
		case "asc":
		case "desc":
			options.descending = true
		default:
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "sort_order must be one of: asc, desc")
			return bondsHonorListOptions{}, false
		}
	}

	var ok bool
	if options.filters.bondsGroupID, ok = parseBondsHonorIDFilter(c, query, "bonds_group_id"); !ok {
		return bondsHonorListOptions{}, false
	}
	if options.filters.gameCharacterUnitID1, ok = parseBondsHonorIDFilter(c, query, "game_character_unit_id1"); !ok {
		return bondsHonorListOptions{}, false
	}
	if options.filters.gameCharacterUnitID2, ok = parseBondsHonorIDFilter(c, query, "game_character_unit_id2"); !ok {
		return bondsHonorListOptions{}, false
	}

	return options, true
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

func sortBondsHonorResponses(items []shared.BondsHonorObjectResponse, sortBy string, descending bool) {
	sort.SliceStable(items, func(leftIndex int, rightIndex int) bool {
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

func compareBondsHonorSortField(left shared.BondsHonorObjectResponse, right shared.BondsHonorObjectResponse, sortBy string) int {
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

func compareBondsHonorStrings(left *string, right *string) int {
	if left == nil || right == nil {
		return 0
	}

	return strings.Compare(shared.NormalizeComparableText(*left), shared.NormalizeComparableText(*right))
}

func compareBondsHonorInt64(left *int64, right *int64) int {
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

func compareBondsHonorIDs(left *int64, right *int64) int {
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

func paginateBondsHonorResponses(items []shared.BondsHonorObjectResponse, page int, pageSize int) []shared.BondsHonorObjectResponse {
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

func (handler *LookupHandler) enrichBondsHonorResponses(ctx context.Context, region string, items []shared.BondsHonorObjectResponse) error {
	if len(items) == 0 {
		return nil
	}

	needsBonds := false
	needsCharacterUnits := false
	for _, item := range items {
		needsBonds = needsBonds || item.BondsGroupID != nil
		needsCharacterUnits = needsCharacterUnits || item.GameCharacterUnitID1 != nil || item.GameCharacterUnitID2 != nil
	}

	bondsGroups := make(map[int64]*shared.BondsHonorGroupResponse)
	if needsBonds {
		records, err := handler.masterDataSync.ListAll(ctx, region, bondsHonorBondsEntity)
		if err != nil {
			return err
		}
		for _, record := range records {
			groupID, ok := lookupInt64(record["groupId"])
			if !ok {
				continue
			}
			if _, exists := bondsGroups[groupID]; !exists {
				bondsGroups[groupID] = &shared.BondsHonorGroupResponse{
					GroupID:      lookupOptionalInt64(record["groupId"]),
					CharacterID1: lookupOptionalInt64(record["characterId1"]),
					CharacterID2: lookupOptionalInt64(record["characterId2"]),
				}
			}
		}
	}

	characterUnits := make(map[int64]*shared.BondsHonorCharacterUnitResponse)
	if needsCharacterUnits {
		records, err := handler.masterDataSync.ListAll(ctx, region, bondsHonorGameCharacterUnitsEntity)
		if err != nil {
			return err
		}
		for _, record := range records {
			id, ok := lookupInt64(record["id"])
			if !ok {
				continue
			}
			if _, exists := characterUnits[id]; !exists {
				characterUnits[id] = &shared.BondsHonorCharacterUnitResponse{
					ID:              lookupOptionalInt64(record["id"]),
					GameCharacterID: lookupOptionalInt64(record["gameCharacterId"]),
					Unit:            lookupOptionalString(record["unit"]),
				}
			}
		}
	}

	for index := range items {
		item := &items[index]
		if item.BondsGroupID != nil {
			item.BondsGroup = bondsGroups[*item.BondsGroupID]
		}
		if item.GameCharacterUnitID1 != nil {
			item.CharacterUnit1 = characterUnits[*item.GameCharacterUnitID1]
		}
		if item.GameCharacterUnitID2 != nil {
			item.CharacterUnit2 = characterUnits[*item.GameCharacterUnitID2]
		}
	}

	return nil
}
