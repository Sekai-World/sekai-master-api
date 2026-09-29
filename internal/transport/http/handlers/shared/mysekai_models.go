package shared

// MysekaiFixtureGridSizeResponse is a fixture's footprint in MySekai grid
// cells.
type MysekaiFixtureGridSizeResponse struct {
	Width  int64 `json:"width"`
	Depth  int64 `json:"depth"`
	Height int64 `json:"height"`
}

// MysekaiFixtureListItemResponse is one fixture (furniture) in the catalogue.
// Genre and tag IDs resolve through GET /mysekaiFixtures/{region}/filters.
type MysekaiFixtureListItemResponse struct {
	ID                        int64                           `json:"id"`
	Seq                       *int64                          `json:"seq,omitempty"`
	Name                      string                          `json:"name"`
	Pronunciation             *string                         `json:"pronunciation,omitempty"`
	MysekaiFixtureType        *string                         `json:"mysekaiFixtureType,omitempty"`
	MysekaiFixtureMainGenreID *int64                          `json:"mysekaiFixtureMainGenreId,omitempty"`
	MysekaiFixtureSubGenreID  *int64                          `json:"mysekaiFixtureSubGenreId,omitempty"`
	MysekaiSettableLayoutType *string                         `json:"mysekaiSettableLayoutType,omitempty"`
	GridSize                  *MysekaiFixtureGridSizeResponse `json:"gridSize,omitempty"`
	TagIDs                    []int64                         `json:"tagIds"`
	AssetbundleName           *string                         `json:"assetbundleName,omitempty"`
}

type MysekaiFixtureListResponse struct {
	Items      []MysekaiFixtureListItemResponse `json:"items"`
	Pagination PaginationResponse               `json:"pagination"`
}

type MysekaiFixtureGenreResponse struct {
	ID              int64   `json:"id"`
	Name            string  `json:"name"`
	AssetbundleName *string `json:"assetbundleName,omitempty"`
}

type MysekaiFixtureMainGenreResponse struct {
	ID              int64   `json:"id"`
	Name            string  `json:"name"`
	AssetbundleName *string `json:"assetbundleName,omitempty"`
	// SubGenres are the sub-genres fixtures of this main genre use, by ID.
	SubGenres []MysekaiFixtureGenreResponse `json:"subGenres,omitempty"`
}

// MysekaiFixtureTagResponse is a fixture tag. For the game_character type,
// ExternalID is the game character ID; for unit, it is the unit ID.
type MysekaiFixtureTagResponse struct {
	ID                    int64   `json:"id"`
	Name                  string  `json:"name"`
	MysekaiFixtureTagType string  `json:"mysekaiFixtureTagType"`
	ExternalID            *int64  `json:"externalId,omitempty"`
	Pronunciation         *string `json:"pronunciation,omitempty"`
}

// MysekaiFixtureFiltersResponse lists the genres and the series, unit, and
// character tags that at least one fixture of the region uses.
type MysekaiFixtureFiltersResponse struct {
	MainGenres []MysekaiFixtureMainGenreResponse `json:"mainGenres"`
	Tags       []MysekaiFixtureTagResponse       `json:"tags"`
}

type MysekaiFixtureColorResponse struct {
	TextureID *int64  `json:"textureId,omitempty"`
	ColorCode *string `json:"colorCode,omitempty"`
}

type MysekaiMaterialSummaryResponse struct {
	ID                        int64   `json:"id"`
	Name                      string  `json:"name"`
	MysekaiMaterialType       *string `json:"mysekaiMaterialType,omitempty"`
	MysekaiMaterialRarityType *string `json:"mysekaiMaterialRarityType,omitempty"`
	IconAssetbundleName       *string `json:"iconAssetbundleName,omitempty"`
}

type MysekaiMaterialQuantityResponse struct {
	Material MysekaiMaterialSummaryResponse `json:"material"`
	Quantity int64                          `json:"quantity"`
}

// MysekaiBlueprintResponse is the blueprint that crafts a fixture and the
// materials one craft costs, in cost order.
type MysekaiBlueprintResponse struct {
	ID                           int64                             `json:"id"`
	IsEnableSketch               *bool                             `json:"isEnableSketch,omitempty"`
	IsObtainedByConvert          *bool                             `json:"isObtainedByConvert,omitempty"`
	IsAvailableWithoutPossession *bool                             `json:"isAvailableWithoutPossession,omitempty"`
	CraftCountLimit              *int64                            `json:"craftCountLimit,omitempty"`
	MaterialCosts                []MysekaiMaterialQuantityResponse `json:"materialCosts"`
}

// MysekaiFixtureCharacterBonusResponse is the character performance bonus a
// fixture grants.
type MysekaiFixtureCharacterBonusResponse struct {
	BonusRate        *float64 `json:"bonusRate,omitempty"`
	GameCharacterIDs []int64  `json:"gameCharacterIds"`
}

type MysekaiFixtureDetailResponse struct {
	ID                        int64                                 `json:"id"`
	Seq                       *int64                                `json:"seq,omitempty"`
	Name                      string                                `json:"name"`
	Pronunciation             *string                               `json:"pronunciation,omitempty"`
	MysekaiFixtureType        *string                               `json:"mysekaiFixtureType,omitempty"`
	MysekaiFixtureMainGenreID *int64                                `json:"mysekaiFixtureMainGenreId,omitempty"`
	MysekaiFixtureSubGenreID  *int64                                `json:"mysekaiFixtureSubGenreId,omitempty"`
	MysekaiSettableLayoutType *string                               `json:"mysekaiSettableLayoutType,omitempty"`
	GridSize                  *MysekaiFixtureGridSizeResponse       `json:"gridSize,omitempty"`
	TagIDs                    []int64                               `json:"tagIds"`
	AssetbundleName           *string                               `json:"assetbundleName,omitempty"`
	FlavorText                *string                               `json:"flavorText,omitempty"`
	ColorCode                 *string                               `json:"colorCode,omitempty"`
	AnotherColors             []MysekaiFixtureColorResponse         `json:"anotherColors"`
	MysekaiSettableSiteType   *string                               `json:"mysekaiSettableSiteType,omitempty"`
	IsAssembled               *bool                                 `json:"isAssembled,omitempty"`
	IsDisassembled            *bool                                 `json:"isDisassembled,omitempty"`
	FirstPutCost              *int64                                `json:"firstPutCost,omitempty"`
	SecondPutCost             *int64                                `json:"secondPutCost,omitempty"`
	MainGenre                 *MysekaiFixtureGenreResponse          `json:"mainGenre,omitempty"`
	SubGenre                  *MysekaiFixtureGenreResponse          `json:"subGenre,omitempty"`
	Tags                      []MysekaiFixtureTagResponse           `json:"tags"`
	Blueprint                 *MysekaiBlueprintResponse             `json:"blueprint,omitempty"`
	CharacterBonus            *MysekaiFixtureCharacterBonusResponse `json:"characterBonus,omitempty"`
	DisassembleMaterials      []MysekaiMaterialQuantityResponse     `json:"disassembleMaterials"`
}

type MysekaiSiteResponse struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type MysekaiMaterialResponse struct {
	ID                        int64                 `json:"id"`
	Name                      string                `json:"name"`
	MysekaiMaterialType       *string               `json:"mysekaiMaterialType,omitempty"`
	MysekaiMaterialRarityType *string               `json:"mysekaiMaterialRarityType,omitempty"`
	IconAssetbundleName       *string               `json:"iconAssetbundleName,omitempty"`
	Seq                       *int64                `json:"seq,omitempty"`
	Pronunciation             *string               `json:"pronunciation,omitempty"`
	Description               *string               `json:"description,omitempty"`
	Sites                     []MysekaiSiteResponse `json:"sites"`
	GameCharacterIDs          []int64               `json:"gameCharacterIds"`
}

type MysekaiMaterialListResponse struct {
	Items      []MysekaiMaterialResponse `json:"items"`
	Pagination PaginationResponse        `json:"pagination"`
}

// MysekaiFixtureSummaryResponse is enough of a fixture to show and link it.
type MysekaiFixtureSummaryResponse struct {
	ID                        int64   `json:"id"`
	Name                      string  `json:"name"`
	MysekaiFixtureType        *string `json:"mysekaiFixtureType,omitempty"`
	MysekaiSettableLayoutType *string `json:"mysekaiSettableLayoutType,omitempty"`
	AssetbundleName           *string `json:"assetbundleName,omitempty"`
}

type MysekaiMaterialUseResponse struct {
	Fixture  MysekaiFixtureSummaryResponse `json:"fixture"`
	Quantity int64                         `json:"quantity"`
}

// MysekaiMaterialDetailResponse is a material and the fixtures whose
// blueprints cost it.
type MysekaiMaterialDetailResponse struct {
	ID                        int64                        `json:"id"`
	Name                      string                       `json:"name"`
	MysekaiMaterialType       *string                      `json:"mysekaiMaterialType,omitempty"`
	MysekaiMaterialRarityType *string                      `json:"mysekaiMaterialRarityType,omitempty"`
	IconAssetbundleName       *string                      `json:"iconAssetbundleName,omitempty"`
	Seq                       *int64                       `json:"seq,omitempty"`
	Pronunciation             *string                      `json:"pronunciation,omitempty"`
	Description               *string                      `json:"description,omitempty"`
	Sites                     []MysekaiSiteResponse        `json:"sites"`
	GameCharacterIDs          []int64                      `json:"gameCharacterIds"`
	UsedBy                    []MysekaiMaterialUseResponse `json:"usedBy"`
}

type MysekaiMusicRecordMusicResponse struct {
	ID              int64   `json:"id"`
	Title           string  `json:"title"`
	AssetbundleName *string `json:"assetbundleName,omitempty"`
}

type MysekaiMusicRecordSoundTrackResponse struct {
	ID                        int64   `json:"id"`
	Title                     string  `json:"title"`
	MusicSoundTrackCategoryID *int64  `json:"musicSoundTrackCategoryId,omitempty"`
	AssetbundleName           *string `json:"assetbundleName,omitempty"`
	AssetbundleFileName       *string `json:"assetbundleFileName,omitempty"`
}

// MysekaiMusicRecordResponse is one MySekai music record. Music records carry
// Music and sound-track records carry SoundTrack.
type MysekaiMusicRecordResponse struct {
	ID                    int64                                 `json:"id"`
	MysekaiMusicTrackType string                                `json:"mysekaiMusicTrackType"`
	ExternalID            int64                                 `json:"externalId"`
	Music                 *MysekaiMusicRecordMusicResponse      `json:"music,omitempty"`
	SoundTrack            *MysekaiMusicRecordSoundTrackResponse `json:"soundTrack,omitempty"`
}

type MysekaiMusicRecordListResponse struct {
	Items      []MysekaiMusicRecordResponse `json:"items"`
	Pagination PaginationResponse           `json:"pagination"`
}

type MusicSoundTrackCategoryResponse struct {
	ID              int64   `json:"id"`
	Name            string  `json:"name"`
	AssetbundleName *string `json:"assetbundleName,omitempty"`
}

// MysekaiMusicRecordFiltersResponse lists the sound-track categories that at
// least one sound-track record uses.
type MysekaiMusicRecordFiltersResponse struct {
	SoundTrackCategories []MusicSoundTrackCategoryResponse `json:"soundTrackCategories"`
}
