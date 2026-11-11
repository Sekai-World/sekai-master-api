package lookups

import (
	"cmp"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
)

const costume2dsEntity = "costume2ds"

// Costume2DsList godoc
// @Summary List 2D costumes by page
// @Description Lists the 2D costumes in ID order. A costume names its Live2D model, its Spine (chibi) model, both, or neither; several costumes can share one Spine model.
// @Tags costume2ds
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param has_spine query bool false "Keep only costumes with a Spine model"
// @Success 200 {object} shared.Costume2DListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /costume2ds/{region}/list [get]
func (handler *LookupHandler) Costume2DsList(c *gin.Context) {
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
	onlyWithSpine := false
	if rawHasSpine := strings.TrimSpace(c.Query("has_spine")); rawHasSpine != "" {
		parsed, err := strconv.ParseBool(rawHasSpine)
		if err != nil {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "has_spine must be a boolean")
			return
		}
		onlyWithSpine = parsed
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, costume2dsEntity) {
		return
	}

	projection, err := handler.masterDataSync.LoadProjection(c.Request.Context(), region, costume2dsEntity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "COSTUME_2D_QUERY_ERROR", "failed to list 2D costumes")
		return
	}

	costumes := make([]shared.Costume2DResponse, 0, projection.Len())
	for _, source := range projectionSourcesByType(projection, "", nil) {
		costume, ok := projectCostume2D(source)
		if !ok || (onlyWithSpine && costume.SpineAssetbundleName == nil) {
			continue
		}
		costumes = append(costumes, costume)
	}
	slices.SortFunc(costumes, func(left, right shared.Costume2DResponse) int {
		return cmp.Compare(left.ID, right.ID)
	})

	items := pageSlice(costumes, page, pageSize)
	if items == nil {
		items = []shared.Costume2DResponse{}
	}
	response.JSON(c, http.StatusOK, shared.Costume2DListResponse{
		Items:      items,
		Pagination: lookupPaginationResponse(page, pageSize, len(costumes)),
	})
}

// projectCostume2D returns the 2D costume a record holds, if it has an ID.
func projectCostume2D(source fieldSource) (shared.Costume2DResponse, bool) {
	id, ok := sourceID(source)
	if !ok {
		return shared.Costume2DResponse{}, false
	}
	return shared.Costume2DResponse{
		ID:                    id,
		Character2DID:         sourceOptionalInt64(source, "character2dId"),
		Costume2DGroupID:      sourceOptionalInt64(source, "costume2dGroupId"),
		Live2DAssetbundleName: sourceOptionalString(source, "live2dAssetbundleName"),
		SpineAssetbundleName:  sourceOptionalString(source, "spineAssetbundleName"),
		FromMmddhh:            sourceString(source, "fromMmddhh"),
		ToMmddhh:              sourceString(source, "toMmddhh"),
	}, true
}
