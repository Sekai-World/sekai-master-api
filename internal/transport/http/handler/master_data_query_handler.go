package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type MasterDataQueryHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
}

func NewMasterDataQueryHandler(masterDataSync *usecase.MasterDataSyncUsecase) *MasterDataQueryHandler {
	return &MasterDataQueryHandler{masterDataSync: masterDataSync}
}

func (handler *MasterDataQueryHandler) ByID(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	entity := strings.TrimSpace(c.Param("entity"))
	id := strings.TrimSpace(c.Param("id"))
	if region == "" || entity == "" || id == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region, entity and id are required")
		return
	}

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, entity, id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MASTER_DATA_QUERY_ERROR", "failed to query master data")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "MASTER_DATA_NOT_FOUND", "master data not found")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{"item": record})
}

func (handler *MasterDataQueryHandler) SearchByName(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	entity := strings.TrimSpace(c.Param("entity"))
	query := strings.TrimSpace(c.Query("q"))
	if region == "" || entity == "" || query == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region, entity and q are required")
		return
	}

	limit := 20
	if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "limit must be a positive integer")
			return
		}
		limit = parsedLimit
	}

	fields := parseFields(c.Query("field"))

	items, err := handler.masterDataSync.Search(c.Request.Context(), region, entity, query, fields, limit)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MASTER_DATA_QUERY_ERROR", "failed to search master data")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{"items": items})
}

func parseFields(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	parts := strings.Split(trimmed, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		field := strings.TrimSpace(part)
		if field == "" {
			continue
		}
		result = append(result, field)
	}

	return result
}
