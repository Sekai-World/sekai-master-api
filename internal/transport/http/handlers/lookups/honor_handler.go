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
	honorsEntity                     = "honors"
	honorGroupsEntity                = "honorgroups"
	honorGroupsDefaultPageSize       = 12
	honorGroupsMaximumPageSize       = 24
	masterDataServiceNotReadyMessage = "master data service is not ready"
)

// HonorsByID godoc
// @Summary Get an honor by id
// @Tags honors
// @Produce json
// @Param region path string true "Region"
// @Param id path int true "Honor ID" minimum(1)
// @Success 200 {object} shared.HonorObjectResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /honors/{region}/{id} [get]
func (handler *LookupHandler) HonorsByID(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", masterDataServiceNotReadyMessage)
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, honorsEntity) {
		return
	}

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, honorsEntity, id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "HONOR_QUERY_ERROR", "failed to query honor")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "HONOR_NOT_FOUND", "honor not found")
		return
	}

	honor := projectHonor(record)
	if honor.GroupID > 0 {
		honor.Group, err = handler.honorGroupByID(c.Request.Context(), region, honor.GroupID)
		if err != nil {
			response.Error(c, http.StatusInternalServerError, "HONOR_QUERY_ERROR", "failed to query honor group")
			return
		}
	}

	response.JSON(c, http.StatusOK, honor)
}

// HonorsList godoc
// @Summary List honors by page
// @Tags honors
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Success 200 {object} shared.HonorListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /honors/{region}/list [get]
func (handler *LookupHandler) HonorsList(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", masterDataServiceNotReadyMessage)
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, honorsEntity) {
		return
	}

	records, total, err := handler.masterDataSync.ListByPage(c.Request.Context(), region, honorsEntity, page, pageSize)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "HONOR_QUERY_ERROR", "failed to list honors")
		return
	}

	items, err := handler.honorResponses(c.Request.Context(), region, records)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "HONOR_QUERY_ERROR", "failed to query honor groups")
		return
	}

	response.JSON(c, http.StatusOK, shared.HonorListResponse{
		Items:      items,
		Pagination: lookupPaginationResponse(page, pageSize, total),
	})
}

// HonorGroupsList godoc
// @Summary List honor groups by page
// @Tags honorGroups
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(24) default(12)
// @Param name query string false "Case-insensitive substring of the group or nested honor name"
// @Param honor_type query string false "Exact honor group type filter"
// @Param sort_by query string false "Sort field" Enums(id) default(id)
// @Param sort_order query string false "Sort order" Enums(asc,desc) default(asc)
// @Success 200 {object} shared.HonorGroupListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /honorGroups/{region}/list [get]
func (handler *LookupHandler) HonorGroupsList(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", masterDataServiceNotReadyMessage)
		return
	}

	region, ok := parseTypedLookupRegion(c, handler.masterDataSync)
	if !ok {
		return
	}
	page, pageSize, ok := parseHonorGroupsPagination(c)
	if !ok {
		return
	}
	descending, ok := parseHonorGroupsSortOptions(c)
	if !ok {
		return
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, honorsEntity) {
		return
	}

	honorRecords, err := handler.masterDataSync.ListAll(c.Request.Context(), region, honorsEntity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "HONOR_QUERY_ERROR", "failed to list honors")
		return
	}
	groupRecords, err := handler.masterDataSync.ListAll(c.Request.Context(), region, honorGroupsEntity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "HONOR_QUERY_ERROR", "failed to list honor groups")
		return
	}

	items := buildHonorGroupResponses(groupRecords, honorRecords)
	availableHonorTypes := listAvailableHonorTypes(items)
	if honorType, hasHonorType := c.GetQuery("honor_type"); hasHonorType {
		filteredItems := make([]shared.HonorGroupObjectResponse, 0, len(items))
		for _, item := range items {
			if honorType != "" && item.HonorType != nil && *item.HonorType == honorType {
				filteredItems = append(filteredItems, item)
			}
		}
		items = filteredItems
	}
	if name := shared.NormalizeComparableText(c.Query("name")); name != "" {
		filteredItems := make([]shared.HonorGroupObjectResponse, 0, len(items))
		for _, item := range items {
			if honorGroupMatchesName(item, name) {
				filteredItems = append(filteredItems, item)
			}
		}
		items = filteredItems
	}

	sortHonorGroupResponses(items, descending)
	pageItems := paginateHonorGroupResponses(items, page, pageSize)
	response.JSON(c, http.StatusOK, shared.HonorGroupListResponse{
		Items:               pageItems,
		AvailableHonorTypes: availableHonorTypes,
		Pagination:          lookupPaginationResponse(page, pageSize, len(items)),
	})
}

func listAvailableHonorTypes(items []shared.HonorGroupObjectResponse) []string {
	types := make([]string, 0)
	seen := make(map[string]struct{})
	for _, item := range items {
		if item.HonorType == nil || *item.HonorType == "" {
			continue
		}
		if _, exists := seen[*item.HonorType]; exists {
			continue
		}

		seen[*item.HonorType] = struct{}{}
		types = append(types, *item.HonorType)
	}
	sort.Strings(types)
	return types
}

func parseHonorGroupsPagination(c *gin.Context) (int, int, bool) {
	page := 1
	if rawPage := strings.TrimSpace(c.Query("page")); rawPage != "" {
		parsedPage, err := strconv.Atoi(rawPage)
		if err != nil || parsedPage <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "page must be a positive integer")
			return 0, 0, false
		}
		page = parsedPage
	}

	pageSize := honorGroupsDefaultPageSize
	if rawPageSize := strings.TrimSpace(c.Query("page_size")); rawPageSize != "" {
		parsedPageSize, err := strconv.Atoi(rawPageSize)
		if err != nil || parsedPageSize <= 0 || parsedPageSize > honorGroupsMaximumPageSize {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "page_size must be a positive integer no greater than 24")
			return 0, 0, false
		}
		pageSize = parsedPageSize
	}

	return page, pageSize, true
}

func parseHonorGroupsSortOptions(c *gin.Context) (bool, bool) {
	sortBy := strings.TrimSpace(c.Query("sort_by"))
	if sortBy == "" {
		sortBy = "id"
	}
	if sortBy != "id" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "sort_by must be one of: id")
		return false, false
	}

	switch strings.ToLower(strings.TrimSpace(c.Query("sort_order"))) {
	case "", "asc":
		return false, true
	case "desc":
		return true, true
	default:
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "sort_order must be one of: asc, desc")
		return false, false
	}
}

func honorGroupMatchesName(item shared.HonorGroupObjectResponse, name string) bool {
	if item.Name != nil && strings.Contains(shared.NormalizeComparableText(*item.Name), name) {
		return true
	}
	for _, honor := range item.Honors {
		if strings.Contains(shared.NormalizeComparableText(honor.Name), name) {
			return true
		}
	}

	return false
}

func sortHonorGroupResponses(items []shared.HonorGroupObjectResponse, descending bool) {
	sort.Slice(items, func(leftIndex, rightIndex int) bool {
		if descending {
			return items[leftIndex].ID > items[rightIndex].ID
		}
		return items[leftIndex].ID < items[rightIndex].ID
	})
}

func buildHonorGroupResponses(
	groupRecords, honorRecords []map[string]any,
) []shared.HonorGroupObjectResponse {
	honorsByGroupID := make(map[int64][]shared.HonorObjectResponse)
	for _, record := range honorRecords {
		groupID, ok := lookupInt64(record["groupId"])
		if !ok || groupID <= 0 {
			continue
		}

		honor := projectHonor(record)
		honorsByGroupID[groupID] = append(honorsByGroupID[groupID], honor)
	}

	items := make([]shared.HonorGroupObjectResponse, 0, len(groupRecords))
	seenGroupIDs := make(map[int64]struct{}, len(groupRecords))
	for _, record := range groupRecords {
		groupID, ok := lookupInt64(record["id"])
		if !ok || groupID <= 0 {
			continue
		}
		if _, seen := seenGroupIDs[groupID]; seen {
			continue
		}

		honors, hasHonors := honorsByGroupID[groupID]
		if !hasHonors {
			continue
		}

		group := projectHonorGroup(record)
		for index := range honors {
			honors[index].Group = group
		}
		items = append(items, shared.HonorGroupObjectResponse{
			ID:                        groupID,
			Name:                      group.Name,
			HonorType:                 group.HonorType,
			BackgroundAssetbundleName: group.BackgroundAssetbundleName,
			FrameName:                 group.FrameName,
			Honors:                    honors,
		})
		seenGroupIDs[groupID] = struct{}{}
	}

	return items
}

func paginateHonorGroupResponses(
	items []shared.HonorGroupObjectResponse,
	page int,
	pageSize int,
) []shared.HonorGroupObjectResponse {
	totalPages := (len(items) + pageSize - 1) / pageSize
	if page > totalPages {
		return []shared.HonorGroupObjectResponse{}
	}

	start := (page - 1) * pageSize
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}

	return items[start:end]
}

func (handler *LookupHandler) honorResponses(ctx context.Context, region string, records []map[string]any) ([]shared.HonorObjectResponse, error) {
	items := make([]shared.HonorObjectResponse, 0, len(records))
	groups := make(map[int64]*shared.HonorGroupResponse, len(records))
	queriedGroups := make(map[int64]bool, len(records))

	for _, record := range records {
		honor := projectHonor(record)
		if honor.GroupID > 0 && !queriedGroups[honor.GroupID] {
			group, err := handler.honorGroupByID(ctx, region, honor.GroupID)
			if err != nil {
				return nil, err
			}
			groups[honor.GroupID] = group
			queriedGroups[honor.GroupID] = true
		}
		if honor.GroupID > 0 {
			honor.Group = groups[honor.GroupID]
		}
		items = append(items, honor)
	}

	return items, nil
}

func (handler *LookupHandler) honorGroupByID(ctx context.Context, region string, groupID int64) (*shared.HonorGroupResponse, error) {
	group, found, err := handler.masterDataSync.GetByID(ctx, region, honorGroupsEntity, strconv.FormatInt(groupID, 10))
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}

	return projectHonorGroup(group), nil
}

func projectHonor(record map[string]any) shared.HonorObjectResponse {
	return shared.HonorObjectResponse{
		ID:               lookupRequiredInt64(record["id"]),
		Seq:              lookupRequiredInt64(record["seq"]),
		GroupID:          lookupRequiredInt64(record["groupId"]),
		Name:             lookupString(record["name"]),
		HonorRarity:      lookupString(record["honorRarity"]),
		HonorMissionType: lookupOptionalString(record["honorMissionType"]),
		HonorType:        lookupOptionalString(record["honorType"]),
		AssetbundleName:  lookupString(record["assetbundleName"]),
		Levels:           projectHonorLevels(record["levels"]),
	}
}

func projectHonorLevels(value any) []shared.HonorLevelResponse {
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
	}

	items := make([]shared.HonorLevelResponse, 0, len(records))
	for _, record := range records {
		items = append(items, shared.HonorLevelResponse{
			HonorID:         lookupOptionalInt64(record["honorId"]),
			Level:           lookupOptionalInt64(record["level"]),
			Bonus:           lookupOptionalFloat64(record["bonus"]),
			Description:     lookupOptionalString(record["description"]),
			HonorRarity:     lookupOptionalString(record["honorRarity"]),
			AssetbundleName: lookupOptionalString(record["assetbundleName"]),
		})
	}

	return items
}

func projectHonorGroup(record map[string]any) *shared.HonorGroupResponse {
	return &shared.HonorGroupResponse{
		ID:                        lookupOptionalInt64(record["id"]),
		Name:                      lookupOptionalString(record["name"]),
		HonorType:                 lookupOptionalString(record["honorType"]),
		BackgroundAssetbundleName: lookupOptionalString(record["backgroundAssetbundleName"]),
		FrameName:                 lookupOptionalString(record["frameName"]),
	}
}
