package shared

// MysekaiFixtureGridSizeResponse is a fixture's footprint in MySekai grid
// cells.
type MysekaiFixtureGridSizeResponse struct {
	Width  int64 `json:"width" binding:"required"`
	Depth  int64 `json:"depth" binding:"required"`
	Height int64 `json:"height" binding:"required"`
}

// MysekaiFixtureListItemResponse is one fixture (furniture) in the catalogue.
// Genre and tag IDs resolve through GET /mysekaiFixtures/{region}/filters.
type MysekaiFixtureListItemResponse struct {
	ID                        int64                           `json:"id" binding:"required"`
	Seq                       *int64                          `json:"seq,omitempty"`
	Name                      string                          `json:"name" binding:"required"`
	Pronunciation             *string                         `json:"pronunciation,omitempty"`
	MysekaiFixtureType        *string                         `json:"mysekaiFixtureType,omitempty"`
	MysekaiFixtureMainGenreID *int64                          `json:"mysekaiFixtureMainGenreId,omitempty"`
	MysekaiFixtureSubGenreID  *int64                          `json:"mysekaiFixtureSubGenreId,omitempty"`
	MysekaiSettableLayoutType *string                         `json:"mysekaiSettableLayoutType,omitempty"`
	GridSize                  *MysekaiFixtureGridSizeResponse `json:"gridSize,omitempty"`
	TagIDs                    []int64                         `json:"tagIds" binding:"required"`
	AssetbundleName           *string                         `json:"assetbundleName,omitempty"`
}

type MysekaiFixtureListResponse struct {
	Items      []MysekaiFixtureListItemResponse `json:"items" binding:"required"`
	Pagination PaginationResponse               `json:"pagination" binding:"required"`
}

type MysekaiFixtureGenreResponse struct {
	ID              int64   `json:"id" binding:"required"`
	Name            string  `json:"name" binding:"required"`
	AssetbundleName *string `json:"assetbundleName,omitempty"`
}

type MysekaiFixtureMainGenreResponse struct {
	ID              int64   `json:"id" binding:"required"`
	Name            string  `json:"name" binding:"required"`
	AssetbundleName *string `json:"assetbundleName,omitempty"`
	// SubGenres are the sub-genres fixtures of this main genre use, by ID.
	SubGenres []MysekaiFixtureGenreResponse `json:"subGenres,omitempty"`
}

// MysekaiFixtureTagResponse is a fixture tag. For the game_character type,
// ExternalID is the game character ID; for unit, it is the unit ID.
type MysekaiFixtureTagResponse struct {
	ID                    int64   `json:"id" binding:"required"`
	Name                  string  `json:"name" binding:"required"`
	MysekaiFixtureTagType string  `json:"mysekaiFixtureTagType" binding:"required"`
	ExternalID            *int64  `json:"externalId,omitempty"`
	Pronunciation         *string `json:"pronunciation,omitempty"`
}

// MysekaiFixtureFiltersResponse lists the genres and the series, unit, and
// character tags that at least one fixture of the region uses.
type MysekaiFixtureFiltersResponse struct {
	MainGenres []MysekaiFixtureMainGenreResponse `json:"mainGenres" binding:"required"`
	Tags       []MysekaiFixtureTagResponse       `json:"tags" binding:"required"`
}

type MysekaiFixtureColorResponse struct {
	TextureID *int64  `json:"textureId,omitempty"`
	ColorCode *string `json:"colorCode,omitempty"`
}

type MysekaiMaterialSummaryResponse struct {
	ID                        int64   `json:"id" binding:"required"`
	Name                      string  `json:"name" binding:"required"`
	MysekaiMaterialType       *string `json:"mysekaiMaterialType,omitempty"`
	MysekaiMaterialRarityType *string `json:"mysekaiMaterialRarityType,omitempty"`
	IconAssetbundleName       *string `json:"iconAssetbundleName,omitempty"`
}

type MysekaiMaterialQuantityResponse struct {
	Material MysekaiMaterialSummaryResponse `json:"material" binding:"required"`
	Quantity int64                          `json:"quantity" binding:"required"`
}

// MysekaiBlueprintResponse is the blueprint that crafts a fixture and the
// materials one craft costs, in cost order.
type MysekaiBlueprintResponse struct {
	ID                           int64                             `json:"id" binding:"required"`
	IsEnableSketch               *bool                             `json:"isEnableSketch,omitempty"`
	IsObtainedByConvert          *bool                             `json:"isObtainedByConvert,omitempty"`
	IsAvailableWithoutPossession *bool                             `json:"isAvailableWithoutPossession,omitempty"`
	CraftCountLimit              *int64                            `json:"craftCountLimit,omitempty"`
	MaterialCosts                []MysekaiMaterialQuantityResponse `json:"materialCosts" binding:"required"`
}

// MysekaiFixtureCharacterBonusResponse is the character performance bonus a
// fixture grants.
type MysekaiFixtureCharacterBonusResponse struct {
	BonusRate        *float64 `json:"bonusRate,omitempty"`
	GameCharacterIDs []int64  `json:"gameCharacterIds" binding:"required"`
}

type MysekaiFixtureDetailResponse struct {
	ID                        int64                                 `json:"id" binding:"required"`
	Seq                       *int64                                `json:"seq,omitempty"`
	Name                      string                                `json:"name" binding:"required"`
	Pronunciation             *string                               `json:"pronunciation,omitempty"`
	MysekaiFixtureType        *string                               `json:"mysekaiFixtureType,omitempty"`
	MysekaiFixtureMainGenreID *int64                                `json:"mysekaiFixtureMainGenreId,omitempty"`
	MysekaiFixtureSubGenreID  *int64                                `json:"mysekaiFixtureSubGenreId,omitempty"`
	MysekaiSettableLayoutType *string                               `json:"mysekaiSettableLayoutType,omitempty"`
	GridSize                  *MysekaiFixtureGridSizeResponse       `json:"gridSize,omitempty"`
	TagIDs                    []int64                               `json:"tagIds" binding:"required"`
	AssetbundleName           *string                               `json:"assetbundleName,omitempty"`
	FlavorText                *string                               `json:"flavorText,omitempty"`
	ColorCode                 *string                               `json:"colorCode,omitempty"`
	AnotherColors             []MysekaiFixtureColorResponse         `json:"anotherColors" binding:"required"`
	MysekaiSettableSiteType   *string                               `json:"mysekaiSettableSiteType,omitempty"`
	IsAssembled               *bool                                 `json:"isAssembled,omitempty"`
	IsDisassembled            *bool                                 `json:"isDisassembled,omitempty"`
	FirstPutCost              *int64                                `json:"firstPutCost,omitempty"`
	SecondPutCost             *int64                                `json:"secondPutCost,omitempty"`
	MainGenre                 *MysekaiFixtureGenreResponse          `json:"mainGenre,omitempty"`
	SubGenre                  *MysekaiFixtureGenreResponse          `json:"subGenre,omitempty"`
	Tags                      []MysekaiFixtureTagResponse           `json:"tags" binding:"required"`
	Blueprint                 *MysekaiBlueprintResponse             `json:"blueprint,omitempty"`
	CharacterBonus            *MysekaiFixtureCharacterBonusResponse `json:"characterBonus,omitempty"`
	DisassembleMaterials      []MysekaiMaterialQuantityResponse     `json:"disassembleMaterials" binding:"required"`
}

type MysekaiSiteResponse struct {
	ID   int64  `json:"id" binding:"required"`
	Name string `json:"name" binding:"required"`
}

type MysekaiMaterialResponse struct {
	ID                        int64                 `json:"id" binding:"required"`
	Name                      string                `json:"name" binding:"required"`
	MysekaiMaterialType       *string               `json:"mysekaiMaterialType,omitempty"`
	MysekaiMaterialRarityType *string               `json:"mysekaiMaterialRarityType,omitempty"`
	IconAssetbundleName       *string               `json:"iconAssetbundleName,omitempty"`
	Seq                       *int64                `json:"seq,omitempty"`
	Pronunciation             *string               `json:"pronunciation,omitempty"`
	Description               *string               `json:"description,omitempty"`
	Sites                     []MysekaiSiteResponse `json:"sites" binding:"required"`
	GameCharacterIDs          []int64               `json:"gameCharacterIds" binding:"required"`
}

type MysekaiMaterialListResponse struct {
	Items      []MysekaiMaterialResponse `json:"items" binding:"required"`
	Pagination PaginationResponse        `json:"pagination" binding:"required"`
}

// MysekaiFixtureSummaryResponse is enough of a fixture to show and link it.
type MysekaiFixtureSummaryResponse struct {
	ID                        int64   `json:"id" binding:"required"`
	Name                      string  `json:"name" binding:"required"`
	MysekaiFixtureType        *string `json:"mysekaiFixtureType,omitempty"`
	MysekaiSettableLayoutType *string `json:"mysekaiSettableLayoutType,omitempty"`
	AssetbundleName           *string `json:"assetbundleName,omitempty"`
}

type MysekaiMaterialUseResponse struct {
	Fixture  MysekaiFixtureSummaryResponse `json:"fixture" binding:"required"`
	Quantity int64                         `json:"quantity" binding:"required"`
}

// MysekaiMaterialDetailResponse is a material and the fixtures whose
// blueprints cost it.
type MysekaiMaterialDetailResponse struct {
	ID                        int64                        `json:"id" binding:"required"`
	Name                      string                       `json:"name" binding:"required"`
	MysekaiMaterialType       *string                      `json:"mysekaiMaterialType,omitempty"`
	MysekaiMaterialRarityType *string                      `json:"mysekaiMaterialRarityType,omitempty"`
	IconAssetbundleName       *string                      `json:"iconAssetbundleName,omitempty"`
	Seq                       *int64                       `json:"seq,omitempty"`
	Pronunciation             *string                      `json:"pronunciation,omitempty"`
	Description               *string                      `json:"description,omitempty"`
	Sites                     []MysekaiSiteResponse        `json:"sites" binding:"required"`
	GameCharacterIDs          []int64                      `json:"gameCharacterIds" binding:"required"`
	UsedBy                    []MysekaiMaterialUseResponse `json:"usedBy" binding:"required"`
}

type MysekaiMusicRecordMusicResponse struct {
	ID              int64   `json:"id" binding:"required"`
	Title           string  `json:"title" binding:"required"`
	AssetbundleName *string `json:"assetbundleName,omitempty"`
}

type MysekaiMusicRecordSoundTrackResponse struct {
	ID                        int64   `json:"id" binding:"required"`
	Title                     string  `json:"title" binding:"required"`
	MusicSoundTrackCategoryID *int64  `json:"musicSoundTrackCategoryId,omitempty"`
	AssetbundleName           *string `json:"assetbundleName,omitempty"`
	AssetbundleFileName       *string `json:"assetbundleFileName,omitempty"`
}

// MysekaiMusicRecordResponse is one MySekai music record. Music records carry
// Music and sound-track records carry SoundTrack.
type MysekaiMusicRecordResponse struct {
	ID                    int64                                 `json:"id" binding:"required"`
	MysekaiMusicTrackType string                                `json:"mysekaiMusicTrackType" binding:"required"`
	ExternalID            int64                                 `json:"externalId" binding:"required"`
	Music                 *MysekaiMusicRecordMusicResponse      `json:"music,omitempty"`
	SoundTrack            *MysekaiMusicRecordSoundTrackResponse `json:"soundTrack,omitempty"`
}

type MysekaiMusicRecordListResponse struct {
	Items      []MysekaiMusicRecordResponse `json:"items" binding:"required"`
	Pagination PaginationResponse           `json:"pagination" binding:"required"`
}

type MusicSoundTrackCategoryResponse struct {
	ID              int64   `json:"id" binding:"required"`
	Name            string  `json:"name" binding:"required"`
	AssetbundleName *string `json:"assetbundleName,omitempty"`
}

// MysekaiMusicRecordFiltersResponse lists the sound-track categories that at
// least one sound-track record uses.
type MysekaiMusicRecordFiltersResponse struct {
	SoundTrackCategories []MusicSoundTrackCategoryResponse `json:"soundTrackCategories" binding:"required"`
}

// MysekaiShopCostResponse is one price of a secret shop item.
type MysekaiShopCostResponse struct {
	ResourceType string `json:"resourceType" binding:"required"`
	ResourceID   *int64 `json:"resourceId,omitempty"`
	Quantity     int64  `json:"quantity" binding:"required"`
}

// MysekaiShopResourceResponse is a resource a secret shop item sells: a
// MySekai material or tool. AssetbundleName names its icon: a material's
// iconAssetbundleName or a tool's assetbundleName.
type MysekaiShopResourceResponse struct {
	ResourceType              string  `json:"resourceType" binding:"required"`
	ResourceID                *int64  `json:"resourceId,omitempty"`
	ResourceQuantity          int64   `json:"resourceQuantity" binding:"required"`
	Name                      *string `json:"name,omitempty"`
	Description               *string `json:"description,omitempty"`
	AssetbundleName           *string `json:"assetbundleName,omitempty"`
	MysekaiMaterialType       *string `json:"mysekaiMaterialType,omitempty"`
	MysekaiMaterialRarityType *string `json:"mysekaiMaterialRarityType,omitempty"`
	MysekaiToolType           *string `json:"mysekaiToolType,omitempty"`
}

// MysekaiShopItemResponse is one secret shop item. A limit type of
// limited_per_mysekai_colorful_pass caps purchases at the limit value per
// World Pass period; none means unlimited.
type MysekaiShopItemResponse struct {
	ID                            int64                         `json:"id" binding:"required"`
	Seq                           *int64                        `json:"seq,omitempty"`
	MysekaiShopType               string                        `json:"mysekaiShopType" binding:"required"`
	MysekaiShopExchangeLimitType  *string                       `json:"mysekaiShopExchangeLimitType,omitempty"`
	MysekaiShopExchangeLimitValue *int64                        `json:"mysekaiShopExchangeLimitValue,omitempty"`
	Costs                         []MysekaiShopCostResponse     `json:"costs" binding:"required"`
	Resources                     []MysekaiShopResourceResponse `json:"resources" binding:"required"`
}

type MysekaiShopListResponse struct {
	Items      []MysekaiShopItemResponse `json:"items" binding:"required"`
	Pagination PaginationResponse        `json:"pagination" binding:"required"`
}
