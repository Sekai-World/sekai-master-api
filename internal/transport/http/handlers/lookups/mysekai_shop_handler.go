package lookups

import (
	"context"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
)

const (
	mysekaiShopsEntity     = "mysekaishops"
	mysekaiShopCostsEntity = "mysekaishopcosts"
	mysekaiToolsEntity     = "mysekaitools"
	mysekaiShopBoxPurpose  = "mysekai_shop"
)

// MysekaiShopsList godoc
// @Summary List MySekai secret shop items by page
// @Description Lists the secret shop's material and tool items in stored order, each with its costs, purchase limit, and the MySekai materials or tools it sells.
// @Tags mysekaiShops
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param shop_type query string false "Comma-separated shop types, such as material or tool"
// @Success 200 {object} shared.MysekaiShopListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /mysekaiShops/{region}/list [get]
func (handler *LookupHandler) MysekaiShopsList(c *gin.Context) {
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
	shopTypes := parseCommaSeparatedValues(c.Query("shop_type"))
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, mysekaiShopsEntity) {
		return
	}

	ctx := c.Request.Context()
	projection, err := handler.masterDataSync.LoadProjection(ctx, region, mysekaiShopsEntity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MYSEKAI_SHOP_QUERY_ERROR", "failed to list MySekai shop items")
		return
	}
	sources := make([]fieldSource, 0, projection.Len())
	seen := make(map[int64]struct{}, projection.Len())
	for row := range projection.Len() {
		source := projectionRowSource{projection: projection, row: row}
		id, ok := sourceID(source)
		if !ok {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		if len(shopTypes) > 0 && !slices.Contains(shopTypes, sourceString(source, "mysekaiShopType")) {
			continue
		}
		sources = append(sources, source)
	}

	items, err := handler.enrichMysekaiShopItems(ctx, region, pageSlice(sources, page, pageSize))
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MYSEKAI_SHOP_QUERY_ERROR", "failed to list MySekai shop item details")
		return
	}
	response.JSON(c, http.StatusOK, shared.MysekaiShopListResponse{
		Items:      items,
		Pagination: lookupPaginationResponse(page, pageSize, len(sources)),
	})
}

// enrichMysekaiShopItems projects shop items with their costs and the
// resources their mysekai_shop resource boxes hold, reading each related
// entity once for all of them.
func (handler *LookupHandler) enrichMysekaiShopItems(ctx context.Context, region string, sources []fieldSource) ([]shared.MysekaiShopItemResponse, error) {
	items := make([]shared.MysekaiShopItemResponse, 0, len(sources))
	if len(sources) == 0 {
		return items, nil
	}

	lookups := make([][]any, 0, len(sources))
	boxKeys := make([]map[string]any, 0, len(sources))
	for _, source := range sources {
		id, _ := sourceID(source)
		boxID, _ := sourceInt64(source, "resourceBoxId")
		lookups = append(lookups, []any{id})
		boxKeys = append(boxKeys, map[string]any{"id": formatID(boxID), "resourceBoxPurpose": mysekaiShopBoxPurpose})
	}
	costs, err := handler.masterDataSync.ListByIndex(ctx, region, mysekaiShopCostsEntity, "mysekaiShopId", lookups)
	if err != nil {
		return nil, err
	}
	boxes, err := handler.masterDataSync.GetByCompositeKeys(ctx, region, "resourceboxes", boxKeys)
	if err != nil {
		return nil, err
	}

	details, idsByEntity := mysekaiShopBoxDetails(boxes)
	resources, err := shared.PrefetchRecords(ctx, handler.masterDataSync, region, idsByEntity)
	if err != nil {
		return nil, err
	}

	for position, source := range sources {
		id, _ := sourceID(source)
		item := shared.MysekaiShopItemResponse{
			ID:                            id,
			Seq:                           sourceOptionalInt64(source, "seq"),
			MysekaiShopType:               sourceString(source, "mysekaiShopType"),
			MysekaiShopExchangeLimitType:  sourceOptionalString(source, "mysekaiShopExchangeLimitType"),
			MysekaiShopExchangeLimitValue: sourceOptionalInt64(source, "mysekaiShopExchangeLimitValue"),
			Costs:                         projectMysekaiShopCosts(costs[position]),
			Resources:                     []shared.MysekaiShopResourceResponse{},
		}
		for _, detail := range details[position] {
			item.Resources = append(item.Resources, projectMysekaiShopResource(detail, resources))
		}
		items = append(items, item)
	}
	return items, nil
}

// mysekaiShopBoxDetails returns each box's details in seq order, and the IDs
// of the materials and tools they name, by entity.
func mysekaiShopBoxDetails(boxes []map[string]any) ([][]map[string]any, map[string][]string) {
	details := make([][]map[string]any, len(boxes))
	idsByEntity := map[string][]string{}
	for position, box := range boxes {
		rawDetails, _ := box["details"].([]any)
		for _, raw := range rawDetails {
			detail, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			details[position] = append(details[position], detail)
			if entity, ok := mysekaiShopResourceEntities[lookupString(detail["resourceType"])]; ok {
				idsByEntity[entity] = append(idsByEntity[entity], shared.NormalizeAnyID(detail["resourceId"]))
			}
		}
		sortRecordsBySeq(details[position])
	}
	return details, idsByEntity
}

// projectMysekaiShopCosts returns a shop item's costs in seq order.
func projectMysekaiShopCosts(records []map[string]any) []shared.MysekaiShopCostResponse {
	sortRecordsBySeq(records)
	costs := make([]shared.MysekaiShopCostResponse, 0, len(records))
	for _, cost := range records {
		costs = append(costs, shared.MysekaiShopCostResponse{
			ResourceType: lookupString(cost["resourceType"]),
			ResourceID:   lookupOptionalInt64(cost["resourceId"]),
			Quantity:     lookupRequiredInt64(cost["quantity"]),
		})
	}
	return costs
}

// mysekaiShopResourceEntities maps the resource types the shop sells to the
// entity that describes them.
var mysekaiShopResourceEntities = map[string]string{
	"mysekai_material": mysekaiMaterialsEntity,
	"mysekai_tool":     mysekaiToolsEntity,
}

func projectMysekaiShopResource(detail map[string]any, records *shared.PrefetchedRecords) shared.MysekaiShopResourceResponse {
	resourceType := lookupString(detail["resourceType"])
	resource := shared.MysekaiShopResourceResponse{
		ResourceType:     resourceType,
		ResourceID:       lookupOptionalInt64(detail["resourceId"]),
		ResourceQuantity: lookupRequiredInt64(detail["resourceQuantity"]),
	}
	entity, ok := mysekaiShopResourceEntities[resourceType]
	if !ok {
		return resource
	}
	record, ok := records.Record(entity, shared.NormalizeAnyID(detail["resourceId"]))
	if !ok {
		return resource
	}
	source := recordSource(record)
	resource.Name = sourceOptionalString(source, "name")
	resource.Description = sourceOptionalString(source, "description")
	switch resourceType {
	case "mysekai_material":
		resource.AssetbundleName = sourceOptionalString(source, "iconAssetbundleName")
		resource.MysekaiMaterialType = sourceOptionalString(source, "mysekaiMaterialType")
		resource.MysekaiMaterialRarityType = sourceOptionalString(source, "mysekaiMaterialRarityType")
	case "mysekai_tool":
		resource.AssetbundleName = sourceOptionalString(source, "assetbundleName")
		resource.MysekaiToolType = sourceOptionalString(source, "mysekaiToolType")
	}
	return resource
}
