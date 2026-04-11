package handler

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type MusicHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
}

func NewMusicHandler(masterDataSync *usecase.MasterDataSyncUsecase) *MusicHandler {
	return &MusicHandler{masterDataSync: masterDataSync}
}

// ByID godoc
// @Summary Get music by id
// @Tags musics
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Music ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /musics/{region}/{id} [get]
func (handler *MusicHandler) ByID(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	id := strings.TrimSpace(c.Param("id"))
	if region == "" || id == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region and id are required")
		return
	}
	if !handler.ensureRegionReady(c, region) {
		return
	}

	record, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "musics", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to query music")
		return
	}
	if !found {
		response.Error(c, http.StatusNotFound, "MUSIC_NOT_FOUND", "music not found")
		return
	}

	response.JSON(c, http.StatusOK, handler.buildMusic(c.Request.Context(), region, record))
}

// AvailableRegionsByID godoc
// @Summary Get available regions for a music id
// @Tags musics
// @Produce json
// @Param id path string true "Music ID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /musics/regions/{id}/availability [get]
func (handler *MusicHandler) AvailableRegionsByID(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "id is required")
		return
	}

	regions, err := availableRegionsByID(c.Request.Context(), handler.masterDataSync, "musics", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to query music available regions")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{"regions": regions})
}

// Search godoc
// @Summary Search musics
// @Tags musics
// @Produce json
// @Param region path string true "Region"
// @Param title query string false "Keyword for title field"
// @Param lyricist query string false "Keyword for lyricist field"
// @Param composer query string false "Keyword for composer field"
// @Param arranger query string false "Keyword for arranger field"
// @Param page query int false "Page number"
// @Param limit query int false "Max results"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /musics/{region}/search [get]
func (handler *MusicHandler) Search(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	if region == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region is required")
		return
	}
	if !handler.ensureRegionReady(c, region) {
		return
	}

	fieldKeywords := extractMusicFieldKeywords(c)
	searchFields := musicSearchFieldsFromKeywords(fieldKeywords)
	if len(searchFields) == 0 {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "at least one of title, lyricist, composer, arranger is required")
		return
	}

	page := 1
	if rawPage := strings.TrimSpace(c.Query("page")); rawPage != "" {
		parsedPage, err := strconv.Atoi(rawPage)
		if err != nil || parsedPage <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "page must be a positive integer")
			return
		}
		page = parsedPage
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

	fetchLimit := 1000000
	matches, err := handler.searchMusicsWithFieldKeywords(c.Request.Context(), region, searchFields, fieldKeywords, fetchLimit)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to search musics")
		return
	}

	total := len(matches)
	start := (page - 1) * limit
	if start >= total {
		totalPages := 0
		if limit > 0 {
			totalPages = (total + limit - 1) / limit
		}
		response.JSON(c, http.StatusOK, gin.H{
			"items": []map[string]any{},
			"pagination": gin.H{
				"page":        page,
				"page_size":   limit,
				"total":       total,
				"total_pages": totalPages,
				"has_next":    false,
			},
		})
		return
	}

	end := start + limit
	if end > total {
		end = total
	}
	pagedMatches := matches[start:end]

	items := make([]map[string]any, 0, len(pagedMatches))
	for _, match := range pagedMatches {
		items = append(items, handler.buildMusic(c.Request.Context(), region, match.Item))
	}

	totalPages := 0
	if limit > 0 {
		totalPages = (total + limit - 1) / limit
	}
	hasNext := page < totalPages

	response.JSON(c, http.StatusOK, gin.H{
		"items": items,
		"pagination": gin.H{
			"page":        page,
			"page_size":   limit,
			"total":       total,
			"total_pages": totalPages,
			"has_next":    hasNext,
		},
	})
}

// List godoc
// @Summary List musics by page
// @Tags musics
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number"
// @Param page_size query int false "Page size"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /musics/{region}/list [get]
func (handler *MusicHandler) List(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region := strings.TrimSpace(c.Param("region"))
	if region == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "region is required")
		return
	}
	if !handler.ensureRegionReady(c, region) {
		return
	}

	page := 1
	if rawPage := strings.TrimSpace(c.Query("page")); rawPage != "" {
		parsedPage, err := strconv.Atoi(rawPage)
		if err != nil || parsedPage <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "page must be a positive integer")
			return
		}
		page = parsedPage
	}

	pageSize := 20
	if rawPageSize := strings.TrimSpace(c.Query("page_size")); rawPageSize != "" {
		parsedPageSize, err := strconv.Atoi(rawPageSize)
		if err != nil || parsedPageSize <= 0 {
			response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "page_size must be a positive integer")
			return
		}
		pageSize = parsedPageSize
	}

	records, total, err := handler.masterDataSync.ListByPage(c.Request.Context(), region, "musics", page, pageSize)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to list musics")
		return
	}

	totalPages := 0
	if pageSize > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	hasNext := page < totalPages

	response.JSON(c, http.StatusOK, gin.H{
		"items": handler.buildMusicList(c.Request.Context(), region, records),
		"pagination": gin.H{
			"page":        page,
			"page_size":   pageSize,
			"total":       total,
			"total_pages": totalPages,
			"has_next":    hasNext,
		},
	})
}

func (handler *MusicHandler) ensureRegionReady(c *gin.Context, region string) bool {
	if handler == nil || handler.masterDataSync == nil {
		return true
	}

	readyRegions, err := readyMasterDataRegions(c.Request.Context(), handler.masterDataSync)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MASTER_DATA_STATUS_ERROR", "failed to check master data sync status")
		return false
	}

	normalizedRegion := strings.ToLower(strings.TrimSpace(region))
	for _, readyRegion := range readyRegions {
		if readyRegion == normalizedRegion {
			return true
		}
	}

	response.Error(c, http.StatusServiceUnavailable, "REGION_DATA_NOT_READY", "region data is updating or unavailable, please try again later")
	return false
}

func (handler *MusicHandler) buildMusicList(ctx context.Context, region string, records []map[string]any) []map[string]any {
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, handler.buildMusic(ctx, region, record))
	}

	return items
}

func (handler *MusicHandler) buildMusic(ctx context.Context, region string, record map[string]any) map[string]any {
	if record == nil {
		return map[string]any{}
	}

	result := make(map[string]any, len(record)+2)
	for key, value := range record {
		result[key] = value
	}

	if handler == nil || handler.masterDataSync == nil {
		return result
	}

	if rawCreatorArtistID, hasCreatorArtistID := record["creatorArtistId"]; hasCreatorArtistID {
		delete(result, "creatorArtistId")

		creatorArtistLookupID := normalizeAnyID(rawCreatorArtistID)
		if creatorArtistLookupID == "" {
			result["creatorArtist"] = nil
		} else {
			creatorArtist, found, err := handler.masterDataSync.GetByID(ctx, region, "musicartists", creatorArtistLookupID)
			if err != nil || !found {
				result["creatorArtist"] = nil
			} else {
				result["creatorArtist"] = creatorArtist
			}
		}
	}

	if rawLiveStageID, hasLiveStageID := record["liveStageId"]; hasLiveStageID {
		delete(result, "liveStageId")

		liveStageLookupID := normalizeAnyID(rawLiveStageID)
		if liveStageLookupID == "" {
			result["liveStage"] = nil
		} else {
			liveStage, found, err := handler.masterDataSync.GetByID(ctx, region, "livestages", liveStageLookupID)
			if err != nil || !found {
				result["liveStage"] = nil
			} else {
				result["liveStage"] = liveStage
			}
		}
	}

	return result
}

type musicSearchResult struct {
	Item  map[string]any
	Score int
}

func extractMusicFieldKeywords(c *gin.Context) map[string]string {
	fieldKeywords := make(map[string]string, 4)
	for _, field := range []string{"title", "lyricist", "composer", "arranger"} {
		value := strings.TrimSpace(c.Query(field))
		if value == "" {
			continue
		}
		fieldKeywords[field] = value
	}

	return fieldKeywords
}

func musicSearchFieldsFromKeywords(fieldKeywords map[string]string) []string {
	fields := make([]string, 0, len(fieldKeywords))
	for _, field := range []string{"title", "lyricist", "composer", "arranger"} {
		if _, exists := fieldKeywords[field]; !exists {
			continue
		}
		fields = append(fields, field)
	}

	return fields
}

func (handler *MusicHandler) searchMusicsWithFieldKeywords(
	ctx context.Context,
	region string,
	fields []string,
	fieldKeywords map[string]string,
	fetchLimit int,
) ([]musicSearchResult, error) {
	type aggregate struct {
		item  map[string]any
		score int
	}

	candidates := make(map[string]aggregate)
	for index, field := range fields {
		query := fieldKeywords[field]

		matches, err := handler.masterDataSync.Search(ctx, region, "musics", query, []string{field}, fetchLimit)
		if err != nil {
			return nil, err
		}

		currentFieldResults := make(map[string]aggregate, len(matches))
		for _, match := range matches {
			id := normalizeAnyID(match.Item["id"])
			if id == "" {
				continue
			}
			currentFieldResults[id] = aggregate{
				item:  match.Item,
				score: match.MatchScore,
			}
		}

		if index == 0 {
			for id, value := range currentFieldResults {
				candidates[id] = value
			}
			continue
		}

		for id, candidate := range candidates {
			current, exists := currentFieldResults[id]
			if !exists {
				delete(candidates, id)
				continue
			}
			candidate.score += current.score
			candidates[id] = candidate
		}
	}

	results := make([]musicSearchResult, 0, len(candidates))
	for _, candidate := range candidates {
		results = append(results, musicSearchResult{
			Item:  candidate.item,
			Score: candidate.score,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return normalizeAnyID(results[i].Item["id"]) < normalizeAnyID(results[j].Item["id"])
		}
		return results[i].Score > results[j].Score
	})

	return results, nil
}
