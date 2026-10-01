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
	mysekaiMaterialsEntity                 = "mysekaimaterials"
	mysekaiSitesEntity                     = "mysekaisites"
	mysekaiMaterialCharacterRelationEntity = "mysekaimaterialgamecharacterrelations"
)

// MysekaiMaterialsByID godoc
// @Summary Get a MySekai material by id
// @Description Returns a material with the sites it is gathered at, its related characters, and the fixtures whose blueprints cost it.
// @Tags mysekaiMaterials
// @Produce json
// @Param region path string true "Region"
// @Param id path int true "Material ID" minimum(1)
// @Success 200 {object} shared.MysekaiMaterialDetailResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /mysekaiMaterials/{region}/{id} [get]
func (handler *LookupHandler) MysekaiMaterialsByID(c *gin.Context) {
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, mysekaiMaterialsEntity) {
		return
	}

	ctx := c.Request.Context()
	record, found, err := handler.masterDataSync.GetByID(ctx, region, mysekaiMaterialsEntity, id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MYSEKAI_MATERIAL_QUERY_ERROR", "failed to query MySekai material")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "MYSEKAI_MATERIAL_NOT_FOUND", "MySekai material not found")
		return
	}

	materials, err := handler.enrichMysekaiMaterials(ctx, region, []fieldSource{recordSource(record)})
	if err != nil || len(materials) == 0 {
		response.Error(c, http.StatusInternalServerError, "MYSEKAI_MATERIAL_QUERY_ERROR", "failed to query MySekai material details")
		return
	}
	usedBy, err := handler.loadMysekaiMaterialUses(ctx, region, materials[0].ID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MYSEKAI_MATERIAL_QUERY_ERROR", "failed to query MySekai material uses")
		return
	}

	material := materials[0]
	response.JSON(c, http.StatusOK, shared.MysekaiMaterialDetailResponse{
		ID:                        material.ID,
		Name:                      material.Name,
		MysekaiMaterialType:       material.MysekaiMaterialType,
		MysekaiMaterialRarityType: material.MysekaiMaterialRarityType,
		IconAssetbundleName:       material.IconAssetbundleName,
		Seq:                       material.Seq,
		Pronunciation:             material.Pronunciation,
		Description:               material.Description,
		Sites:                     material.Sites,
		GameCharacterIDs:          material.GameCharacterIDs,
		UsedBy:                    usedBy,
	})
}

// MysekaiMaterialsList godoc
// @Summary List MySekai materials by page
// @Description Lists materials in stored order with the sites they are gathered at and their related characters.
// @Tags mysekaiMaterials
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param material_type query string false "Comma-separated material types, such as wood or mineral"
// @Success 200 {object} shared.MysekaiMaterialListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /mysekaiMaterials/{region}/list [get]
func (handler *LookupHandler) MysekaiMaterialsList(c *gin.Context) {
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
	materialTypes := parseCommaSeparatedValues(c.Query("material_type"))
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, mysekaiMaterialsEntity) {
		return
	}

	ctx := c.Request.Context()
	projection, err := handler.masterDataSync.LoadProjection(ctx, region, mysekaiMaterialsEntity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MYSEKAI_MATERIAL_QUERY_ERROR", "failed to list MySekai materials")
		return
	}
	sources := projectionSourcesByType(projection, "mysekaiMaterialType", materialTypes)

	items, err := handler.enrichMysekaiMaterials(ctx, region, pageSlice(sources, page, pageSize))
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MYSEKAI_MATERIAL_QUERY_ERROR", "failed to list MySekai material details")
		return
	}
	response.JSON(c, http.StatusOK, shared.MysekaiMaterialListResponse{
		Items:      items,
		Pagination: lookupPaginationResponse(page, pageSize, len(sources)),
	})
}

// enrichMysekaiMaterials projects materials with their sites and characters,
// reading each related entity once for all of them.
func (handler *LookupHandler) enrichMysekaiMaterials(ctx context.Context, region string, sources []fieldSource) ([]shared.MysekaiMaterialResponse, error) {
	items := make([]shared.MysekaiMaterialResponse, 0, len(sources))
	siteIDs := []string{}
	lookups := make([][]any, 0, len(sources))
	for _, source := range sources {
		summary, ok := projectMysekaiMaterialSummary(source)
		if !ok {
			continue
		}
		items = append(items, shared.MysekaiMaterialResponse{
			ID:                        summary.ID,
			Name:                      summary.Name,
			MysekaiMaterialType:       summary.MysekaiMaterialType,
			MysekaiMaterialRarityType: summary.MysekaiMaterialRarityType,
			IconAssetbundleName:       summary.IconAssetbundleName,
			Seq:                       sourceOptionalInt64(source, "seq"),
			Pronunciation:             sourceOptionalString(source, "pronunciation"),
			Description:               sourceOptionalString(source, "description"),
			Sites:                     []shared.MysekaiSiteResponse{},
			GameCharacterIDs:          []int64{},
		})
		siteIDs = append(siteIDs, formatIDs(mysekaiMaterialSiteIDs(source))...)
		lookups = append(lookups, []any{summary.ID})
	}
	if len(items) == 0 {
		return items, nil
	}

	sites, err := shared.PrefetchRecords(ctx, handler.masterDataSync, region, map[string][]string{mysekaiSitesEntity: siteIDs})
	if err != nil {
		return nil, err
	}
	relations, err := handler.masterDataSync.ListByIndex(ctx, region, mysekaiMaterialCharacterRelationEntity, "mysekaiMaterialId", lookups)
	if err != nil {
		return nil, err
	}

	position := 0
	for _, source := range sources {
		if _, ok := sourceID(source); !ok {
			continue
		}
		item := &items[position]
		for _, siteID := range mysekaiMaterialSiteIDs(source) {
			if site, ok := sites.Record(mysekaiSitesEntity, formatID(siteID)); ok {
				item.Sites = append(item.Sites, shared.MysekaiSiteResponse{ID: siteID, Name: sourceString(recordSource(site), "name")})
			}
		}
		for _, relation := range relations[position] {
			if characterID, ok := lookupInt64(relation["gameCharacterId"]); ok && characterID > 0 {
				item.GameCharacterIDs = appendUniqueID(item.GameCharacterIDs, characterID)
			}
		}
		position++
	}
	return items, nil
}

func mysekaiMaterialSiteIDs(source fieldSource) []int64 {
	value, _ := source.value("mysekaiSiteIds")
	rawIDs, _ := value.([]any)
	ids := make([]int64, 0, len(rawIDs))
	for _, raw := range rawIDs {
		if id, ok := lookupInt64(raw); ok && id > 0 {
			ids = appendUniqueID(ids, id)
		}
	}
	return ids
}

func projectMysekaiMaterialSummary(source fieldSource) (shared.MysekaiMaterialSummaryResponse, bool) {
	id, ok := sourceID(source)
	if !ok {
		return shared.MysekaiMaterialSummaryResponse{}, false
	}
	return shared.MysekaiMaterialSummaryResponse{
		ID:                        id,
		Name:                      sourceString(source, "name"),
		MysekaiMaterialType:       sourceOptionalString(source, "mysekaiMaterialType"),
		MysekaiMaterialRarityType: sourceOptionalString(source, "mysekaiMaterialRarityType"),
		IconAssetbundleName:       sourceOptionalString(source, "iconAssetbundleName"),
	}, true
}

// loadMysekaiMaterialUses returns the fixtures whose blueprints cost the
// material and how much one craft costs, in fixture ID order.
func (handler *LookupHandler) loadMysekaiMaterialUses(ctx context.Context, region string, materialID int64) ([]shared.MysekaiMaterialUseResponse, error) {
	uses := []shared.MysekaiMaterialUseResponse{}
	costs, err := handler.masterDataSync.ListByIndex(ctx, region, mysekaiBlueprintMaterialCostsEntity, "mysekaiMaterialId", [][]any{{materialID}})
	if err != nil {
		return nil, err
	}
	if len(costs[0]) == 0 {
		return uses, nil
	}

	quantityByBlueprint := map[string]int64{}
	blueprintIDs := make([]string, 0, len(costs[0]))
	for _, cost := range costs[0] {
		blueprintID := shared.NormalizeAnyID(cost["mysekaiBlueprintId"])
		if blueprintID == "" {
			continue
		}
		quantityByBlueprint[blueprintID] += lookupRequiredInt64(cost["quantity"])
		blueprintIDs = append(blueprintIDs, blueprintID)
	}
	prefetched, err := shared.PrefetchRecords(ctx, handler.masterDataSync, region, map[string][]string{mysekaiBlueprintsEntity: blueprintIDs})
	if err != nil {
		return nil, err
	}

	quantityByFixture := map[int64]int64{}
	fixtureIDs := []int64{}
	for blueprintID, quantity := range quantityByBlueprint {
		blueprint, ok := prefetched.Record(mysekaiBlueprintsEntity, blueprintID)
		if !ok || lookupString(blueprint["mysekaiCraftType"]) != mysekaiFixtureCraftType {
			continue
		}
		fixtureID, ok := lookupInt64(blueprint["craftTargetId"])
		if !ok || fixtureID <= 0 {
			continue
		}
		fixtureIDs = appendUniqueID(fixtureIDs, fixtureID)
		quantityByFixture[fixtureID] += quantity
	}
	slices.Sort(fixtureIDs)
	if err := prefetched.Add(ctx, map[string][]string{mysekaiFixturesEntity: formatIDs(fixtureIDs)}); err != nil {
		return nil, err
	}
	for _, fixtureID := range fixtureIDs {
		record, ok := prefetched.Record(mysekaiFixturesEntity, formatID(fixtureID))
		if !ok {
			continue
		}
		source := recordSource(record)
		uses = append(uses, shared.MysekaiMaterialUseResponse{
			Fixture: shared.MysekaiFixtureSummaryResponse{
				ID:                        fixtureID,
				Name:                      sourceString(source, "name"),
				MysekaiFixtureType:        sourceOptionalString(source, "mysekaiFixtureType"),
				MysekaiSettableLayoutType: sourceOptionalString(source, "mysekaiSettableLayoutType"),
				AssetbundleName:           sourceOptionalString(source, "assetbundleName"),
			},
			Quantity: quantityByFixture[fixtureID],
		})
	}
	return uses, nil
}
