package lookups

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
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

	group, err := handler.loadCostume3DGroup(c.Request.Context(), region, costume3DRecordSource(record))
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "COSTUME_3D_QUERY_ERROR", "failed to query 3D costume groups")
		return
	}
	normalized, ok := normalizeCostume3DRecord(costume3DRecordSource(record), group)
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

	rows, err := handler.loadCostume3DRows(c.Request.Context(), region)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "COSTUME_3D_QUERY_ERROR", "failed to list 3D costumes")
		return
	}
	rows = filterCostume3DRows(rows, numericFilters, nameFilter)

	if sortOptions.Enabled {
		if !shared.ValidateSortField(c, sortOptions.Field, nil, costume3DSortableFields) {
			return
		}
		sortCostume3DRows(rows, sortOptions.Field, sortOptions.Descending)
	}

	total := len(rows)
	pagedRows := pageCostume3DRows(rows, page, pageSize)
	items := make([]shared.Costume3DObjectResponse, 0, len(pagedRows))
	for _, row := range pagedRows {
		if normalized, ok := normalizeCostume3DRecord(row.source, row.group); ok {
			items = append(items, projectCostume3DRecord(normalized))
		}
	}

	response.JSON(c, http.StatusOK, shared.Costume3DListResponse{
		Items:      items,
		Pagination: lookupPaginationResponse(page, pageSize, total),
	})
}

// costume3DSource reads one costume or group record's fields: a decoded
// record, or a row of the entity's list projection.
type costume3DSource interface {
	value(key string) (any, bool)
}

type costume3DRecordSource map[string]any

func (record costume3DRecordSource) value(key string) (any, bool) {
	value, ok := record[key]
	return value, ok
}

type costume3DProjectionRow struct {
	projection *masterdata.Projection
	row        int
}

func (source costume3DProjectionRow) value(key string) (any, bool) {
	return source.projection.Value(key, source.row)
}

type costume3DFieldKind int

const (
	costume3DInt64 costume3DFieldKind = iota
	costume3DString
	costume3DTimestamp
)

// costume3DField is one normalized field: the costume's own keys are tried
// first, then its group's.
type costume3DField struct {
	name        string
	kind        costume3DFieldKind
	primaryKeys []string
	groupKeys   []string
}

var costume3DFields = []costume3DField{
	{name: "groupId", kind: costume3DInt64, primaryKeys: []string{"groupId", "costume3dGroupId"}, groupKeys: []string{"groupId"}},
	{name: "colorId", kind: costume3DInt64, primaryKeys: []string{"colorId", "costume3dColorId"}, groupKeys: []string{"colorId", "costume3dColorId"}},
	{name: "partType", kind: costume3DString, primaryKeys: []string{"partType", "costume3dPartType"}, groupKeys: []string{"partType", "costume3dPartType"}},
	{name: "seq", kind: costume3DInt64, primaryKeys: []string{"seq"}, groupKeys: []string{"seq"}},
	{name: "name", kind: costume3DString, primaryKeys: []string{"name"}, groupKeys: []string{"name"}},
	{name: "designer", kind: costume3DString, primaryKeys: []string{"designer"}, groupKeys: []string{"designer"}},
	{name: "characterId", kind: costume3DInt64, primaryKeys: []string{"characterId"}, groupKeys: []string{"characterId"}},
	{name: "rarity", kind: costume3DString, primaryKeys: []string{"rarity", "costume3dRarity"}, groupKeys: []string{"rarity", "costume3dRarity"}},
	{name: "type", kind: costume3DString, primaryKeys: []string{"type", "costume3dType"}, groupKeys: []string{"type", "costume3dType"}},
	{name: "assetbundleName", kind: costume3DString, primaryKeys: []string{"assetbundleName"}, groupKeys: []string{"assetbundleName"}},
	{name: "publishedAt", kind: costume3DTimestamp, primaryKeys: []string{"publishedAt"}, groupKeys: []string{"publishedAt"}},
}

var costume3DFieldsByName = func() map[string]costume3DField {
	fields := make(map[string]costume3DField, len(costume3DFields))
	for _, field := range costume3DFields {
		fields[field.name] = field
	}
	return fields
}()

// costume3DRow is one listed costume: its projection row, its group, and its
// normalized ID.
type costume3DRow struct {
	source costume3DSource
	group  costume3DSource
	id     int64
}

// loadCostume3DRows reads the costume and costume group list projections and
// returns every costume with a valid ID once, in stored order. Only the page
// is normalized into records, so the list never builds a record per costume.
func (handler *LookupHandler) loadCostume3DRows(ctx context.Context, region string) ([]costume3DRow, error) {
	costumes, err := handler.masterDataSync.LoadProjection(ctx, region, costume3dsEntity)
	if err != nil {
		return nil, err
	}
	groupProjection, err := handler.masterDataSync.LoadProjection(ctx, region, costume3dGroupsEntity)
	if err != nil {
		return nil, err
	}
	groups := make(map[string]costume3DSource, groupProjection.Len())
	for row := range groupProjection.Len() {
		source := costume3DProjectionRow{projection: groupProjection, row: row}
		if key := costume3DGroupID(source); key != "" {
			if _, exists := groups[key]; !exists {
				groups[key] = source
			}
		}
	}

	rows := make([]costume3DRow, 0, costumes.Len())
	seenIDs := make(map[int64]struct{}, costumes.Len())
	for row := range costumes.Len() {
		source := costume3DProjectionRow{projection: costumes, row: row}
		id, ok := costume3DID(source)
		if !ok {
			continue
		}
		if _, exists := seenIDs[id]; exists {
			continue
		}
		seenIDs[id] = struct{}{}
		rows = append(rows, costume3DRow{source: source, group: groups[costume3DGroupLookupKey(source)], id: id})
	}
	return rows, nil
}

// loadCostume3DGroup reads the group a costume belongs to through the
// costume3dgroups groupId index, or nil when it has none.
func (handler *LookupHandler) loadCostume3DGroup(ctx context.Context, region string, record costume3DSource) (costume3DSource, error) {
	groupID := costume3DGroupLookupKey(record)
	if groupID == "" {
		return nil, nil
	}
	matches, err := handler.masterDataSync.ListByIndex(ctx, region, costume3dGroupsEntity, "groupId", [][]any{{groupID}})
	if err != nil {
		return nil, err
	}
	if len(matches[0]) == 0 {
		return nil, nil
	}
	return costume3DRecordSource(matches[0][0]), nil
}

func filterCostume3DRows(rows []costume3DRow, numericFilters map[string][]float64, nameFilter string) []costume3DRow {
	if len(numericFilters) == 0 && nameFilter == "" {
		return rows
	}

	filtered := make([]costume3DRow, 0, len(rows))
	for _, row := range rows {
		if costume3DRowMatches(row, numericFilters, nameFilter) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func costume3DRowMatches(row costume3DRow, numericFilters map[string][]float64, nameFilter string) bool {
	for field, values := range numericFilters {
		value := row.fieldValue(field)
		number, ok := value.(int64)
		if value == nil || !ok || !slices.Contains(values, float64(number)) {
			return false
		}
	}
	return nameFilter == "" || strings.Contains(shared.NormalizeComparableText(row.fieldValue("name")), nameFilter)
}

// sortCostume3DRows orders rows like shared.SortResponseItems orders
// normalized records: missing values last, ties by ID.
func sortCostume3DRows(rows []costume3DRow, field string, descending bool) {
	keys := make([]any, len(rows))
	for index, row := range rows {
		keys[index] = row.fieldValue(field)
	}
	order := make([]int, len(rows))
	for index := range order {
		order[index] = index
	}
	sort.SliceStable(order, func(i int, j int) bool {
		left, right := keys[order[i]], keys[order[j]]
		if (left == nil) != (right == nil) {
			return left != nil
		}
		comparison := 0
		if left != nil {
			comparison = shared.CompareSortableValues(left, right)
		}
		if comparison == 0 {
			return shared.CompareIDValues(rows[order[i]].id, rows[order[j]].id) < 0
		}
		if descending {
			return comparison > 0
		}
		return comparison < 0
	})

	sorted := make([]costume3DRow, len(rows))
	for index, position := range order {
		sorted[index] = rows[position]
	}
	copy(rows, sorted)
}

func pageCostume3DRows(rows []costume3DRow, page int, pageSize int) []costume3DRow {
	totalPages := (len(rows) + pageSize - 1) / pageSize
	if page > totalPages {
		return []costume3DRow{}
	}

	start := (page - 1) * pageSize
	end := min(start+pageSize, len(rows))
	return rows[start:end]
}

// fieldValue returns a normalized field: int64, string, or nil when absent.
func (row costume3DRow) fieldValue(name string) any {
	if name == "id" {
		return row.id
	}
	field, ok := costume3DFieldsByName[name]
	if !ok {
		return nil
	}
	return costume3DFieldValue(field, row.source, row.group)
}

func normalizeCostume3DRecord(record costume3DSource, group costume3DSource) (map[string]any, bool) {
	id, ok := costume3DID(record)
	if !ok {
		return nil, false
	}

	normalized := map[string]any{"id": id}
	for _, field := range costume3DFields {
		if value := costume3DFieldValue(field, record, group); value != nil {
			normalized[field.name] = value
		}
	}
	return normalized, true
}

func costume3DID(record costume3DSource) (int64, bool) {
	value, _ := record.value("id")
	id, ok := lookupInt64(value)
	return id, ok && id > 0
}

func costume3DGroupID(group costume3DSource) string {
	value, _ := group.value("groupId")
	groupID, ok := lookupInt64(value)
	if !ok {
		return ""
	}
	return strconv.FormatInt(groupID, 10)
}

func costume3DGroupLookupKey(record costume3DSource) string {
	for _, field := range []string{"costume3dGroupId", "groupId"} {
		value, ok := record.value(field)
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

// costume3DFieldValue returns the first usable value of field from the
// costume's keys, then its group's: int64 for IDs and timestamps, string
// otherwise, or nil when neither has one.
func costume3DFieldValue(field costume3DField, primary costume3DSource, group costume3DSource) any {
	for _, source := range []struct {
		record costume3DSource
		keys   []string
	}{
		{record: primary, keys: field.primaryKeys},
		{record: group, keys: field.groupKeys},
	} {
		if source.record == nil {
			continue
		}
		for _, key := range source.keys {
			value, ok := source.record.value(key)
			if !ok || value == nil || isBlankCostume3DString(value) {
				continue
			}
			switch field.kind {
			case costume3DInt64:
				if parsed, ok := lookupInt64(value); ok {
					return parsed
				}
			case costume3DString:
				if text, ok := value.(string); ok {
					return text
				}
			case costume3DTimestamp:
				if millis, ok := costume3DTimestampMillis(value); ok {
					return millis
				}
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
