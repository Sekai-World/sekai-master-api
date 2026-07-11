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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, "musics") {
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

	item, err := handler.buildMusic(c.Request.Context(), region, record)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to enrich music")
		return
	}
	response.JSON(c, http.StatusOK, item)
}

// DifficultiesByID godoc
// @Summary Get music difficulties by music id
// @Tags musics
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Music ID"
// @Success 200 {object} shared.MusicDifficultiesResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /musics/{region}/{id}/difficulties [get]
func (handler *MusicHandler) DifficultiesByID(c *gin.Context) {
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, "musics") {
		return
	}

	if _, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "musics", id); err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to query music")
		return
	} else if !found {
		response.Error(c, http.StatusNotFound, "MUSIC_NOT_FOUND", "music not found")
		return
	}

	difficulties, err := handler.loadMusicDifficultiesByMusicID(c.Request.Context(), region, id, false)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to query music difficulties")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{"items": difficulties})
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
// @Param tag query string false "Comma-separated music tags"
// @Param playLevel query string false "Music difficulty playLevel. Supports 30, >30, >=30, <30, <=30, or 26-30. Aliases: play_level, level"
// @Param hasAppend query bool false "Filter musics by whether they have append difficulty"
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, "musics") {
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
		items, err := handler.buildMusicList(c.Request.Context(), region, pagedRecords)
		if err != nil {
			response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to enrich musics")
			return
		}
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

	items, err := handler.buildMusicList(c.Request.Context(), region, records)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to enrich musics")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{
		"items": items,
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
	Tags      map[string]struct{}
	PlayLevel musicPlayLevelFilter
	HasAppend bool
	UseAppend bool
}

func (options musicListFilterOptions) Enabled() bool {
	return options.Name != "" ||
		len(options.Category) > 0 ||
		len(options.Composer) > 0 ||
		len(options.Arranger) > 0 ||
		len(options.Lyricist) > 0 ||
		len(options.Tags) > 0 ||
		options.PlayLevel.Enabled() ||
		options.UseAppend
}

func parseMusicListFilterOptions(c *gin.Context) (musicListFilterOptions, bool) {
	playLevelFilter, ok := parseMusicPlayLevelFilter(c)
	if !ok {
		return musicListFilterOptions{}, false
	}
	hasAppend, useAppend, ok := parseMusicListBool(c, "hasAppend")
	if !ok {
		return musicListFilterOptions{}, false
	}

	return musicListFilterOptions{
		Name:      shared.NormalizeComparableText(c.Query("name")),
		Category:  parseMusicListQuerySet(c, "category"),
		Composer:  parseMusicListQuerySet(c, "composer"),
		Arranger:  parseMusicListQuerySet(c, "arranger"),
		Lyricist:  parseMusicListQuerySet(c, "lyricist"),
		Tags:      parseMusicListQuerySet(c, "tag"),
		PlayLevel: playLevelFilter,
		HasAppend: hasAppend,
		UseAppend: useAppend,
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

func parseMusicListBool(c *gin.Context, key string) (bool, bool, bool) {
	rawValue := strings.TrimSpace(c.Query(key))
	if rawValue == "" {
		return false, false, true
	}

	parsed, err := strconv.ParseBool(rawValue)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", key+" must be a boolean")
		return false, false, false
	}

	return parsed, true, true
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

	for _, key := range []string{"playLevel", "play_level", "level"} {
		for _, rawValue := range c.QueryArray(key) {
			for _, part := range strings.Split(rawValue, ",") {
				if !parseMusicPlayLevelExpression(c, strings.TrimSpace(part), &filter) {
					return musicPlayLevelFilter{}, false
				}
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

	var tags map[string]map[string]struct{}
	if len(options.Tags) > 0 {
		var err error
		tags, err = handler.loadMusicTags(ctx, region)
		if err != nil {
			return nil, err
		}
	}

	var appendMusicIDs map[string]struct{}
	if options.UseAppend {
		var err error
		appendMusicIDs, err = handler.loadAppendMusicIDs(ctx, region)
		if err != nil {
			return nil, err
		}
	}

	filtered := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if !musicMatchesFilterOptions(record, options, playLevels, tags, appendMusicIDs) {
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

func (handler *MusicHandler) loadMusicTags(ctx context.Context, region string) (map[string]map[string]struct{}, error) {
	tagRecords, err := handler.masterDataSync.ListAll(ctx, region, "musictags")
	if err != nil {
		return nil, err
	}

	tags := make(map[string]map[string]struct{}, len(tagRecords))
	for _, tagRecord := range tagRecords {
		musicID := shared.NormalizeAnyID(tagRecord["musicId"])
		musicTag := shared.NormalizeComparableText(tagRecord["musicTag"])
		if musicID == "" || musicTag == "" {
			continue
		}
		if tags[musicID] == nil {
			tags[musicID] = map[string]struct{}{}
		}
		tags[musicID][musicTag] = struct{}{}
	}

	return tags, nil
}

func (handler *MusicHandler) loadAppendMusicIDs(ctx context.Context, region string) (map[string]struct{}, error) {
	difficulties, err := handler.masterDataSync.ListAll(ctx, region, "musicdifficulties")
	if err != nil {
		return nil, err
	}

	appendMusicIDs := map[string]struct{}{}
	for _, difficulty := range difficulties {
		musicID := shared.NormalizeAnyID(difficulty["musicId"])
		difficultyType := shared.NormalizeComparableText(difficulty["musicDifficulty"])
		if musicID == "" || difficultyType != "append" {
			continue
		}
		appendMusicIDs[musicID] = struct{}{}
	}

	return appendMusicIDs, nil
}

func musicMatchesFilterOptions(record map[string]any, options musicListFilterOptions, playLevels map[string][]float64, tags map[string]map[string]struct{}, appendMusicIDs map[string]struct{}) bool {
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
	if len(options.Tags) > 0 && !musicTagsMatchAny(tags[shared.NormalizeAnyID(record["id"])], options.Tags) {
		return false
	}
	if options.UseAppend && musicHasAppend(appendMusicIDs, shared.NormalizeAnyID(record["id"])) != options.HasAppend {
		return false
	}
	if options.PlayLevel.Enabled() && !musicMatchesPlayLevelFilter(playLevels[shared.NormalizeAnyID(record["id"])], options.PlayLevel) {
		return false
	}

	return true
}

func musicTagsMatchAny(tags map[string]struct{}, queries map[string]struct{}) bool {
	for query := range queries {
		if _, ok := tags[query]; ok {
			return true
		}
	}

	return false
}

func musicHasAppend(appendMusicIDs map[string]struct{}, musicID string) bool {
	if musicID == "" {
		return false
	}
	_, ok := appendMusicIDs[musicID]
	return ok
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

func (handler *MusicHandler) buildMusicList(ctx context.Context, region string, records []map[string]any) ([]map[string]any, error) {
	difficulties, err := handler.loadMusicDifficultyRecords(ctx, region)
	if err != nil {
		return nil, err
	}
	tags, err := handler.loadMusicTagRecords(ctx, region)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		musicID := shared.NormalizeAnyID(record["id"])
		item, err := handler.buildMusic(ctx, region, record)
		if err != nil {
			return nil, err
		}
		item["difficulties"] = difficulties[musicID]
		if item["difficulties"] == nil {
			item["difficulties"] = []map[string]any{}
		}
		item["tags"] = tags[musicID]
		if item["tags"] == nil {
			item["tags"] = []string{}
		}
		items = append(items, item)
	}

	return items, nil
}

func (handler *MusicHandler) loadMusicDifficultyRecords(ctx context.Context, region string) (map[string][]map[string]any, error) {
	if handler == nil || handler.masterDataSync == nil {
		return map[string][]map[string]any{}, nil
	}

	difficultyRecords, err := handler.masterDataSync.ListAll(ctx, region, "musicdifficulties")
	if err != nil {
		return nil, fmt.Errorf("list music difficulties: %w", err)
	}

	difficulties := make(map[string][]map[string]any, len(difficultyRecords))
	for _, difficulty := range difficultyRecords {
		musicID := shared.NormalizeAnyID(difficulty["musicId"])
		if musicID == "" {
			continue
		}
		item, err := handler.buildMusicDifficulty(ctx, region, difficulty, true)
		if err != nil {
			return nil, err
		}
		difficulties[musicID] = append(difficulties[musicID], item)
	}

	return difficulties, nil
}

func (handler *MusicHandler) loadMusicTagRecords(ctx context.Context, region string) (map[string][]string, error) {
	if handler == nil || handler.masterDataSync == nil {
		return map[string][]string{}, nil
	}

	tagRecords, err := handler.masterDataSync.ListAll(ctx, region, "musictags")
	if err != nil {
		return nil, fmt.Errorf("list music tags: %w", err)
	}

	tags := make(map[string][]string, len(tagRecords))
	for _, tagRecord := range tagRecords {
		musicID := shared.NormalizeAnyID(tagRecord["musicId"])
		musicTag := strings.TrimSpace(fmt.Sprintf("%v", tagRecord["musicTag"]))
		if musicID == "" || musicTag == "" {
			continue
		}
		tags[musicID] = append(tags[musicID], musicTag)
	}

	return tags, nil
}

func (handler *MusicHandler) loadMusicDifficultiesByMusicID(ctx context.Context, region string, musicID string, compact bool) ([]map[string]any, error) {
	difficultyRecords, err := handler.masterDataSync.ListAll(ctx, region, "musicdifficulties")
	if err != nil {
		return nil, err
	}

	targetMusicID := shared.NormalizeAnyID(musicID)
	difficulties := make([]map[string]any, 0)
	for _, difficulty := range difficultyRecords {
		if shared.NormalizeAnyID(difficulty["musicId"]) != targetMusicID {
			continue
		}
		item, err := handler.buildMusicDifficulty(ctx, region, difficulty, compact)
		if err != nil {
			return nil, err
		}
		difficulties = append(difficulties, item)
	}

	return difficulties, nil
}

func (handler *MusicHandler) buildMusicDifficulty(ctx context.Context, region string, difficulty map[string]any, compact bool) (map[string]any, error) {
	result, err := shared.BuildRecordWithReleaseConditionResult(ctx, handler.masterDataSync, region, difficulty)
	if err != nil {
		return nil, err
	}
	if compact {
		delete(result, "id")
		delete(result, "musicId")
		delete(result, "totalNoteCount")
	}
	return result, nil
}

func (handler *MusicHandler) buildMusic(ctx context.Context, region string, record map[string]any) (map[string]any, error) {
	result, err := shared.BuildRecordWithReleaseConditionResult(ctx, handler.masterDataSync, region, record)
	if err != nil {
		return nil, err
	}

	if handler == nil || handler.masterDataSync == nil {
		return result, nil
	}

	if rawCreatorArtistID, hasCreatorArtistID := record["creatorArtistId"]; hasCreatorArtistID {
		delete(result, "creatorArtistId")

		creatorArtistLookupID := shared.NormalizeAnyID(rawCreatorArtistID)
		if creatorArtistLookupID == "" {
			result["creatorArtist"] = nil
		} else {
			creatorArtist, found, err := handler.masterDataSync.GetByID(ctx, region, "musicartists", creatorArtistLookupID)
			if err != nil {
				return nil, fmt.Errorf("get music artist %s: %w", creatorArtistLookupID, err)
			}
			if !found {
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
			if err != nil {
				return nil, fmt.Errorf("get live stage %s: %w", liveStageLookupID, err)
			}
			if !found {
				result["liveStage"] = nil
			} else {
				result["liveStage"] = liveStage
			}
		}
	}

	return result, nil
}

// VocalsByID godoc
// @Summary Get music vocals by music id
// @Description Returns all vocal variants for a specific music
// @Tags musics
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Music ID"
// @Success 200 {object} shared.MusicVocalsResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /musics/{region}/{id}/vocals [get]
func (handler *MusicHandler) VocalsByID(c *gin.Context) {
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, "musics") {
		return
	}

	if _, found, err := handler.masterDataSync.GetByID(c.Request.Context(), region, "musics", id); err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to query music")
		return
	} else if !found {
		response.Error(c, http.StatusNotFound, "MUSIC_NOT_FOUND", "music not found")
		return
	}

	vocals, err := handler.buildMusicVocalsByMusicID(c.Request.Context(), region, id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to query music vocals")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{"items": vocals})
}

// DetailByID godoc
// @Summary Get music detail composite by id
// @Description Returns music base info, difficulties, vocals, and tags in a single response
// @Tags musics
// @Produce json
// @Param region path string true "Region"
// @Param id path string true "Music ID"
// @Success 200 {object} shared.MusicDetailResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /musics/{region}/{id}/detail [get]
func (handler *MusicHandler) DetailByID(c *gin.Context) {
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
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, "musics") {
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

	ctx := c.Request.Context()

	music, err := handler.buildMusic(ctx, region, record)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to enrich music")
		return
	}

	difficulties, err := handler.loadMusicDifficultiesByMusicID(ctx, region, id, false)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to query music difficulties")
		return
	}

	vocals, err := handler.buildMusicVocalsByMusicID(ctx, region, id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to query music vocals")
		return
	}

	tags, err := handler.buildMusicTagsByMusicID(ctx, region, id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to query music tags")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{
		"music":        music,
		"difficulties": difficulties,
		"vocals":       vocals,
		"tags":         tags,
	})
}

func (handler *MusicHandler) buildMusicVocalsByMusicID(ctx context.Context, region string, musicID string) ([]map[string]any, error) {
	if handler == nil || handler.masterDataSync == nil {
		return nil, nil
	}

	matches, err := handler.masterDataSync.Search(ctx, region, "musicVocals", musicID, []string{"musicId"}, 1000000)
	if err != nil {
		return nil, fmt.Errorf("search musicVocals: %w", err)
	}

	targetMusicID := shared.NormalizeAnyID(musicID)
	items := make([]map[string]any, 0, len(matches))
	for _, match := range matches {
		if shared.NormalizeAnyID(match.Item["musicId"]) != targetMusicID {
			continue
		}
		items = append(items, shared.BuildRecordWithReleaseCondition(ctx, handler.masterDataSync, region, match.Item))
	}

	return items, nil
}

func (handler *MusicHandler) buildMusicTagsByMusicID(ctx context.Context, region string, musicID string) ([]string, error) {
	if handler == nil || handler.masterDataSync == nil {
		return nil, nil
	}

	tagRecords, err := handler.masterDataSync.ListAll(ctx, region, "musictags")
	if err != nil {
		return nil, fmt.Errorf("list musictags: %w", err)
	}

	targetMusicID := shared.NormalizeAnyID(musicID)
	tags := make([]string, 0)
	for _, tagRecord := range tagRecords {
		if shared.NormalizeAnyID(tagRecord["musicId"]) != targetMusicID {
			continue
		}
		musicTag := strings.TrimSpace(fmt.Sprintf("%v", tagRecord["musicTag"]))
		if musicTag != "" {
			tags = append(tags, musicTag)
		}
	}

	return tags, nil
}
