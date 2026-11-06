package lookups

import (
	"cmp"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
)

// Comics are the tips that carry an image; the other tips are text only.
const comicsEntity = "tips"

// ComicsByID godoc
// @Summary Get a comic by id
// @Description Returns one four-panel comic. A tip without an image is not a comic and returns 404.
// @Tags comics
// @Produce json
// @Param region path string true "Region"
// @Param id path int true "Comic ID" minimum(1)
// @Success 200 {object} shared.ComicResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /comics/{region}/{id} [get]
func (handler *LookupHandler) ComicsByID(c *gin.Context) {
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, comicsEntity) {
		return
	}

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, comicsEntity, id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "COMIC_QUERY_ERROR", "failed to query comic")
		return
	}
	comic, isComic := projectComic(recordSource(record))
	if !found || !isComic {
		response.Error(c, http.StatusNotFound, "COMIC_NOT_FOUND", "comic not found")
		return
	}

	response.JSON(c, http.StatusOK, comic)
}

// ComicsList godoc
// @Summary List comics by page
// @Description Lists the four-panel comics in ID order. Tips without an image are left out.
// @Tags comics
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Success 200 {object} shared.ComicListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /comics/{region}/list [get]
func (handler *LookupHandler) ComicsList(c *gin.Context) {
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, comicsEntity) {
		return
	}

	projection, err := handler.masterDataSync.LoadProjection(c.Request.Context(), region, comicsEntity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "COMIC_QUERY_ERROR", "failed to list comics")
		return
	}

	comics := make([]shared.ComicResponse, 0, projection.Len())
	for _, source := range projectionSourcesByType(projection, "", nil) {
		if comic, isComic := projectComic(source); isComic {
			comics = append(comics, comic)
		}
	}
	slices.SortFunc(comics, func(left, right shared.ComicResponse) int {
		return cmp.Compare(left.ID, right.ID)
	})

	items := pageSlice(comics, page, pageSize)
	if items == nil {
		items = []shared.ComicResponse{}
	}
	response.JSON(c, http.StatusOK, shared.ComicListResponse{
		Items:      items,
		Pagination: lookupPaginationResponse(page, pageSize, len(comics)),
	})
}

// projectComic returns the comic a tip holds, if it has an ID and an image.
func projectComic(source fieldSource) (shared.ComicResponse, bool) {
	id, ok := sourceID(source)
	assetbundleName := sourceString(source, "assetbundleName")
	if !ok || strings.TrimSpace(assetbundleName) == "" {
		return shared.ComicResponse{}, false
	}
	return shared.ComicResponse{
		ID:              id,
		Title:           sourceString(source, "title"),
		AssetbundleName: assetbundleName,
		FromUserRank:    sourceOptionalInt64(source, "fromUserRank"),
		ToUserRank:      sourceOptionalInt64(source, "toUserRank"),
	}, true
}
