package lookups

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/cachehint"
	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/response"
)

const (
	costume3DColorsEntity = "costume3dcolors"

	costume3DOutfitQueryError = "COSTUME_3D_OUTFIT_QUERY_ERROR"
	// costume3DDisplayNone marks rows that are not outfits: JP's 未設定 and
	// バーチャルライブ用表情差分 placeholders.
	costume3DDisplayNone = "none"
	costume3DPartBody    = "body"
)

var costume3DOutfitSortableFields = []string{"id", "name", "publishedAt"}

// costume3DGameLaunchMillis is when the game launched. Costumes dated earlier
// carry a placeholder rather than a release date: 2009-01-30 in JP, EN, KR, and CN.
var costume3DGameLaunchMillis = time.Date(2020, time.September, 30, 0, 0, 0, 0, time.UTC).UnixMilli()

// costume3DOutfit is one outfit: the costumes sharing a name, across every
// character who has it. A costume3dGroupId is only one character's version, so
// listing groups would repeat each outfit once per character. Its ID is its
// lowest costume ID, which stays the same when a hidden costume is published.
type costume3DOutfit struct {
	id   int64
	rows []costume3DRow
}

// Costume3DOutfitsList godoc
// @Summary List 3D costume outfits by page
// @Description Lists outfits, each the costumes of one name across every character who has it, in ID order. Costumes whose publish time is in the future are hidden unless spoiler=true, and placeholder costumes (archiveDisplayType none) are never listed.
// @Tags costume3dOutfits
// @Produce json
// @Param region path string true "Region"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param name query string false "Case-insensitive substring of the outfit name"
// @Param character_id query string false "Comma-separated character IDs; the outfit must have at least one of them"
// @Param rarity query string false "Comma-separated rarities"
// @Param spoiler query bool false "Include costumes that are not published yet"
// @Param sort_by query string false "Sort field (id|name|publishedAt)"
// @Param sort_order query string false "Sort order (asc|desc)"
// @Success 200 {object} shared.Costume3DOutfitListResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /costume3dOutfits/{region}/list [get]
func (handler *LookupHandler) Costume3DOutfitsList(c *gin.Context) {
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
	sortOptions, ok := shared.ParseListSortOptions(c)
	if !ok || (sortOptions.Enabled && !shared.ValidateSortField(c, sortOptions.Field, nil, costume3DOutfitSortableFields)) {
		return
	}
	characterFilters, ok := shared.ParseRecordFilters(c, map[string]string{"character_id": "characterId"})
	if !ok {
		return
	}
	includeSpoilers, ok := shared.ParseSpoilerOption(c)
	if !ok {
		return
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, costume3dsEntity) {
		return
	}

	outfits, err := handler.loadCostume3DOutfits(c.Request.Context(), region, includeSpoilers)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, costume3DOutfitQueryError, "failed to list 3D costume outfits")
		return
	}
	outfits = filterCostume3DOutfits(
		outfits,
		shared.NormalizeComparableText(c.Query("name")),
		characterFilters["characterId"],
		parseCommaSeparatedValues(c.Query("rarity")),
	)
	if sortOptions.Enabled {
		sortCostume3DOutfits(outfits, sortOptions.Field, sortOptions.Descending)
	}

	items := make([]shared.Costume3DOutfitResponse, 0, pageSize)
	for _, outfit := range pageSlice(outfits, page, pageSize) {
		items = append(items, outfit.response())
	}
	response.JSON(c, http.StatusOK, shared.Costume3DOutfitListResponse{
		Items:      items,
		Pagination: lookupPaginationResponse(page, pageSize, len(outfits)),
	})
}

// Costume3DOutfitsByID godoc
// @Summary Get a 3D costume outfit by id
// @Description Returns the outfit with its colors and every costume in it. Costumes whose publish time is in the future are hidden unless spoiler=true.
// @Tags costume3dOutfits
// @Produce json
// @Param region path string true "Region"
// @Param id path int true "Outfit ID: its lowest costume ID" minimum(1)
// @Param spoiler query bool false "Include costumes that are not published yet"
// @Success 200 {object} shared.Costume3DOutfitDetailResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 404 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Failure 500 {object} shared.ErrorResponse
// @Router /costume3dOutfits/{region}/{id} [get]
func (handler *LookupHandler) Costume3DOutfitsByID(c *gin.Context) {
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
	includeSpoilers, ok := shared.ParseSpoilerOption(c)
	if !ok {
		return
	}
	if !shared.EnsureRegionReadyForEntityRecords(c, handler.masterDataSync, region, costume3dsEntity) {
		return
	}

	ctx := c.Request.Context()
	outfits, err := handler.loadCostume3DOutfits(ctx, region, includeSpoilers)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, costume3DOutfitQueryError, "failed to query 3D costume outfit")
		return
	}
	position := slices.IndexFunc(outfits, func(outfit *costume3DOutfit) bool { return formatID(outfit.id) == id })
	if position < 0 {
		response.Error(c, http.StatusNotFound, "COSTUME_3D_OUTFIT_NOT_FOUND", "3D costume outfit not found")
		return
	}
	detail, err := handler.costume3DOutfitDetail(ctx, region, outfits[position])
	if err != nil {
		response.Error(c, http.StatusInternalServerError, costume3DOutfitQueryError, "failed to query 3D costume outfit")
		return
	}
	response.JSON(c, http.StatusOK, detail)
}

// loadCostume3DOutfits groups every listed costume into outfits in ID order.
// Unpublished costumes are dropped unless includeSpoilers, and the response
// cache learns when the first of them is published.
func (handler *LookupHandler) loadCostume3DOutfits(ctx context.Context, region string, includeSpoilers bool) ([]*costume3DOutfit, error) {
	rows, err := handler.loadCostume3DRows(ctx, region)
	if err != nil {
		return nil, err
	}
	outfits, firstReveal := groupCostume3DOutfits(rows, includeSpoilers, time.Now().UTC())
	cachehint.ValidUntil(ctx, firstReveal)
	return outfits, nil
}

// groupCostume3DOutfits returns the outfits with a visible costume in ID order,
// and the first time a hidden costume is published.
func groupCostume3DOutfits(rows []costume3DRow, includeSpoilers bool, now time.Time) ([]*costume3DOutfit, time.Time) {
	ordered := slices.Clone(rows)
	slices.SortFunc(ordered, func(left costume3DRow, right costume3DRow) int { return cmp.Compare(left.id, right.id) })

	var firstReveal time.Time
	byKey := make(map[string]*costume3DOutfit)
	outfits := []*costume3DOutfit{}
	for _, row := range ordered {
		if row.fieldValue("archiveDisplayType") == any(costume3DDisplayNone) {
			continue
		}
		key := costume3DOutfitKey(row)
		if key == "" {
			continue
		}
		outfit, exists := byKey[key]
		if !exists {
			outfit = &costume3DOutfit{id: row.id}
			byKey[key] = outfit
			outfits = append(outfits, outfit)
		}
		if publishedAt, ok := row.publishedAtMillis(); ok && publishedAt > now.UnixMilli() && !includeSpoilers {
			if reveal := time.UnixMilli(publishedAt); firstReveal.IsZero() || reveal.Before(firstReveal) {
				firstReveal = reveal
			}
			continue
		}
		outfit.rows = append(outfit.rows, row)
	}
	return slices.DeleteFunc(outfits, func(outfit *costume3DOutfit) bool { return len(outfit.rows) == 0 }), firstReveal
}

// costume3DOutfitKey is a costume's name, or its group for a costume without
// one, or empty for a costume with neither. In JP the name fixes the designer,
// rarity, type, and publish time, except for the デフォルト base models.
func costume3DOutfitKey(row costume3DRow) string {
	if name, ok := row.fieldValue("name").(string); ok {
		return strings.TrimSpace(name)
	}
	if group := costume3DGroupLookupKey(row.source); group != "" {
		return "\x00" + group
	}
	return ""
}

// publishedAtMillis is when the game's archive shows a costume, or else when it
// was published. JP and EN costumes only have the former.
func (row costume3DRow) publishedAtMillis() (int64, bool) {
	for _, field := range []string{"archivePublishedAt", "publishedAt"} {
		if millis, ok := row.fieldValue(field).(int64); ok {
			return millis, true
		}
	}
	return 0, false
}

func filterCostume3DOutfits(outfits []*costume3DOutfit, nameFilter string, characterIDs []float64, rarities []string) []*costume3DOutfit {
	if nameFilter == "" && len(characterIDs) == 0 && len(rarities) == 0 {
		return outfits
	}
	filtered := make([]*costume3DOutfit, 0, len(outfits))
	for _, outfit := range outfits {
		if outfit.matches(nameFilter, characterIDs, rarities) {
			filtered = append(filtered, outfit)
		}
	}
	return filtered
}

func (outfit *costume3DOutfit) matches(nameFilter string, characterIDs []float64, rarities []string) bool {
	if nameFilter != "" && !strings.Contains(shared.NormalizeComparableText(outfit.firstValue("name")), nameFilter) {
		return false
	}
	if rarity, _ := outfit.firstValue("rarity").(string); len(rarities) > 0 && !slices.Contains(rarities, rarity) {
		return false
	}
	return len(characterIDs) == 0 || slices.ContainsFunc(outfit.rows, func(row costume3DRow) bool {
		characterID, ok := row.fieldValue("characterId").(int64)
		return ok && slices.Contains(characterIDs, float64(characterID))
	})
}

// sortCostume3DOutfits orders outfits like costume lists order costumes:
// missing values last, ties by ID.
func sortCostume3DOutfits(outfits []*costume3DOutfit, field string, descending bool) {
	keys := make(map[*costume3DOutfit]*costume3DSortKey, len(outfits))
	for _, outfit := range outfits {
		key := newCostume3DSortKey(outfit.sortValue(field), outfit.id)
		keys[outfit] = &key
	}
	slices.SortStableFunc(outfits, func(left *costume3DOutfit, right *costume3DOutfit) int {
		switch {
		case keys[left].before(keys[right], descending):
			return -1
		case keys[right].before(keys[left], descending):
			return 1
		default:
			return 0
		}
	})
}

func (outfit *costume3DOutfit) sortValue(field string) any {
	switch field {
	case "id":
		return outfit.id
	case "publishedAt":
		if publishedAt := outfit.publishedAt(); publishedAt != nil {
			return *publishedAt
		}
		return nil
	default:
		return outfit.firstValue(field)
	}
}

// firstValue returns a normalized field of the outfit's lowest-ID costume that
// has it.
func (outfit *costume3DOutfit) firstValue(field string) any {
	for _, row := range outfit.rows {
		if value := row.fieldValue(field); value != nil {
			return value
		}
	}
	return nil
}

// publishedAt is when the outfit first appeared, or nil when no costume in it has
// a real date.
func (outfit *costume3DOutfit) publishedAt() *int64 {
	var earliest *int64
	for _, row := range outfit.rows {
		if millis, ok := row.publishedAtMillis(); ok && millis >= costume3DGameLaunchMillis && (earliest == nil || millis < *earliest) {
			earliest = &millis
		}
	}
	return earliest
}

// thumbnail is the thumbnail of the outfit's lowest-ID body, which is the
// original color: JP's outfits use one body asset for every character. An
// outfit without a body falls back to its first costume with a thumbnail.
func (outfit *costume3DOutfit) thumbnail() *string {
	for _, row := range outfit.rows {
		if row.fieldValue("partType") == any(costume3DPartBody) {
			if name := row.thumbnailName(); name != nil {
				return name
			}
		}
	}
	for _, row := range outfit.rows {
		if name := row.thumbnailName(); name != nil {
			return name
		}
	}
	return nil
}

// thumbnailName is a costume's file under thumbnail/costume. JP and EN name it
// in assetbundleName. TW, KR, and CN leave costumes shared with JP without an
// assetbundleName, and give their own costumes only the number: both are
// cos{number}_{part}, plus _{01,02,03} for the other colors, and a shared
// costume's number is its ID's thousands.
func (row costume3DRow) thumbnailName() *string {
	bundle, _ := row.fieldValue("assetbundleName").(string)
	bundle = strings.TrimSpace(bundle)
	if bundle != "" && strings.Trim(bundle, "0123456789") != "" {
		return &bundle
	}
	part, ok := row.fieldValue("partType").(string)
	if !ok {
		return nil
	}
	number := bundle
	if number == "" {
		number = fmt.Sprintf("%04d", row.id/1000)
	}
	name := "cos" + number + "_" + part
	if colorID, ok := row.fieldValue("colorId").(int64); ok && colorID > 1 {
		name += fmt.Sprintf("_%02d", colorID-1)
	}
	return &name
}

func (outfit *costume3DOutfit) response() shared.Costume3DOutfitResponse {
	characterIDs, colorIDs := []int64{}, []int64{}
	partTypes := []string{}
	for _, row := range outfit.rows {
		if id, ok := row.fieldValue("characterId").(int64); ok {
			characterIDs = appendUnique(characterIDs, id)
		}
		if id, ok := row.fieldValue("colorId").(int64); ok {
			colorIDs = appendUnique(colorIDs, id)
		}
		if part, ok := row.fieldValue("partType").(string); ok {
			partTypes = appendUnique(partTypes, part)
		}
	}
	slices.Sort(characterIDs)
	slices.Sort(colorIDs)
	slices.Sort(partTypes)
	return shared.Costume3DOutfitResponse{
		ID:                       outfit.id,
		Name:                     lookupOptionalString(outfit.firstValue("name")),
		Designer:                 lookupOptionalString(outfit.firstValue("designer")),
		Rarity:                   lookupOptionalString(outfit.firstValue("rarity")),
		Type:                     lookupOptionalString(outfit.firstValue("type")),
		CharacterIDs:             characterIDs,
		ColorIDs:                 colorIDs,
		PartTypes:                partTypes,
		ThumbnailAssetbundleName: outfit.thumbnail(),
		PublishedAt:              outfit.publishedAt(),
	}
}

func appendUnique[T comparable](values []T, value T) []T {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

// costume3DOutfitDetail reads the outfit's full costume records for what the
// list projection leaves out: how to obtain each costume and its color's name.
// TW, KR, and CN name colors in costume3dColors instead of on the costume.
func (handler *LookupHandler) costume3DOutfitDetail(ctx context.Context, region string, outfit *costume3DOutfit) (shared.Costume3DOutfitDetailResponse, error) {
	ids := make([]string, 0, len(outfit.rows))
	for _, row := range outfit.rows {
		ids = append(ids, formatID(row.id))
	}
	records, err := handler.masterDataSync.GetByIDs(ctx, region, costume3dsEntity, ids)
	if err != nil {
		return shared.Costume3DOutfitDetailResponse{}, err
	}

	costumes := make([]shared.Costume3DOutfitCostumeResponse, 0, len(outfit.rows))
	colorNames := map[int64]string{}
	for position, row := range outfit.rows {
		var howToObtain *string
		if position < len(records) && records[position] != nil {
			howToObtain = nonBlankString(records[position]["howToObtain"])
			if colorID, ok := row.fieldValue("colorId").(int64); ok {
				if name := nonBlankString(records[position]["colorName"]); name != nil {
					if _, named := colorNames[colorID]; !named {
						colorNames[colorID] = *name
					}
				}
			}
		}
		costumes = append(costumes, shared.Costume3DOutfitCostumeResponse{
			ID:                       row.id,
			GroupID:                  lookupOptionalInt64(row.fieldValue("groupId")),
			ColorID:                  lookupOptionalInt64(row.fieldValue("colorId")),
			PartType:                 lookupOptionalString(row.fieldValue("partType")),
			CharacterID:              lookupOptionalInt64(row.fieldValue("characterId")),
			ThumbnailAssetbundleName: row.thumbnailName(),
			HowToObtain:              howToObtain,
		})
	}

	summary := outfit.response()
	if err := handler.fillCostume3DColorNames(ctx, region, summary.ColorIDs, colorNames); err != nil {
		return shared.Costume3DOutfitDetailResponse{}, err
	}
	colors := make([]shared.Costume3DOutfitColorResponse, 0, len(summary.ColorIDs))
	for _, colorID := range summary.ColorIDs {
		color := shared.Costume3DOutfitColorResponse{ID: colorID}
		if name, ok := colorNames[colorID]; ok {
			color.Name = &name
		}
		colors = append(colors, color)
	}
	return shared.Costume3DOutfitDetailResponse{Costume3DOutfitResponse: summary, Colors: colors, Costumes: costumes}, nil
}

func (handler *LookupHandler) fillCostume3DColorNames(ctx context.Context, region string, colorIDs []int64, colorNames map[int64]string) error {
	missing := []string{}
	for _, colorID := range colorIDs {
		if _, named := colorNames[colorID]; !named {
			missing = append(missing, strconv.FormatInt(colorID, 10))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	records, err := handler.masterDataSync.GetByIDs(ctx, region, costume3DColorsEntity, missing)
	if err != nil {
		return err
	}
	for _, record := range records {
		if id, ok := lookupInt64(record["id"]); ok {
			if name := nonBlankString(record["name"]); name != nil {
				colorNames[id] = *name
			}
		}
	}
	return nil
}

func nonBlankString(value any) *string {
	if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
		return &text
	}
	return nil
}
