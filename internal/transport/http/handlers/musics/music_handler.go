package musics

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type MusicHandler struct {
	masterDataSync *usecase.MasterDataSyncUsecase
}

var defaultSortableMusicFields = []string{
	"id",
	"seq",
	"title",
	"pronunciation",
	"lyricist",
	"composer",
	"arranger",
	"assetbundleName",
	"publishedAt",
	"fillerSec",
	"dancerCount",
	"selfDancerPosition",
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
// @Success 200 {object} shared.MusicObjectResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
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
// @Success 200 {object} shared.RegionAvailabilityResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
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

	regions, err := shared.AvailableRegionsByID(c.Request.Context(), handler.masterDataSync, "musics", id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to query music available regions")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{"regions": regions})
}

// List godoc
// @Summary List musics by page
// @Tags musics
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number"
// @Param page_size query int false "Page size"
// @Param spoiler query bool false "Include spoiler content"
// @Param name query string false "Fuzzy music name"
// @Param category query string false "Comma-separated music categories"
// @Param composer query string false "Comma-separated composer names"
// @Param arranger query string false "Comma-separated arranger names"
// @Param lyricist query string false "Comma-separated lyricist names"
// @Param playLevel query string false "Music difficulty playLevel. Supports 30, >30, >=30, <30, <=30, or 26-30"
// @Param sort_by query string false "Sort field"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.MusicListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
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

	sortOptions, ok := shared.ParseListSortOptions(c)
	if !ok {
		return
	}

	includeSpoilers, ok := shared.ParseSpoilerOption(c)
	if !ok {
		return
	}

	filterOptions, ok := parseMusicListFilterOptions(c)
	if !ok {
		return
	}

	if !includeSpoilers || sortOptions.Enabled || filterOptions.Enabled() {
		records, err := handler.masterDataSync.ListAll(c.Request.Context(), region, "musics")
		if err != nil {
			response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to list musics")
			return
		}
		if !includeSpoilers {
			records = shared.FilterSpoilerItems(records, time.Now().UTC())
		}
		if filterOptions.Enabled() {
			records, err = handler.filterMusicRecords(c.Request.Context(), region, records, filterOptions)
			if err != nil {
				response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to filter musics")
				return
			}
		}
		if sortOptions.Enabled {
			if !shared.ValidateSortField(c, sortOptions.Field, records, defaultSortableMusicFields) {
				return
			}
			shared.SortResponseItems(records, sortOptions.Field, sortOptions.Descending)
		}
		pagedRecords, pagination := shared.PaginateItems(records, page, pageSize)
		items := handler.buildMusicList(c.Request.Context(), region, pagedRecords)
		response.JSON(c, http.StatusOK, gin.H{
			"items":      items,
			"pagination": pagination,
		})
		return
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

type musicListFilterOptions struct {
	Name      string
	Category  map[string]struct{}
	Composer  map[string]struct{}
	Arranger  map[string]struct{}
	Lyricist  map[string]struct{}
	PlayLevel musicPlayLevelFilter
}

func (options musicListFilterOptions) Enabled() bool {
	return options.Name != "" ||
		len(options.Category) > 0 ||
		len(options.Composer) > 0 ||
		len(options.Arranger) > 0 ||
		len(options.Lyricist) > 0 ||
		options.PlayLevel.Enabled()
}

func parseMusicListFilterOptions(c *gin.Context) (musicListFilterOptions, bool) {
	playLevelFilter, ok := parseMusicPlayLevelFilter(c)
	if !ok {
		return musicListFilterOptions{}, false
	}

	return musicListFilterOptions{
		Name:      shared.NormalizeComparableText(c.Query("name")),
		Category:  parseMusicListQuerySet(c, "category"),
		Composer:  parseMusicListQuerySet(c, "composer"),
		Arranger:  parseMusicListQuerySet(c, "arranger"),
		Lyricist:  parseMusicListQuerySet(c, "lyricist"),
		PlayLevel: playLevelFilter,
	}, true
}

func parseMusicListQuerySet(c *gin.Context, key string) map[string]struct{} {
	values := c.QueryArray(key)
	if len(values) == 0 {
		return nil
	}

	result := map[string]struct{}{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			normalized := shared.NormalizeComparableText(part)
			if normalized == "" {
				continue
			}
			result[normalized] = struct{}{}
		}
	}
	if len(result) == 0 {
		return nil
	}

	return result
}

type musicPlayLevelFilter struct {
	Equals       []float64
	Min          float64
	Max          float64
	HasMin       bool
	HasMax       bool
	MinInclusive bool
	MaxInclusive bool
}

func (filter musicPlayLevelFilter) Enabled() bool {
	return len(filter.Equals) > 0 || filter.HasMin || filter.HasMax
}

func parseMusicPlayLevelFilter(c *gin.Context) (musicPlayLevelFilter, bool) {
	filter := musicPlayLevelFilter{}

	for _, rawValue := range c.QueryArray("playLevel") {
		for _, part := range strings.Split(rawValue, ",") {
			if !parseMusicPlayLevelExpression(c, strings.TrimSpace(part), &filter) {
				return musicPlayLevelFilter{}, false
			}
		}
	}

	return filter, true
}

func parseMusicPlayLevelExpression(c *gin.Context, rawValue string, filter *musicPlayLevelFilter) bool {
	if rawValue == "" {
		return true
	}

	if strings.HasPrefix(rawValue, ">=") {
		return parseMusicPlayLevelComparison(c, strings.TrimSpace(strings.TrimPrefix(rawValue, ">=")), true, true, filter)
	}
	if strings.HasPrefix(rawValue, ">") {
		return parseMusicPlayLevelComparison(c, strings.TrimSpace(strings.TrimPrefix(rawValue, ">")), false, true, filter)
	}
	if strings.HasPrefix(rawValue, "<=") {
		return parseMusicPlayLevelComparison(c, strings.TrimSpace(strings.TrimPrefix(rawValue, "<=")), true, false, filter)
	}
	if strings.HasPrefix(rawValue, "<") {
		return parseMusicPlayLevelComparison(c, strings.TrimSpace(strings.TrimPrefix(rawValue, "<")), false, false, filter)
	}
	if strings.Contains(rawValue, "-") {
		return parseMusicPlayLevelRange(c, rawValue, filter)
	}

	value, ok := parseMusicPlayLevelNumber(c, rawValue, "playLevel")
	if !ok {
		return false
	}
	filter.Equals = append(filter.Equals, value)
	return true
}

func parseMusicPlayLevelRange(c *gin.Context, rawValue string, filter *musicPlayLevelFilter) bool {
	parts := strings.Split(rawValue, "-")
	if len(parts) != 2 {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "playLevel range must contain two values")
		return false
	}

	minValue, ok := parseMusicPlayLevelNumber(c, parts[0], "playLevel")
	if !ok {
		return false
	}
	maxValue, ok := parseMusicPlayLevelNumber(c, parts[1], "playLevel")
	if !ok {
		return false
	}
	if minValue > maxValue {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "playLevel range minimum must be less than or equal to maximum")
		return false
	}

	filter.Min = minValue
	filter.Max = maxValue
	filter.HasMin = true
	filter.HasMax = true
	filter.MinInclusive = true
	filter.MaxInclusive = true
	return true
}

func parseMusicPlayLevelComparison(c *gin.Context, rawValue string, inclusive bool, isMin bool, filter *musicPlayLevelFilter) bool {
	value, ok := parseMusicPlayLevelNumber(c, rawValue, "playLevel")
	if !ok {
		return false
	}

	if isMin {
		filter.Min = value
		filter.HasMin = true
		filter.MinInclusive = inclusive
		return true
	}

	filter.Max = value
	filter.HasMax = true
	filter.MaxInclusive = inclusive
	return true
}

func parseMusicPlayLevelNumber(c *gin.Context, rawValue string, key string) (float64, bool) {
	parsedValue, err := strconv.ParseFloat(strings.TrimSpace(rawValue), 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", key+" must be a number")
		return 0, false
	}

	return parsedValue, true
}

func (handler *MusicHandler) filterMusicRecords(ctx context.Context, region string, records []map[string]any, options musicListFilterOptions) ([]map[string]any, error) {
	if len(records) == 0 || !options.Enabled() {
		return records, nil
	}

	var playLevels map[string][]float64
	if options.PlayLevel.Enabled() {
		var err error
		playLevels, err = handler.loadMusicPlayLevels(ctx, region)
		if err != nil {
			return nil, err
		}
	}

	filtered := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if !musicMatchesFilterOptions(record, options, playLevels) {
			continue
		}
		filtered = append(filtered, record)
	}

	return filtered, nil
}

func (handler *MusicHandler) loadMusicPlayLevels(ctx context.Context, region string) (map[string][]float64, error) {
	difficulties, err := handler.masterDataSync.ListAll(ctx, region, "musicdifficulties")
	if err != nil {
		return nil, err
	}

	playLevels := make(map[string][]float64, len(difficulties))
	for _, difficulty := range difficulties {
		musicID := shared.NormalizeAnyID(difficulty["musicId"])
		if musicID == "" {
			continue
		}
		playLevel, ok := musicNumericValue(difficulty["playLevel"])
		if !ok {
			continue
		}
		playLevels[musicID] = append(playLevels[musicID], playLevel)
	}

	return playLevels, nil
}

func musicMatchesFilterOptions(record map[string]any, options musicListFilterOptions, playLevels map[string][]float64) bool {
	if options.Name != "" &&
		!musicValueContains(record["title"], options.Name) &&
		!musicValueContains(record["pronunciation"], options.Name) {
		return false
	}

	if len(options.Category) > 0 &&
		!musicValueContainsAny(record["category"], options.Category) &&
		!musicValueContainsAny(record["categories"], options.Category) &&
		!musicValueContainsAny(record["musicCategory"], options.Category) {
		return false
	}

	if len(options.Composer) > 0 && !musicValueContainsAny(record["composer"], options.Composer) {
		return false
	}
	if len(options.Arranger) > 0 && !musicValueContainsAny(record["arranger"], options.Arranger) {
		return false
	}
	if len(options.Lyricist) > 0 && !musicValueContainsAny(record["lyricist"], options.Lyricist) {
		return false
	}
	if options.PlayLevel.Enabled() && !musicMatchesPlayLevelFilter(playLevels[shared.NormalizeAnyID(record["id"])], options.PlayLevel) {
		return false
	}

	return true
}

func musicMatchesPlayLevelFilter(playLevels []float64, filter musicPlayLevelFilter) bool {
	for _, playLevel := range playLevels {
		if musicPlayLevelSatisfiesFilter(playLevel, filter) {
			return true
		}
	}

	return false
}

func musicPlayLevelSatisfiesFilter(playLevel float64, filter musicPlayLevelFilter) bool {
	if len(filter.Equals) > 0 {
		for _, expected := range filter.Equals {
			if playLevel == expected {
				return true
			}
		}
		return false
	}

	if filter.HasMin {
		if filter.MinInclusive && playLevel < filter.Min {
			return false
		}
		if !filter.MinInclusive && playLevel <= filter.Min {
			return false
		}
	}

	if filter.HasMax {
		if filter.MaxInclusive && playLevel > filter.Max {
			return false
		}
		if !filter.MaxInclusive && playLevel >= filter.Max {
			return false
		}
	}

	return true
}

func musicNumericValue(value any) (float64, bool) {
	if value == nil {
		return 0, false
	}

	parsed, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprintf("%v", value)), 64)
	if err != nil {
		return 0, false
	}

	return parsed, true
}

func musicValueContainsAny(value any, queries map[string]struct{}) bool {
	for query := range queries {
		if musicValueContains(value, query) {
			return true
		}
	}

	return false
}

func musicValueContains(value any, query string) bool {
	if query == "" || value == nil {
		return false
	}

	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if musicValueContains(item, query) {
				return true
			}
		}
		return false
	case []string:
		for _, item := range typed {
			if musicValueContains(item, query) {
				return true
			}
		}
		return false
	case map[string]any:
		for _, item := range typed {
			if musicValueContains(item, query) {
				return true
			}
		}
		return false
	default:
		return strings.Contains(shared.NormalizeComparableText(value), query)
	}
}

func (handler *MusicHandler) ensureRegionReady(c *gin.Context, region string) bool {
	if handler == nil || handler.masterDataSync == nil {
		return true
	}

	readyRegions, err := shared.ReadyMasterDataRegions(c.Request.Context(), handler.masterDataSync)
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
	result := shared.BuildRecordWithReleaseCondition(ctx, handler.masterDataSync, region, record)

	if handler == nil || handler.masterDataSync == nil {
		return result
	}

	if rawCreatorArtistID, hasCreatorArtistID := record["creatorArtistId"]; hasCreatorArtistID {
		delete(result, "creatorArtistId")

		creatorArtistLookupID := shared.NormalizeAnyID(rawCreatorArtistID)
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

		liveStageLookupID := shared.NormalizeAnyID(rawLiveStageID)
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
