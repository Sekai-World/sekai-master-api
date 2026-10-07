package musics

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

const musicEnrichmentErrorMessage = "failed to enrich music"

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

	categories, err := handler.loadMusicCategoryRecords(c.Request.Context(), region, []string{id})
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", musicEnrichmentErrorMessage)
		return
	}

	item, err := handler.buildMusic(c.Request.Context(), region, record, categories, handler.masterDataSync.GetByID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", musicEnrichmentErrorMessage)
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
			records = shared.FilterSpoilerItemsContext(c.Request.Context(), records, time.Now().UTC())
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

	var musicCategories map[string][]string
	if len(options.Category) > 0 {
		var err error
		musicCategories, err = handler.loadMusicCategoryRecords(ctx, region, nil)
		if err != nil {
			return nil, err
		}
	}

	filtered := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if !musicMatchesFilterOptions(record, options, playLevels, tags, appendMusicIDs, musicCategories) {
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

func musicMatchesFilterOptions(record map[string]any, options musicListFilterOptions, playLevels map[string][]float64, tags map[string]map[string]struct{}, appendMusicIDs map[string]struct{}, musicCategories map[string][]string) bool {
	if !musicMatchesNameFilter(record, options) {
		return false
	}
	if len(options.Category) > 0 && !musicCategoryMatchesOptions(record, options.Category, musicCategories) {
		return false
	}
	if !musicMatchesCreatorFilters(record, options) {
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

func musicMatchesNameFilter(record map[string]any, options musicListFilterOptions) bool {
	return options.Name == "" ||
		musicValueContains(record["title"], options.Name) ||
		musicValueContains(record["pronunciation"], options.Name)
}

func musicMatchesCreatorFilters(record map[string]any, options musicListFilterOptions) bool {
	if len(options.Composer) > 0 && !musicValueContainsAny(record["composer"], options.Composer) {
		return false
	}
	if len(options.Arranger) > 0 && !musicValueContainsAny(record["arranger"], options.Arranger) {
		return false
	}
	if len(options.Lyricist) > 0 && !musicValueContainsAny(record["lyricist"], options.Lyricist) {
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

func (handler *MusicHandler) buildMusicList(ctx context.Context, region string, records []map[string]any) ([]map[string]any, error) {
	musicIDs := make([]string, 0, len(records))
	for _, record := range records {
		if musicID := shared.NormalizeAnyID(record["id"]); musicID != "" {
			musicIDs = append(musicIDs, musicID)
		}
	}
	difficulties, err := handler.loadMusicDifficultyRecords(ctx, region, musicIDs)
	if err != nil {
		return nil, err
	}
	tags, err := handler.loadMusicTagRecords(ctx, region, musicIDs)
	if err != nil {
		return nil, err
	}
	categories, err := handler.loadMusicCategoryRecords(ctx, region, musicIDs)
	if err != nil {
		return nil, err
	}
	related, err := handler.prefetchMusicRelations(ctx, region, records)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		musicID := shared.NormalizeAnyID(record["id"])
		item, err := handler.buildMusic(ctx, region, record, categories, related.Lookup)
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

// musicRecordsByMusicID reads entity's records of musicIDs through the musicId
// index, in stored order per music. A nil musicIDs reads every record, which
// only list filters over all musics need.
func (handler *MusicHandler) musicRecordsByMusicID(ctx context.Context, region string, entity string, musicIDs []string) ([]map[string]any, error) {
	if musicIDs == nil {
		return handler.masterDataSync.ListAll(ctx, region, entity)
	}
	lookups := make([][]any, 0, len(musicIDs))
	for _, musicID := range musicIDs {
		lookups = append(lookups, []any{musicID})
	}
	matches, err := handler.masterDataSync.ListByIndex(ctx, region, entity, "musicId", lookups)
	if err != nil {
		return nil, err
	}
	return slices.Concat(matches...), nil
}

func (handler *MusicHandler) loadMusicDifficultyRecords(ctx context.Context, region string, musicIDs []string) (map[string][]map[string]any, error) {
	if handler == nil || handler.masterDataSync == nil {
		return map[string][]map[string]any{}, nil
	}

	difficultyRecords, err := handler.musicRecordsByMusicID(ctx, region, "musicdifficulties", musicIDs)
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

func (handler *MusicHandler) loadMusicTagRecords(ctx context.Context, region string, musicIDs []string) (map[string][]string, error) {
	if handler == nil || handler.masterDataSync == nil {
		return map[string][]string{}, nil
	}

	tagRecords, err := handler.musicRecordsByMusicID(ctx, region, "musictags", musicIDs)
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

func (handler *MusicHandler) loadMusicCategoryRecords(ctx context.Context, region string, musicIDs []string) (map[string][]string, error) {
	if handler == nil || handler.masterDataSync == nil {
		return map[string][]string{}, nil
	}

	categoryRecords, err := handler.musicRecordsByMusicID(ctx, region, "musiccategories", musicIDs)
	if err != nil {
		return nil, fmt.Errorf("list musiccategories: %w", err)
	}

	return aggregateMusicCategoryRecords(categoryRecords), nil
}

func aggregateMusicCategoryRecords(categoryRecords []map[string]any) map[string][]string {
	categories := make(map[string][]string, len(categoryRecords))
	for _, categoryRecord := range categoryRecords {
		musicID := shared.NormalizeAnyID(categoryRecord["musicId"])
		musicCategory := normalizeMusicCategoryName(categoryRecord["musicCategoryName"])
		if musicCategory == "" {
			musicCategory = normalizeMusicCategoryName(categoryRecord["musicCategory"])
		}
		if musicID == "" || musicCategory == "" {
			continue
		}
		categories[musicID] = append(categories[musicID], musicCategory)
	}

	return categories
}

func (handler *MusicHandler) buildMusicVideos(ctx context.Context, region string, musicID string, musicRecord map[string]any, categoryRecords []map[string]any) ([]shared.MusicVideoResponse, error) {
	targetMusicID := shared.NormalizeAnyID(musicID)
	categoryRowsExist := false
	categories := make([]musicVideoCategory, 0, len(categoryRecords))
	for _, categoryRecord := range categoryRecords {
		if shared.NormalizeAnyID(categoryRecord["musicId"]) != targetMusicID {
			continue
		}
		categoryRowsExist = true

		category := musicVideoCategoryName(categoryRecord)
		if category == "" {
			continue
		}
		variantID, hasVariantID := musicVideoVariantReference(categoryRecord)
		categories = append(categories, musicVideoCategory{
			name:               category,
			variantID:          variantID,
			hasExplicitVariant: hasVariantID,
		})
	}

	if !categoryRowsExist {
		categories = extractEmbeddedMusicVideoCategories(musicRecord)
	}
	if len(categories) == 0 {
		return []shared.MusicVideoResponse{}, nil
	}

	variantIDs := make([]string, 0, len(categories))
	seenVariantIDs := make(map[string]struct{}, len(categories))
	for _, category := range categories {
		if !category.hasExplicitVariant || category.variantID == "" {
			continue
		}
		if _, exists := seenVariantIDs[category.variantID]; exists {
			continue
		}
		seenVariantIDs[category.variantID] = struct{}{}
		variantIDs = append(variantIDs, category.variantID)
	}

	variantsByID := make(map[string]musicVideoAssetVariant, len(variantIDs))
	vocalIDs := make([]string, 0, len(variantIDs))
	seenVocalIDs := make(map[string]struct{}, len(variantIDs))
	if len(variantIDs) > 0 {
		variantRecords, err := handler.masterDataSync.GetByIDs(ctx, region, "musicassetvariants", variantIDs)
		if err != nil {
			return nil, fmt.Errorf("get musicassetvariants: %w", err)
		}
		for index, variantID := range variantIDs {
			if index >= len(variantRecords) {
				continue
			}
			variantRecord := variantRecords[index]
			if variantRecord == nil || shared.NormalizeAnyID(variantRecord["id"]) != variantID {
				continue
			}
			if shared.NormalizeComparableText(variantRecord["musicAssetType"]) != "mv" {
				continue
			}
			assetbundleName, valid := safeMusicVideoAssetbundleName(variantRecord["assetbundleName"])
			if !valid {
				continue
			}

			musicVocalID := shared.NormalizeAnyID(variantRecord["musicVocalId"])
			variantsByID[variantID] = musicVideoAssetVariant{
				assetbundleName: assetbundleName,
				musicVocalID:    musicVocalID,
			}
			if musicVocalID != "" {
				if _, exists := seenVocalIDs[musicVocalID]; !exists {
					seenVocalIDs[musicVocalID] = struct{}{}
					vocalIDs = append(vocalIDs, musicVocalID)
				}
			}
		}
	}

	validVocalIDs := make(map[string]struct{}, len(vocalIDs))
	if len(vocalIDs) > 0 {
		vocalRecords, err := handler.masterDataSync.GetByIDs(ctx, region, "musicvocals", vocalIDs)
		if err != nil {
			return nil, fmt.Errorf("get musicvocals: %w", err)
		}
		for index, vocalID := range vocalIDs {
			if index >= len(vocalRecords) {
				continue
			}
			vocalRecord := vocalRecords[index]
			if vocalRecord == nil || shared.NormalizeAnyID(vocalRecord["id"]) != vocalID {
				continue
			}
			if shared.NormalizeAnyID(vocalRecord["musicId"]) != targetMusicID {
				continue
			}
			validVocalIDs[vocalID] = struct{}{}
		}
	}

	defaultAssetbundleName := musicVideoDefaultAssetbundleName(targetMusicID)
	musicVideos := make([]shared.MusicVideoResponse, 0, len(categories))
	for _, category := range categories {
		assetbundleName := defaultAssetbundleName
		musicVocalID := ""
		if category.hasExplicitVariant {
			if category.variantID == "" {
				continue
			}
			variant, exists := variantsByID[category.variantID]
			if !exists {
				continue
			}
			assetbundleName = variant.assetbundleName
			musicVocalID = variant.musicVocalID
			if musicVocalID != "" {
				if _, valid := validVocalIDs[musicVocalID]; !valid {
					continue
				}
			}
		}

		musicVideos = append(musicVideos, shared.MusicVideoResponse{
			Category:        category.name,
			AssetbundleName: assetbundleName,
			MusicVocalID:    musicVocalID,
		})
	}

	return musicVideos, nil
}

type musicVideoCategory struct {
	name               string
	variantID          string
	hasExplicitVariant bool
}

type musicVideoAssetVariant struct {
	assetbundleName string
	musicVocalID    string
}

func musicVideoCategoryName(record map[string]any) string {
	category, ok := record["musicCategoryName"].(string)
	if !ok || strings.TrimSpace(category) == "" {
		category, ok = record["musicCategory"].(string)
		if !ok {
			return ""
		}
	}

	switch strings.TrimSpace(category) {
	case "original", "mv_2d":
		return strings.TrimSpace(category)
	default:
		return ""
	}
}

func musicVideoVariantReference(record map[string]any) (string, bool) {
	value, present := record["musicAssetVariantId"]
	if !present {
		return "", false
	}
	variantID, valid := parseMusicAssetVariantID(value)
	if !valid {
		return "", true
	}
	return variantID, true
}

func parseMusicAssetVariantID(value any) (string, bool) {
	switch value := value.(type) {
	case string:
		return canonicalMusicAssetVariantID(value)
	case json.Number:
		return canonicalMusicAssetVariantID(string(value))
	case int:
		if value < 0 {
			return "", false
		}
		return strconv.FormatInt(int64(value), 10), true
	case int8:
		if value < 0 {
			return "", false
		}
		return strconv.FormatInt(int64(value), 10), true
	case int16:
		if value < 0 {
			return "", false
		}
		return strconv.FormatInt(int64(value), 10), true
	case int32:
		if value < 0 {
			return "", false
		}
		return strconv.FormatInt(int64(value), 10), true
	case int64:
		if value < 0 {
			return "", false
		}
		return strconv.FormatInt(value, 10), true
	case uint:
		return strconv.FormatUint(uint64(value), 10), true
	case uint8:
		return strconv.FormatUint(uint64(value), 10), true
	case uint16:
		return strconv.FormatUint(uint64(value), 10), true
	case uint32:
		return strconv.FormatUint(uint64(value), 10), true
	case uint64:
		return strconv.FormatUint(value, 10), true
	case float32:
		return canonicalMusicAssetVariantFloat(float64(value))
	case float64:
		return canonicalMusicAssetVariantFloat(value)
	default:
		return "", false
	}
}

func canonicalMusicAssetVariantID(value string) (string, bool) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return "", false
	}
	return strconv.FormatUint(parsed, 10), true
}

func canonicalMusicAssetVariantFloat(value float64) (string, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || math.Trunc(value) != value {
		return "", false
	}
	return canonicalMusicAssetVariantID(strconv.FormatFloat(value, 'f', 0, 64))
}

func extractEmbeddedMusicVideoCategories(record map[string]any) []musicVideoCategory {
	categories := make([]musicVideoCategory, 0)
	for _, key := range []string{"categories", "category", "musicCategory"} {
		categories = append(categories, collectEmbeddedMusicVideoCategories(record[key])...)
	}
	return categories
}

func collectEmbeddedMusicVideoCategories(value any) []musicVideoCategory {
	switch value := value.(type) {
	case []any:
		categories := make([]musicVideoCategory, 0, len(value))
		for _, item := range value {
			categories = append(categories, collectEmbeddedMusicVideoCategories(item)...)
		}
		return categories
	case []string:
		categories := make([]musicVideoCategory, 0, len(value))
		for _, item := range value {
			categories = append(categories, collectEmbeddedMusicVideoCategories(item)...)
		}
		return categories
	case []map[string]any:
		categories := make([]musicVideoCategory, 0, len(value))
		for _, item := range value {
			categories = append(categories, collectEmbeddedMusicVideoCategories(item)...)
		}
		return categories
	case map[string]any:
		category := musicVideoCategoryName(value)
		if category == "" {
			return nil
		}
		variantID, hasVariantID := musicVideoVariantReference(value)
		return []musicVideoCategory{{
			name:               category,
			variantID:          variantID,
			hasExplicitVariant: hasVariantID,
		}}
	case string:
		category := musicVideoCategoryName(map[string]any{"musicCategoryName": value})
		if category == "" {
			return nil
		}
		return []musicVideoCategory{{name: category}}
	default:
		return nil
	}
}

func safeMusicVideoAssetbundleName(value any) (string, bool) {
	assetbundleName, ok := value.(string)
	if !ok {
		return "", false
	}
	for _, character := range assetbundleName {
		if unicode.IsControl(character) {
			return "", false
		}
	}

	assetbundleName = strings.TrimSpace(assetbundleName)
	if assetbundleName == "" || assetbundleName == "." || strings.Contains(assetbundleName, "..") || strings.ContainsAny(assetbundleName, `/\?#%`) {
		return "", false
	}
	return assetbundleName, true
}

func musicVideoDefaultAssetbundleName(musicID string) string {
	id, err := strconv.Atoi(musicID)
	if err != nil {
		return musicID
	}
	return fmt.Sprintf("%04d", id)
}

func normalizeMusicCategoryName(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

func resolveMusicCategories(record map[string]any, aggregatedCategories map[string][]string, musicID string) []string {
	if aggregated, ok := aggregatedCategories[musicID]; ok && len(aggregated) > 0 {
		return aggregated
	}
	if embedded := extractEmbeddedMusicCategories(record); len(embedded) > 0 {
		return embedded
	}
	return []string{}
}

func extractEmbeddedMusicCategories(record map[string]any) []string {
	collected := make([]string, 0)
	for _, key := range []string{"categories", "category", "musicCategory"} {
		collected = append(collected, collectMusicCategoryValues(record[key])...)
	}
	return collected
}

func collectMusicCategoryValues(value any) []string {
	switch typed := value.(type) {
	case []any:
		return normalizeMusicCategorySlice(typed)
	case []string:
		return normalizeMusicCategorySlice(toStringSlice(typed))
	default:
		if name := embeddedMusicCategoryName(value); name != "" {
			return []string{name}
		}
		return []string{}
	}
}

// embeddedMusicCategoryName reads one embedded category: a plain name, as in the EN
// master data, or an object that carries the name, as in the KR, TW, and CN master
// data ({"musicCategoryName": "mv"}).
func embeddedMusicCategoryName(value any) string {
	object, ok := value.(map[string]any)
	if !ok {
		return shared.NormalizeComparableText(value)
	}
	for _, key := range []string{"musicCategoryName", "musicCategory"} {
		if name, ok := object[key].(string); ok {
			if normalized := shared.NormalizeComparableText(name); normalized != "" {
				return normalized
			}
		}
	}
	return ""
}

func toStringSlice(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func normalizeMusicCategorySlice(values []any) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if name := embeddedMusicCategoryName(value); name != "" {
			normalized = append(normalized, name)
		}
	}
	return normalized
}

func musicCategoryMatchesOptions(record map[string]any, queries map[string]struct{}, musicCategories map[string][]string) bool {
	musicID := shared.NormalizeAnyID(record["id"])
	if aggregated, ok := musicCategories[musicID]; ok && len(aggregated) > 0 {
		return musicStringSliceContainsAny(aggregated, queries)
	}
	// Match category names exactly, as for aggregated categories: "mv" must not match "mv_2d".
	return musicStringSliceContainsAny(extractEmbeddedMusicCategories(record), queries)
}

func musicStringSliceContainsAny(values []string, queries map[string]struct{}) bool {
	for _, value := range values {
		if _, ok := queries[shared.NormalizeComparableText(value)]; ok {
			return true
		}
	}
	return false
}

func (handler *MusicHandler) loadMusicDifficultiesByMusicID(ctx context.Context, region string, musicID string, compact bool) ([]map[string]any, error) {
	difficultyRecords, err := handler.musicRecordsByMusicID(ctx, region, "musicdifficulties", []string{musicID})
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

// prefetchMusicRelations reads the release conditions, creator artists, and
// live stages of records with one batched read per entity.
func (handler *MusicHandler) prefetchMusicRelations(ctx context.Context, region string, records []map[string]any) (*shared.PrefetchedRecords, error) {
	ids := map[string][]string{}
	for _, record := range records {
		ids["releaseconditions"] = append(ids["releaseconditions"], shared.NormalizeAnyID(record["releaseConditionId"]))
		ids["musicartists"] = append(ids["musicartists"], shared.NormalizeAnyID(record["creatorArtistId"]))
		ids["livestages"] = append(ids["livestages"], shared.NormalizeAnyID(record["liveStageId"]))
	}
	return shared.PrefetchRecords(ctx, handler.masterDataSync, region, ids)
}

// buildMusic builds a music's fields, reading its release condition, creator
// artist, and live stage through lookup.
func (handler *MusicHandler) buildMusic(ctx context.Context, region string, record map[string]any, aggregatedCategories map[string][]string, lookup shared.RecordLookup) (map[string]any, error) {
	if handler == nil || handler.masterDataSync == nil {
		lookup = nil
	}
	result, err := shared.BuildRecordWithReleaseConditionLookup(ctx, lookup, region, record)
	if err != nil {
		return nil, err
	}

	if handler == nil || handler.masterDataSync == nil {
		return result, nil
	}

	result["categories"] = resolveMusicCategories(record, aggregatedCategories, shared.NormalizeAnyID(record["id"]))

	if err := attachCreatorArtist(ctx, lookup, region, record, result); err != nil {
		return nil, err
	}
	if err := attachLiveStage(ctx, lookup, region, record, result); err != nil {
		return nil, err
	}

	return result, nil
}

func attachCreatorArtist(ctx context.Context, lookup shared.RecordLookup, region string, record map[string]any, result map[string]any) error {
	rawCreatorArtistID, hasCreatorArtistID := record["creatorArtistId"]
	if !hasCreatorArtistID {
		return nil
	}
	delete(result, "creatorArtistId")

	creatorArtistLookupID := shared.NormalizeAnyID(rawCreatorArtistID)
	if creatorArtistLookupID == "" {
		result["creatorArtist"] = nil
		return nil
	}

	creatorArtist, found, err := lookup(ctx, region, "musicartists", creatorArtistLookupID)
	if err != nil {
		return fmt.Errorf("get music artist %s: %w", creatorArtistLookupID, err)
	}
	if !found {
		result["creatorArtist"] = nil
	} else {
		result["creatorArtist"] = creatorArtist
	}
	return nil
}

func attachLiveStage(ctx context.Context, lookup shared.RecordLookup, region string, record map[string]any, result map[string]any) error {
	rawLiveStageID, hasLiveStageID := record["liveStageId"]
	if !hasLiveStageID {
		return nil
	}
	delete(result, "liveStageId")

	liveStageLookupID := shared.NormalizeAnyID(rawLiveStageID)
	if liveStageLookupID == "" {
		result["liveStage"] = nil
		return nil
	}

	liveStage, found, err := lookup(ctx, region, "livestages", liveStageLookupID)
	if err != nil {
		return fmt.Errorf("get live stage %s: %w", liveStageLookupID, err)
	}
	if !found {
		result["liveStage"] = nil
	} else {
		result["liveStage"] = liveStage
	}
	return nil
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
// @Description Returns music base info, difficulties, vocals, tags, playable music videos, and original video links in a single response
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

	categoryRecords, err := handler.musicRecordsByMusicID(ctx, region, "musiccategories", []string{id})
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", musicEnrichmentErrorMessage)
		return
	}
	categories := aggregateMusicCategoryRecords(categoryRecords)

	musicVideos, err := handler.buildMusicVideos(ctx, region, id, record, categoryRecords)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", musicEnrichmentErrorMessage)
		return
	}

	music, err := handler.buildMusic(ctx, region, record, categories, handler.masterDataSync.GetByID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", musicEnrichmentErrorMessage)
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

	musicOriginals, err := handler.buildMusicOriginalsByMusicID(ctx, region, id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MUSIC_QUERY_ERROR", "failed to query music originals")
		return
	}

	musicCategories := resolveMusicCategories(record, categories, shared.NormalizeAnyID(record["id"]))

	response.JSON(c, http.StatusOK, gin.H{
		"music":          music,
		"difficulties":   difficulties,
		"vocals":         vocals,
		"tags":           tags,
		"categories":     musicCategories,
		"musicVideos":    musicVideos,
		"musicOriginals": musicOriginals,
	})
}

func (handler *MusicHandler) buildMusicOriginalsByMusicID(ctx context.Context, region string, musicID string) ([]shared.MusicOriginalResponse, error) {
	originals := make([]shared.MusicOriginalResponse, 0)
	if handler == nil || handler.masterDataSync == nil {
		return originals, nil
	}

	targetMusicID, ok := normalizeMusicOriginalID(musicID)
	if !ok {
		return originals, nil
	}

	matches, err := handler.masterDataSync.ListByIndex(ctx, region, "musicoriginals", "musicId", [][]any{{musicID}})
	if err != nil {
		return nil, fmt.Errorf("list musicoriginals: %w", err)
	}
	if len(matches) == 0 {
		return originals, nil
	}

	for _, record := range matches[0] {
		if record == nil {
			continue
		}

		id, idOK := normalizeMusicOriginalID(record["id"])
		relatedMusicID, musicIDOK := normalizeMusicOriginalID(record["musicId"])
		videoLink, videoLinkOK := record["videoLink"].(string)
		videoLink = strings.TrimSpace(videoLink)
		if !idOK || !musicIDOK || relatedMusicID != targetMusicID || !videoLinkOK || videoLink == "" {
			continue
		}

		originals = append(originals, shared.MusicOriginalResponse{
			ID:        id,
			MusicID:   relatedMusicID,
			VideoLink: videoLink,
		})
	}

	return originals, nil
}

func normalizeMusicOriginalID(value any) (string, bool) {
	switch typed := value.(type) {
	case string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) || math.Trunc(float64(typed)) != float64(typed) {
			return "", false
		}
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed {
			return "", false
		}
	default:
		return "", false
	}

	id := shared.NormalizeAnyID(value)
	return id, id != ""
}

func (handler *MusicHandler) buildMusicVocalsByMusicID(ctx context.Context, region string, musicID string) ([]map[string]any, error) {
	if handler == nil || handler.masterDataSync == nil {
		return nil, nil
	}

	records, err := handler.musicRecordsByMusicID(ctx, region, "musicvocals", []string{musicID})
	if err != nil {
		return nil, fmt.Errorf("list musicvocals: %w", err)
	}

	targetMusicID := shared.NormalizeAnyID(musicID)
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		if shared.NormalizeAnyID(record["musicId"]) != targetMusicID {
			continue
		}
		items = append(items, shared.BuildRecordWithReleaseCondition(ctx, handler.masterDataSync, region, record))
	}

	return items, nil
}

func (handler *MusicHandler) buildMusicTagsByMusicID(ctx context.Context, region string, musicID string) ([]string, error) {
	if handler == nil || handler.masterDataSync == nil {
		return nil, nil
	}

	tagRecords, err := handler.musicRecordsByMusicID(ctx, region, "musictags", []string{musicID})
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
