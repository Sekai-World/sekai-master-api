package lookups

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
)

const (
	honorsEntity      = "honors"
	honorGroupsEntity = "honorgroups"
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
