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
)

const (
	costume3dsEntity      = "costume3ds"
	costume3dGroupsEntity = "costume3dgroups"
)

var costume3DSortableFields = []string{
	"id",
	"groupId",
	"colorId",
	"partType",
	"seq",
	"name",
	"designer",
	"characterId",
	"rarity",
	"type",
	"assetbundleName",
	"publishedAt",
}

// Costume3DsByID godoc
// @Summary Get a 3D costume by id
// @Tags costume3ds
// @Produce json
// @Param region path string true "Region"
// @Param id path int true "3D costume ID" minimum(1)
// @Success 200 {object} shared.Costume3DObjectResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /costume3ds/{region}/{id} [get]
func (handler *LookupHandler) Costume3DsByID(c *gin.Context) {
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, costume3dsEntity) {
		return
	}

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, costume3dsEntity, id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "COSTUME_3D_QUERY_ERROR", "failed to query 3D costume")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "COSTUME_3D_NOT_FOUND", "3D costume not found")
		return
	}

	groups, err := handler.loadCostume3DGroups(c.Request.Context(), region)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "COSTUME_3D_QUERY_ERROR", "failed to query 3D costume groups")
		return
	}
	normalized, ok := normalizeCostume3DRecord(record, groups[costume3DGroupLookupKey(record)])
	if !ok {
		response.Error(c, http.StatusInternalServerError, "COSTUME_3D_QUERY_ERROR", "failed to normalize 3D costume")
		return
	}

	response.JSON(c, http.StatusOK, projectCostume3DRecord(normalized))
}

// Costume3DsList godoc
// @Summary List 3D costumes by page
// @Tags costume3ds
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param name query string false "Case-insensitive substring of the normalized costume name"
// @Param group_id query string false "Comma-separated group IDs"
// @Param color_id query string false "Comma-separated color IDs"
// @Param character_id query string false "Comma-separated character IDs"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.Costume3DListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /costume3ds/{region}/list [get]
func (handler *LookupHandler) Costume3DsList(c *gin.Context) {
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, costume3dsEntity) {
		return
	}

	sortOptions, ok := shared.ParseListSortOptions(c)
	if !ok {
		return
	}
	nameFilter := shared.NormalizeComparableText(c.Query("name"))
	numericFilters, ok := shared.ParseRecordFilters(c, map[string]string{
		"group_id":     "groupId",
		"color_id":     "colorId",
		"character_id": "characterId",
	})
	if !ok {
		return
	}

	records, err := handler.masterDataSync.ListAll(c.Request.Context(), region, costume3dsEntity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "COSTUME_3D_QUERY_ERROR", "failed to list 3D costumes")
		return
	}
	groups, err := handler.loadCostume3DGroups(c.Request.Context(), region)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "COSTUME_3D_QUERY_ERROR", "failed to query 3D costume groups")
		return
	}

	normalizedRecords := normalizeCostume3DRecords(records, groups)
	normalizedRecords = shared.FilterRecordsByNumbers(normalizedRecords, numericFilters)
	normalizedRecords = filterCostume3DRecordsByName(normalizedRecords, nameFilter)

	if sortOptions.Enabled {
		if !shared.ValidateSortField(c, sortOptions.Field, normalizedRecords, costume3DSortableFields) {
			return
		}
		shared.SortResponseItems(normalizedRecords, sortOptions.Field, sortOptions.Descending)
	}

	total := len(normalizedRecords)
	pagedRecords := pageCostume3DRecords(normalizedRecords, page, pageSize)
	items := make([]shared.Costume3DObjectResponse, 0, len(pagedRecords))
	for _, record := range pagedRecords {
		items = append(items, projectCostume3DRecord(record))
	}

	response.JSON(c, http.StatusOK, shared.Costume3DListResponse{
		Items:      items,
		Pagination: lookupPaginationResponse(page, pageSize, total),
	})
}

func (handler *LookupHandler) loadCostume3DGroups(ctx context.Context, region string) (map[string]map[string]any, error) {
	records, err := handler.masterDataSync.ListAll(ctx, region, costume3dGroupsEntity)
	if err != nil {
		return nil, err
	}

	groups := make(map[string]map[string]any, len(records))
	for _, record := range records {
		groupID, ok := lookupInt64(record["groupId"])
		if !ok {
			continue
		}
		key := strconv.FormatInt(groupID, 10)
		if _, exists := groups[key]; !exists {
			groups[key] = record
		}
	}

	return groups, nil
}

func normalizeCostume3DRecords(records []map[string]any, groups map[string]map[string]any) []map[string]any {
	items := make([]map[string]any, 0, len(records))
	seenIDs := make(map[string]struct{}, len(records))
	for _, record := range records {
		normalized, ok := normalizeCostume3DRecord(record, groups[costume3DGroupLookupKey(record)])
		if !ok {
			continue
		}

		normalizedID, ok := lookupInt64(normalized["id"])
		if !ok {
			continue
		}
		id := strconv.FormatInt(normalizedID, 10)
		if _, exists := seenIDs[id]; exists {
			continue
		}
		seenIDs[id] = struct{}{}
		items = append(items, normalized)
	}

	return items
}

func filterCostume3DRecordsByName(records []map[string]any, nameFilter string) []map[string]any {
	if nameFilter == "" {
		return records
	}

	filteredRecords := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if strings.Contains(shared.NormalizeComparableText(record["name"]), nameFilter) {
			filteredRecords = append(filteredRecords, record)
		}
	}

	return filteredRecords
}

func normalizeCostume3DRecord(record, group map[string]any) (map[string]any, bool) {
	id, ok := lookupInt64(record["id"])
	if !ok || id <= 0 {
		return nil, false
	}

	normalized := map[string]any{"id": id}
	addCostume3DInt64Field(normalized, "groupId", record, group, []string{"groupId", "costume3dGroupId"}, []string{"groupId"})
	addCostume3DInt64Field(normalized, "colorId", record, group, []string{"colorId", "costume3dColorId"}, []string{"colorId", "costume3dColorId"})
	addCostume3DStringField(normalized, "partType", record, group, []string{"partType", "costume3dPartType"}, []string{"partType", "costume3dPartType"})
	addCostume3DInt64Field(normalized, "seq", record, group, []string{"seq"}, []string{"seq"})
	addCostume3DStringField(normalized, "name", record, group, []string{"name"}, []string{"name"})
	addCostume3DStringField(normalized, "designer", record, group, []string{"designer"}, []string{"designer"})
	addCostume3DInt64Field(normalized, "characterId", record, group, []string{"characterId"}, []string{"characterId"})
	addCostume3DStringField(normalized, "rarity", record, group, []string{"rarity", "costume3dRarity"}, []string{"rarity", "costume3dRarity"})
	addCostume3DStringField(normalized, "type", record, group, []string{"type", "costume3dType"}, []string{"type", "costume3dType"})
	addCostume3DStringField(normalized, "assetbundleName", record, group, []string{"assetbundleName"}, []string{"assetbundleName"})
	addCostume3DTimestampField(normalized, "publishedAt", record, group, []string{"publishedAt"}, []string{"publishedAt"})

	return normalized, true
}

func costume3DGroupLookupKey(record map[string]any) string {
	for _, field := range []string{"costume3dGroupId", "groupId"} {
		value, ok := record[field]
		if !ok || value == nil {
			continue
		}
		groupID, ok := lookupInt64(value)
		if ok {
			return strconv.FormatInt(groupID, 10)
		}
	}

	return ""
}

func addCostume3DInt64Field(target map[string]any, field string, primary map[string]any, group map[string]any, primaryKeys []string, groupKeys []string) {
	if value := costume3DInt64Field(primary, group, primaryKeys, groupKeys); value != nil {
		target[field] = *value
	}
}

func costume3DInt64Field(primary map[string]any, group map[string]any, primaryKeys []string, groupKeys []string) *int64 {
	for _, source := range []struct {
		record map[string]any
		keys   []string
	}{
		{record: primary, keys: primaryKeys},
		{record: group, keys: groupKeys},
	} {
		for _, key := range source.keys {
			value, ok := source.record[key]
			if !ok || value == nil || isBlankCostume3DString(value) {
				continue
			}
			parsed, ok := lookupInt64(value)
			if ok {
				return &parsed
			}
		}
	}

	return nil
}

func addCostume3DStringField(target map[string]any, field string, primary map[string]any, group map[string]any, primaryKeys []string, groupKeys []string) {
	if value := costume3DStringField(primary, group, primaryKeys, groupKeys); value != nil {
		target[field] = *value
	}
}

func costume3DStringField(primary map[string]any, group map[string]any, primaryKeys []string, groupKeys []string) *string {
	for _, source := range []struct {
		record map[string]any
		keys   []string
	}{
		{record: primary, keys: primaryKeys},
		{record: group, keys: groupKeys},
	} {
		for _, key := range source.keys {
			value, ok := source.record[key]
			if !ok || value == nil || isBlankCostume3DString(value) {
				continue
			}
			text, ok := value.(string)
			if ok {
				return &text
			}
		}
	}

	return nil
}

func addCostume3DTimestampField(target map[string]any, field string, primary map[string]any, group map[string]any, primaryKeys []string, groupKeys []string) {
	if value := costume3DTimestampField(primary, group, primaryKeys, groupKeys); value != nil {
		target[field] = *value
	}
}

func costume3DTimestampField(primary map[string]any, group map[string]any, primaryKeys []string, groupKeys []string) *int64 {
	for _, source := range []struct {
		record map[string]any
		keys   []string
	}{
		{record: primary, keys: primaryKeys},
		{record: group, keys: groupKeys},
	} {
		for _, key := range source.keys {
			value, ok := source.record[key]
			if !ok || value == nil || isBlankCostume3DString(value) {
				continue
			}
			millis, ok := costume3DTimestampMillis(value)
			if ok {
				return &millis
			}
		}
	}

	return nil
}

func costume3DTimestampMillis(value any) (int64, bool) {
	if timestamp, ok := value.(time.Time); ok {
		return timestamp.UTC().UnixMilli(), true
	}
	if text, ok := value.(string); ok {
		text = strings.TrimSpace(text)
		if millis, err := strconv.ParseInt(text, 10, 64); err == nil {
			return millis, true
		}
		if timestamp, err := time.Parse(time.RFC3339Nano, text); err == nil {
			return timestamp.UTC().UnixMilli(), true
		}
		return 0, false
	}

	return lookupInt64(value)
}

func isBlankCostume3DString(value any) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) == ""
}

func projectCostume3DRecord(record map[string]any) shared.Costume3DObjectResponse {
	return shared.Costume3DObjectResponse{
		ID:              lookupRequiredInt64(record["id"]),
		GroupID:         lookupOptionalInt64(record["groupId"]),
		ColorID:         lookupOptionalInt64(record["colorId"]),
		PartType:        lookupOptionalString(record["partType"]),
		Seq:             lookupOptionalInt64(record["seq"]),
		Name:            lookupOptionalString(record["name"]),
		Designer:        lookupOptionalString(record["designer"]),
		CharacterID:     lookupOptionalInt64(record["characterId"]),
		Rarity:          lookupOptionalString(record["rarity"]),
		Type:            lookupOptionalString(record["type"]),
		AssetbundleName: lookupOptionalString(record["assetbundleName"]),
		PublishedAt:     lookupOptionalInt64(record["publishedAt"]),
	}
}

func pageCostume3DRecords(records []map[string]any, page int, pageSize int) []map[string]any {
	totalPages := (len(records) + pageSize - 1) / pageSize
	if page > totalPages {
		return []map[string]any{}
	}

	start := (page - 1) * pageSize
	end := start + pageSize
	if end > len(records) {
		end = len(records)
	}
	return records[start:end]
}
