package lookups

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
)

const mysekaiPhotoDecorationsEntity = "mysekaiphotodecorations"

// MysekaiPhotoDecorationsByID godoc
// @Summary Get a MySekai photo decoration by id
// @Tags mysekaiPhotoDecorations
// @Produce json
// @Param region path string true "Region"
// @Param id path int true "Photo decoration ID" minimum(1)
// @Success 200 {object} shared.MysekaiPhotoDecorationResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /mysekaiPhotoDecorations/{region}/{id} [get]
func (handler *LookupHandler) MysekaiPhotoDecorationsByID(c *gin.Context) {
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, mysekaiPhotoDecorationsEntity) {
		return
	}

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, mysekaiPhotoDecorationsEntity, id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MYSEKAI_PHOTO_DECORATION_QUERY_ERROR", "failed to query MySekai photo decoration")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "MYSEKAI_PHOTO_DECORATION_NOT_FOUND", "MySekai photo decoration not found")
		return
	}

	response.JSON(c, http.StatusOK, projectMysekaiPhotoDecoration(record))
}

// MysekaiPhotoDecorationsList godoc
// @Summary List MySekai photo decorations by page
// @Tags mysekaiPhotoDecorations
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Success 200 {object} shared.MysekaiPhotoDecorationListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /mysekaiPhotoDecorations/{region}/list [get]
func (handler *LookupHandler) MysekaiPhotoDecorationsList(c *gin.Context) {
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, mysekaiPhotoDecorationsEntity) {
		return
	}

	records, total, err := handler.masterDataSync.ListByPage(c.Request.Context(), region, mysekaiPhotoDecorationsEntity, page, pageSize)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MYSEKAI_PHOTO_DECORATION_QUERY_ERROR", "failed to list MySekai photo decorations")
		return
	}

	items := make([]shared.MysekaiPhotoDecorationResponse, 0, len(records))
	for _, record := range records {
		items = append(items, projectMysekaiPhotoDecoration(record))
	}

	response.JSON(c, http.StatusOK, shared.MysekaiPhotoDecorationListResponse{
		Items:      items,
		Pagination: lookupPaginationResponse(page, pageSize, total),
	})
}

func projectMysekaiPhotoDecoration(record map[string]any) shared.MysekaiPhotoDecorationResponse {
	return shared.MysekaiPhotoDecorationResponse{
		ID:              lookupRequiredInt64(record["id"]),
		Seq:             lookupRequiredInt64(record["seq"]),
		Name:            lookupString(record["name"]),
		Description:     lookupString(record["description"]),
		AssetbundleName: lookupString(record["assetbundleName"]),
	}
}
