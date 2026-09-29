package lookups

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
)

const (
	mysekaiMusicRecordsEntity       = "mysekaimusicrecords"
	musicSoundTracksEntity          = "musicsoundtracks"
	musicSoundTrackCategoriesEntity = "musicsoundtrackcategories"
	musicsEntity                    = "musics"

	mysekaiMusicTrackTypeMusic      = "music"
	mysekaiMusicTrackTypeSoundTrack = "music_sound_track"
)

// MysekaiMusicRecordsList godoc
// @Summary List MySekai music records by page
// @Description Lists music records in stored order with the song or sound track each one plays. Records of unpublished songs are hidden unless spoiler=true; records whose song or sound track is missing are omitted.
// @Tags mysekaiMusicRecords
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param track_type query string false "Track type (music|music_sound_track)"
// @Param sound_track_category_id query string false "Comma-separated sound-track category IDs; keeps sound-track records only"
// @Param name query string false "Case-insensitive substring of the song or sound-track title"
// @Param spoiler query bool false "Include records of unpublished songs"
// @Success 200 {object} shared.MysekaiMusicRecordListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /mysekaiMusicRecords/{region}/list [get]
func (handler *LookupHandler) MysekaiMusicRecordsList(c *gin.Context) {
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
	trackType := c.Query("track_type")
	if trackType != "" && trackType != mysekaiMusicTrackTypeMusic && trackType != mysekaiMusicTrackTypeSoundTrack {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "track_type must be music or music_sound_track")
		return
	}
	filters, ok := shared.ParseRecordFilters(c, map[string]string{"sound_track_category_id": "musicSoundTrackCategoryId"})
	if !ok {
		return
	}
	includeSpoilers, ok := shared.ParseSpoilerOption(c)
	if !ok {
		return
	}
	nameFilter := shared.NormalizeComparableText(c.Query("name"))
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, mysekaiMusicRecordsEntity) {
		return
	}

	records, err := handler.loadMysekaiMusicRecords(c.Request.Context(), region, includeSpoilers)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MYSEKAI_MUSIC_RECORD_QUERY_ERROR", "failed to list MySekai music records")
		return
	}
	categoryIDs := filters["musicSoundTrackCategoryId"]
	filtered := make([]shared.MysekaiMusicRecordResponse, 0, len(records))
	for _, record := range records {
		if trackType != "" && record.MysekaiMusicTrackType != trackType {
			continue
		}
		if len(categoryIDs) > 0 && (record.SoundTrack == nil || !optionalIDIn(record.SoundTrack.MusicSoundTrackCategoryID, categoryIDs)) {
			continue
		}
		if nameFilter != "" && !mysekaiMusicRecordMatchesName(record, nameFilter) {
			continue
		}
		filtered = append(filtered, record)
	}

	items := pageSlice(filtered, page, pageSize)
	if items == nil {
		items = []shared.MysekaiMusicRecordResponse{}
	}
	response.JSON(c, http.StatusOK, shared.MysekaiMusicRecordListResponse{
		Items:      items,
		Pagination: lookupPaginationResponse(page, pageSize, len(filtered)),
	})
}

// MysekaiMusicRecordFilters godoc
// @Summary List the MySekai music record filters
// @Description Returns the sound-track categories that at least one sound-track record uses, by ID.
// @Tags mysekaiMusicRecords
// @Produce json
// @Param region path string true "Region"
// @Success 200 {object} shared.MysekaiMusicRecordFiltersResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /mysekaiMusicRecords/{region}/filters [get]
func (handler *LookupHandler) MysekaiMusicRecordFilters(c *gin.Context) {
	if handler.masterDataSync == nil {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_DISABLED", "master data service is not ready")
		return
	}

	region, ok := parseTypedLookupRegion(c, handler.masterDataSync)
	if !ok {
		return
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, mysekaiMusicRecordsEntity) {
		return
	}

	ctx := c.Request.Context()
	records, err := handler.loadMysekaiMusicRecords(ctx, region, true)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MYSEKAI_MUSIC_RECORD_QUERY_ERROR", "failed to list MySekai music record filters")
		return
	}
	categoryIDs := []int64{}
	for _, record := range records {
		if record.SoundTrack != nil && record.SoundTrack.MusicSoundTrackCategoryID != nil {
			categoryIDs = appendUniqueID(categoryIDs, *record.SoundTrack.MusicSoundTrackCategoryID)
		}
	}
	slices.Sort(categoryIDs)
	prefetched, err := shared.PrefetchRecords(ctx, handler.masterDataSync, region, map[string][]string{musicSoundTrackCategoriesEntity: formatIDs(categoryIDs)})
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "MYSEKAI_MUSIC_RECORD_QUERY_ERROR", "failed to list MySekai music record filters")
		return
	}

	result := shared.MysekaiMusicRecordFiltersResponse{SoundTrackCategories: []shared.MusicSoundTrackCategoryResponse{}}
	for _, categoryID := range categoryIDs {
		record, ok := prefetched.Record(musicSoundTrackCategoriesEntity, formatID(categoryID))
		if !ok {
			continue
		}
		source := recordSource(record)
		result.SoundTrackCategories = append(result.SoundTrackCategories, shared.MusicSoundTrackCategoryResponse{
			ID:              categoryID,
			Name:            sourceString(source, "name"),
			AssetbundleName: sourceOptionalString(source, "assetbundleName"),
		})
	}
	response.JSON(c, http.StatusOK, result)
}

// loadMysekaiMusicRecords returns every music record, in stored order, joined
// to the song or sound track it plays through their list projections. Records
// of unpublished songs are dropped unless includeSpoilers, and the response
// cache learns when the first of them is published.
func (handler *LookupHandler) loadMysekaiMusicRecords(ctx context.Context, region string, includeSpoilers bool) ([]shared.MysekaiMusicRecordResponse, error) {
	records, err := handler.masterDataSync.LoadProjection(ctx, region, mysekaiMusicRecordsEntity)
	if err != nil {
		return nil, err
	}
	musics, err := handler.masterDataSync.LoadProjection(ctx, region, musicsEntity)
	if err != nil {
		return nil, err
	}
	soundTracks, err := handler.masterDataSync.LoadProjection(ctx, region, musicSoundTracksEntity)
	if err != nil {
		return nil, err
	}

	musicRows := make(map[int64]map[string]any, musics.Len())
	for row := range musics.Len() {
		record := musics.Row(row)
		if id, ok := sourceID(recordSource(record)); ok {
			if _, exists := musicRows[id]; !exists {
				musicRows[id] = record
			}
		}
	}
	if !includeSpoilers {
		published := shared.FilterSpoilerItemsContext(ctx, mapValuesByID(musicRows), time.Now().UTC())
		visible := make(map[int64]map[string]any, len(published))
		for _, record := range published {
			id, _ := sourceID(recordSource(record))
			visible[id] = record
		}
		musicRows = visible
	}
	soundTrackRows := make(map[int64]fieldSource, soundTracks.Len())
	for row := range soundTracks.Len() {
		source := projectionRowSource{projection: soundTracks, row: row}
		if id, ok := sourceID(source); ok {
			if _, exists := soundTrackRows[id]; !exists {
				soundTrackRows[id] = source
			}
		}
	}

	result := make([]shared.MysekaiMusicRecordResponse, 0, records.Len())
	seen := make(map[int64]struct{}, records.Len())
	for row := range records.Len() {
		source := projectionRowSource{projection: records, row: row}
		id, ok := sourceID(source)
		if !ok {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		externalID, ok := sourceInt64(source, "externalId")
		if !ok {
			continue
		}
		record := shared.MysekaiMusicRecordResponse{
			ID:                    id,
			MysekaiMusicTrackType: sourceString(source, "mysekaiMusicTrackType"),
			ExternalID:            externalID,
		}
		switch record.MysekaiMusicTrackType {
		case mysekaiMusicTrackTypeMusic:
			music, ok := musicRows[externalID]
			if !ok {
				continue
			}
			musicSource := recordSource(music)
			record.Music = &shared.MysekaiMusicRecordMusicResponse{
				ID:              externalID,
				Title:           sourceString(musicSource, "title"),
				AssetbundleName: sourceOptionalString(musicSource, "assetbundleName"),
			}
		case mysekaiMusicTrackTypeSoundTrack:
			soundTrack, ok := soundTrackRows[externalID]
			if !ok {
				continue
			}
			record.SoundTrack = &shared.MysekaiMusicRecordSoundTrackResponse{
				ID:                        externalID,
				Title:                     sourceString(soundTrack, "title"),
				MusicSoundTrackCategoryID: sourceOptionalInt64(soundTrack, "musicSoundTrackCategoryId"),
				AssetbundleName:           sourceOptionalString(soundTrack, "assetbundleName"),
				AssetbundleFileName:       sourceOptionalString(soundTrack, "assetbundleFileName"),
			}
		default:
			continue
		}
		seen[id] = struct{}{}
		result = append(result, record)
	}
	return result, nil
}

func mysekaiMusicRecordMatchesName(record shared.MysekaiMusicRecordResponse, nameFilter string) bool {
	switch {
	case record.Music != nil:
		return matchesName(recordSource{"title": record.Music.Title}, nameFilter, "title")
	case record.SoundTrack != nil:
		return matchesName(recordSource{"title": record.SoundTrack.Title}, nameFilter, "title")
	}
	return false
}

// mapValuesByID returns the records in ID order, so spoiler filtering sees a
// stable input.
func mapValuesByID(records map[int64]map[string]any) []map[string]any {
	ids := make([]int64, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	values := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		values = append(values, records[id])
	}
	return values
}
