package lookups

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
)

const boostsEntity = "boosts"

// BoostsList godoc
// @Summary List live bonus boosts by page
// @Description Lists the boosts in costBoost order. A boost is the number of live bonus one live consumes (costBoost) and the rates it multiplies the live's rewards by; eventPointRate multiplies event points. TW, KR and CN rows carry no id, isEventOnly or livePointRate.
// @Tags boosts
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Success 200 {object} shared.BoostListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /boosts/{region}/list [get]
func (handler *LookupHandler) BoostsList(c *gin.Context) {
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, boostsEntity) {
		return
	}

	projection, err := handler.masterDataSync.LoadProjection(c.Request.Context(), region, boostsEntity)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "BOOST_QUERY_ERROR", "failed to list boosts")
		return
	}

	boosts := make([]shared.BoostResponse, 0, projection.Len())
	for row := range projection.Len() {
		boost, ok := projectBoost(projectionRowSource{projection: projection, row: row})
		if !ok {
			continue
		}
		boosts = append(boosts, boost)
	}
	slices.SortStableFunc(boosts, func(left, right shared.BoostResponse) int {
		return cmp.Compare(left.CostBoost, right.CostBoost)
	})

	items := pageSlice(boosts, page, pageSize)
	if items == nil {
		items = []shared.BoostResponse{}
	}
	response.JSON(c, http.StatusOK, shared.BoostListResponse{
		Items:      items,
		Pagination: lookupPaginationResponse(page, pageSize, len(boosts)),
	})
}

// projectBoost returns the boost a record holds, if it has a non-negative
// costBoost and an eventPointRate. Boosts are not keyed by id: TW, KR and CN
// rows have none.
func projectBoost(source fieldSource) (shared.BoostResponse, bool) {
	costBoost, ok := sourceInt64(source, "costBoost")
	if !ok || costBoost < 0 {
		return shared.BoostResponse{}, false
	}
	eventPointRate, ok := sourceInt64(source, "eventPointRate")
	if !ok {
		return shared.BoostResponse{}, false
	}
	return shared.BoostResponse{
		ID:             sourceOptionalInt64(source, "id"),
		CostBoost:      costBoost,
		EventPointRate: eventPointRate,
		IsEventOnly:    sourceOptionalBool(source, "isEventOnly"),
		ExpRate:        sourceOptionalInt64(source, "expRate"),
		RewardRate:     sourceOptionalInt64(source, "rewardRate"),
		LivePointRate:  sourceOptionalInt64(source, "livePointRate"),
		BondsExpRate:   sourceOptionalInt64(source, "bondsExpRate"),
	}, true
}
