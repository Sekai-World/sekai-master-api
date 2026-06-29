package shared

import "sekai-master-api/internal/domain/masterdata"

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type HealthResponse struct {
	Status   string `json:"status"`
	Database string `json:"database"`
}

type RegionAvailabilityResponse struct {
	Regions []string `json:"regions"`
}

type MasterDataVersionsResponse struct {
	AppVersion   string `json:"appVersion,omitempty" example:"3.2.1"`
	DataVersion  string `json:"dataVersion,omitempty" example:"3.2.1.10"`
	AssetVersion string `json:"assetVersion,omitempty" example:"3.2.1.10"`
	CdnVersion   *int   `json:"cdnVersion,omitempty" example:"2"`
}

type MasterDataVersionsByRegionResponse map[string]MasterDataVersionsResponse

type GitHubWebhookResponse struct {
	Status string `json:"status" example:"accepted"`
	Reason string `json:"reason,omitempty" example:"unsupported_event"`
	Region string `json:"region,omitempty" example:"jp"`
}

type GenericObjectResponse map[string]any

type GenericItemsResponse struct {
	Items []any `json:"items"`
}

type RecordItemsResponse struct {
	Items []map[string]any `json:"items"`
}

type PaginationResponse struct {
	Page       int  `json:"page"`
	PageSize   int  `json:"page_size"`
	Total      int  `json:"total"`
	TotalPages int  `json:"total_pages"`
	HasNext    bool `json:"has_next"`
}

type CardSupplyResponse struct {
	ID              int    `json:"id,omitempty"`
	CardSupplyType  string `json:"cardSupplyType,omitempty"`
	AssetbundleName string `json:"assetbundleName,omitempty"`
}

type SkillEffectDetailResponse struct {
	ID                      int     `json:"id,omitempty"`
	Level                   int     `json:"level,omitempty"`
	ActivateEffectDuration  float64 `json:"activateEffectDuration,omitempty"`
	ActivateEffectValueType string  `json:"activateEffectValueType,omitempty"`
	ActivateEffectValue     int     `json:"activateEffectValue,omitempty"`
	ActivateEffectValue2    int     `json:"activateEffectValue2,omitempty"`
}

type SkillEnhanceConditionResponse struct {
	ID   int    `json:"id,omitempty"`
	Seq  int    `json:"seq,omitempty"`
	Unit string `json:"unit,omitempty"`
}

type SkillEnhanceResponse struct {
	ID                      int                            `json:"id,omitempty"`
	SkillEnhanceType        string                         `json:"skillEnhanceType,omitempty"`
	ActivateEffectValueType string                         `json:"activateEffectValueType,omitempty"`
	ActivateEffectValue     int                            `json:"activateEffectValue,omitempty"`
	SkillEnhanceCondition   *SkillEnhanceConditionResponse `json:"skillEnhanceCondition,omitempty"`
}

type SkillEffectResponse struct {
	ID                        int                         `json:"id,omitempty"`
	SkillEffectType           string                      `json:"skillEffectType,omitempty"`
	ActivateNotesJudgmentType string                      `json:"activateNotesJudgmentType,omitempty"`
	ActivateLife              int                         `json:"activateLife,omitempty"`
	ActivateUnitCount         int                         `json:"activateUnitCount,omitempty"`
	ActivateCharacterRank     int                         `json:"activateCharacterRank,omitempty"`
	ConditionType             string                      `json:"conditionType,omitempty"`
	SkillEnhance              *SkillEnhanceResponse       `json:"skillEnhance,omitempty"`
	SkillEffectDetails        []SkillEffectDetailResponse `json:"skillEffectDetails,omitempty"`
}

type SkillResponse struct {
	ID                    int                   `json:"id,omitempty"`
	ShortDescription      string                `json:"shortDescription,omitempty"`
	Description           string                `json:"description,omitempty"`
	DescriptionSpriteName string                `json:"descriptionSpriteName,omitempty"`
	SkillFilterID         int                   `json:"skillFilterId,omitempty"`
	SkillEffects          []SkillEffectResponse `json:"skillEffects,omitempty"`
}

type CharacterResponse struct {
	ID               int     `json:"id,omitempty"`
	Seq              int     `json:"seq,omitempty"`
	ResourceID       int     `json:"resourceId,omitempty"`
	FirstName        string  `json:"firstName,omitempty"`
	GivenName        string  `json:"givenName,omitempty"`
	FirstNameRuby    string  `json:"firstNameRuby,omitempty"`
	GivenNameRuby    string  `json:"givenNameRuby,omitempty"`
	FirstNameEnglish string  `json:"firstNameEnglish,omitempty"`
	GivenNameEnglish string  `json:"givenNameEnglish,omitempty"`
	Gender           string  `json:"gender,omitempty"`
	Height           float64 `json:"height,omitempty"`
	Unit             string  `json:"unit,omitempty"`
	SupportUnitType  string  `json:"supportUnitType,omitempty"`
}

type CardRarityResponse struct {
	CardRarityType   string `json:"cardRarityType,omitempty"`
	Seq              int    `json:"seq,omitempty"`
	MaxLevel         int    `json:"maxLevel,omitempty"`
	TrainingMaxLevel int    `json:"trainingMaxLevel,omitempty"`
	MaxSkillLevel    int    `json:"maxSkillLevel,omitempty"`
}

type CardParameterResponse struct {
	ID                int    `json:"id,omitempty"`
	CardID            int    `json:"cardId,omitempty"`
	CardLevel         int    `json:"cardLevel,omitempty"`
	CardParameterType string `json:"cardParameterType,omitempty"`
	Power             int    `json:"power,omitempty"`
}

type CardObjectResponse struct {
	ID                           any                `json:"id,omitempty"`
	Seq                          any                `json:"seq,omitempty"`
	Attr                         any                `json:"attr,omitempty"`
	SupportUnit                  any                `json:"supportUnit,omitempty"`
	CardSkillName                any                `json:"cardSkillName,omitempty"`
	Prefix                       any                `json:"prefix,omitempty"`
	AssetbundleName              any                `json:"assetbundleName,omitempty"`
	GachaPhrase                  any                `json:"gachaPhrase,omitempty"`
	FlavorText                   any                `json:"flavorText,omitempty"`
	ReleaseAt                    any                `json:"releaseAt,omitempty"`
	ArchivePublishedAt           any                `json:"archivePublishedAt,omitempty"`
	InitialSpecialTrainingStatus any                `json:"initialSpecialTrainingStatus,omitempty"`
	CardSupply                   CardSupplyResponse `json:"cardSupply,omitempty"`
	Skill                        SkillResponse      `json:"skill,omitempty"`
	Character                    CharacterResponse  `json:"character,omitempty"`
	CardRarity                   CardRarityResponse `json:"cardRarity,omitempty"`

	SpecialTrainingPower1BonusFixed any                     `json:"specialTrainingPower1BonusFixed,omitempty"`
	SpecialTrainingPower2BonusFixed any                     `json:"specialTrainingPower2BonusFixed,omitempty"`
	SpecialTrainingPower3BonusFixed any                     `json:"specialTrainingPower3BonusFixed,omitempty"`
	CardParameters                  []CardParameterResponse `json:"cardParameters,omitempty"`
}

type CardItemsResponse struct {
	Items []CardObjectResponse `json:"items"`
}

type CardParamsResponse struct {
	ID                              any                     `json:"id,omitempty"`
	SpecialTrainingPower1BonusFixed any                     `json:"specialTrainingPower1BonusFixed,omitempty"`
	SpecialTrainingPower2BonusFixed any                     `json:"specialTrainingPower2BonusFixed,omitempty"`
	SpecialTrainingPower3BonusFixed any                     `json:"specialTrainingPower3BonusFixed,omitempty"`
	CardParameters                  []CardParameterResponse `json:"cardParameters,omitempty"`
}

type CardEpisodeResponse struct {
	ID               any                       `json:"id,omitempty"`
	CardID           any                       `json:"cardId,omitempty"`
	EpisodeNo        any                       `json:"episodeNo,omitempty"`
	ReleaseCondition *ReleaseConditionResponse `json:"releaseCondition,omitempty"`
}

type CardEpisodesResponse struct {
	Items []CardEpisodeResponse `json:"items"`
}

type CardEventResponse struct {
	EventID           any                       `json:"eventId,omitempty"`
	CardID            any                       `json:"cardId,omitempty"`
	BonusRate         any                       `json:"bonusRate,omitempty"`
	FinalBonusRateMin any                       `json:"finalBonusRateMin,omitempty"`
	FinalBonusRateMax any                       `json:"finalBonusRateMax,omitempty"`
	ReleaseCondition  *ReleaseConditionResponse `json:"releaseCondition,omitempty"`
	Event             map[string]any            `json:"event,omitempty"`
}

type CardEventsResponse struct {
	Items []CardEventResponse `json:"items"`
}

type GachaBannerResponse struct {
	ID              any `json:"id,omitempty"`
	Name            any `json:"name,omitempty"`
	AssetbundleName any `json:"assetbundleName,omitempty"`
	StartAt         any `json:"startAt,omitempty"`
}

type CardGachaResponse struct {
	Gachas []GachaBannerResponse `json:"gachas"`
}

type CardDetailResponse struct {
	Card     CardObjectResponse   `json:"card"`
	Params   CardParamsResponse   `json:"params"`
	Episodes CardEpisodesResponse `json:"episodes"`
	Events   CardEventsResponse   `json:"events"`
	Gachas   CardGachaResponse    `json:"gachas"`
}

type GachaPickupResponse struct {
	CardID any `json:"cardId,omitempty"`
	Weight any `json:"weight,omitempty"`
}

type GachaCardRarityRateResponse struct {
	ID             any     `json:"id,omitempty"`
	CardRarityType string  `json:"cardRarityType,omitempty"`
	Rate           float64 `json:"rate,omitempty"`
	LotteryType    string  `json:"lotteryType,omitempty"`
}

type GachaBehaviorResponse struct {
	ID                   any    `json:"id,omitempty"`
	GachaBehaviorType    string `json:"gachaBehaviorType,omitempty"`
	GachaSpinnableType   string `json:"gachaSpinnableType,omitempty"`
	CostResourceType     any    `json:"costResourceType,omitempty"`
	CostResourceQuantity any    `json:"costResourceQuantity,omitempty"`
	CostResourceID       any    `json:"costResourceId,omitempty"`
	ResourceCategory     string `json:"resourceCategory,omitempty"`
	SpinCount            int    `json:"spinCount,omitempty"`
	ExecuteLimit         int    `json:"executeLimit,omitempty"`
	Priority             int    `json:"priority,omitempty"`
	GroupID              int    `json:"groupId,omitempty"`
}

type GachaDetailSubResponse struct {
	ID      any `json:"id,omitempty"`
	GachaID any `json:"gachaId,omitempty"`
	CardID  any `json:"cardId,omitempty"`
	Weight  any `json:"weight,omitempty"`
	IsWish  any `json:"isWish,omitempty"`
}

type GachaInformationResponse struct {
	Summary     any `json:"summary,omitempty"`
	Description any `json:"description,omitempty"`
}

type GachaObjectResponse struct {
	ID                     any                           `json:"id,omitempty"`
	GachaType              any                           `json:"gachaType,omitempty"`
	Name                   any                           `json:"name,omitempty"`
	AssetbundleName        any                           `json:"assetbundleName,omitempty"`
	Summary                any                           `json:"summary,omitempty"`
	StartAt                any                           `json:"startAt,omitempty"`
	EndAt                  any                           `json:"endAt,omitempty"`
	CostResourceType       any                           `json:"costResourceType,omitempty"`
	CostResourceID         any                           `json:"costResourceId,omitempty"`
	CostCount              any                           `json:"costCount,omitempty"`
	GachaPickups           []GachaPickupResponse         `json:"gachaPickups,omitempty"`
	GachaCardRarityRates   []GachaCardRarityRateResponse `json:"gachaCardRarityRates,omitempty"`
	GachaBehaviors         []GachaBehaviorResponse       `json:"gachaBehaviors,omitempty"`
	GachaDetails           []GachaDetailSubResponse      `json:"gachaDetails,omitempty"`
	GachaInformation       *GachaInformationResponse     `json:"gachaInformation,omitempty"`
	GachaCeilItemID        any                           `json:"gachaCeilItemId,omitempty"`
	WishFixedSelectCount   any                           `json:"wishFixedSelectCount,omitempty"`
	WishLimitedSelectCount any                           `json:"wishLimitedSelectCount,omitempty"`
	WishSelectCount        any                           `json:"wishSelectCount,omitempty"`
	IsShowPeriod           any                           `json:"isShowPeriod,omitempty"`
}

type GachaListItemResponse struct {
	ID              any `json:"id,omitempty"`
	GachaType       any `json:"gachaType,omitempty"`
	Name            any `json:"name,omitempty"`
	AssetbundleName any `json:"assetbundleName,omitempty"`
	StartAt         any `json:"startAt,omitempty"`
	EndAt           any `json:"endAt,omitempty"`
}

type GachaListResponse struct {
	Items      []GachaListItemResponse `json:"items"`
	Pagination PaginationResponse      `json:"pagination"`
}

type CardPagination struct {
	Page       int  `json:"page"`
	PageSize   int  `json:"page_size"`
	Total      int  `json:"total"`
	TotalPages int  `json:"total_pages"`
	HasNext    bool `json:"has_next"`
}

type CardListResponse struct {
	Items      []CardObjectResponse `json:"items"`
	Pagination CardPagination       `json:"pagination"`
}

type MusicArtistResponse struct {
	ID   any `json:"id,omitempty"`
	Name any `json:"name,omitempty"`
}

type LiveStageResponse struct {
	ID   any `json:"id,omitempty"`
	Name any `json:"name,omitempty"`
}

type MusicDifficultyResponse struct {
	MusicDifficulty  any                       `json:"musicDifficulty,omitempty"`
	PlayLevel        any                       `json:"playLevel,omitempty"`
	ReleaseCondition *ReleaseConditionResponse `json:"releaseCondition,omitempty"`
}

type MusicDifficultyDetailResponse struct {
	ID               any                       `json:"id,omitempty"`
	MusicID          any                       `json:"musicId,omitempty"`
	MusicDifficulty  any                       `json:"musicDifficulty,omitempty"`
	PlayLevel        any                       `json:"playLevel,omitempty"`
	TotalNoteCount   any                       `json:"totalNoteCount,omitempty"`
	ReleaseCondition *ReleaseConditionResponse `json:"releaseCondition,omitempty"`
}

type MusicDifficultiesResponse struct {
	Items []MusicDifficultyDetailResponse `json:"items"`
}

type MusicObjectResponse struct {
	ID                 any                       `json:"id,omitempty"`
	Seq                any                       `json:"seq,omitempty"`
	Title              any                       `json:"title,omitempty"`
	Pronunciation      any                       `json:"pronunciation,omitempty"`
	Lyricist           any                       `json:"lyricist,omitempty"`
	Composer           any                       `json:"composer,omitempty"`
	Arranger           any                       `json:"arranger,omitempty"`
	AssetbundleName    any                       `json:"assetbundleName,omitempty"`
	PublishedAt        any                       `json:"publishedAt,omitempty"`
	FillerSec          any                       `json:"fillerSec,omitempty"`
	DancerCount        any                       `json:"dancerCount,omitempty"`
	SelfDancerPosition any                       `json:"selfDancerPosition,omitempty"`
	CreatorArtist      *MusicArtistResponse      `json:"creatorArtist,omitempty"`
	LiveStage          *LiveStageResponse        `json:"liveStage,omitempty"`
	ReleaseCondition   *ReleaseConditionResponse `json:"releaseCondition,omitempty"`
}

type MusicListItemResponse struct {
	ID                 any                       `json:"id,omitempty"`
	Seq                any                       `json:"seq,omitempty"`
	Title              any                       `json:"title,omitempty"`
	Pronunciation      any                       `json:"pronunciation,omitempty"`
	Lyricist           any                       `json:"lyricist,omitempty"`
	Composer           any                       `json:"composer,omitempty"`
	Arranger           any                       `json:"arranger,omitempty"`
	AssetbundleName    any                       `json:"assetbundleName,omitempty"`
	PublishedAt        any                       `json:"publishedAt,omitempty"`
	FillerSec          any                       `json:"fillerSec,omitempty"`
	DancerCount        any                       `json:"dancerCount,omitempty"`
	SelfDancerPosition any                       `json:"selfDancerPosition,omitempty"`
	CreatorArtist      *MusicArtistResponse      `json:"creatorArtist,omitempty"`
	LiveStage          *LiveStageResponse        `json:"liveStage,omitempty"`
	ReleaseCondition   *ReleaseConditionResponse `json:"releaseCondition,omitempty"`
	Difficulties       []MusicDifficultyResponse `json:"difficulties,omitempty"`
	Tags               []string                  `json:"tags,omitempty"`
}

type MusicListResponse struct {
	Items      []MusicListItemResponse `json:"items"`
	Pagination PaginationResponse      `json:"pagination"`
}

type UnitProfileObjectResponse struct {
	ID        any `json:"id,omitempty"`
	Unit      any `json:"unit,omitempty"`
	UnitName  any `json:"unitName,omitempty"`
	ColorCode any `json:"colorCode,omitempty"`
}

type UnitProfileListResponse struct {
	Items      []UnitProfileObjectResponse `json:"items"`
	Pagination PaginationResponse          `json:"pagination"`
}

type GameCharacterUnitObjectResponse struct {
	ID              any `json:"id,omitempty"`
	GameCharacterID any `json:"gameCharacterId,omitempty"`
	Unit            any `json:"unit,omitempty"`
	ColorCode       any `json:"colorCode,omitempty"`
}

type GameCharacterUnitListResponse struct {
	Items      []GameCharacterUnitObjectResponse `json:"items"`
	Pagination PaginationResponse                `json:"pagination"`
}

type GameCharacterObjectResponse struct {
	ID        any `json:"id,omitempty"`
	Seq       any `json:"seq,omitempty"`
	FirstName any `json:"firstName,omitempty"`
	GivenName any `json:"givenName,omitempty"`
	Unit      any `json:"unit,omitempty"`
	Height    any `json:"height,omitempty"`
}

type GameCharacterListResponse struct {
	Items      []GameCharacterObjectResponse `json:"items"`
	Pagination PaginationResponse            `json:"pagination"`
}

type EventUnitResponse struct {
	Unit      any `json:"unit,omitempty"`
	UnitName  any `json:"unitName,omitempty"`
	ColorCode any `json:"colorCode,omitempty"`
}

type EventVirtualLiveResponse struct {
	AssetbundleName any `json:"assetbundleName,omitempty"`
	EndAt           any `json:"endAt,omitempty"`
	ID              any `json:"id,omitempty"`
	Name            any `json:"name,omitempty"`
	StartAt         any `json:"startAt,omitempty"`
	VirtualLiveType any `json:"virtualLiveType,omitempty"`
}

type EventBannerGameCharacterResponse struct {
	GameCharacterUnitID any `json:"gameCharacterUnitId,omitempty"`
	GameCharacterID     any `json:"gameCharacterId,omitempty"`
	Unit                any `json:"unit,omitempty"`
	ColorCode           any `json:"colorCode,omitempty"`
	FirstName           any `json:"firstName,omitempty"`
	GivenName           any `json:"givenName,omitempty"`
}

type EventObjectResponse struct {
	ID                  any                               `json:"id,omitempty"`
	EventType           any                               `json:"eventType,omitempty"`
	Name                any                               `json:"name,omitempty"`
	AssetbundleName     any                               `json:"assetbundleName,omitempty"`
	BgmAssetbundleName  any                               `json:"bgmAssetbundleName,omitempty"`
	Unit                *EventUnitResponse                `json:"unit,omitempty"`
	BannerGameCharacter *EventBannerGameCharacterResponse `json:"bannerGameCharacter,omitempty"`
	StartAt             any                               `json:"startAt,omitempty"`
	AggregateAt         any                               `json:"aggregateAt,omitempty"`
	ClosedAt            any                               `json:"closedAt,omitempty"`
	EventBreakTimeID    any                               `json:"eventBreakTimeId,omitempty"`
	EventPointIcon      any                               `json:"eventPointIcon,omitempty"`
	VirtualLive         *EventVirtualLiveResponse         `json:"virtualLive,omitempty"`
}

type CurrentEventResponse struct {
	ID              any `json:"id,omitempty"`
	Name            any `json:"name,omitempty"`
	StartAt         any `json:"startAt,omitempty"`
	AggregateAt     any `json:"aggregateAt,omitempty"`
	AssetbundleName any `json:"assetbundleName,omitempty"`
	ClosedAt        any `json:"closedAt,omitempty"`
	EventType       any `json:"eventType,omitempty"`
	Unit            any `json:"unit,omitempty"`
}

type EventListItemResponse struct {
	ID                         any `json:"id,omitempty"`
	EventType                  any `json:"eventType,omitempty"`
	Name                       any `json:"name,omitempty"`
	AssetbundleName            any `json:"assetbundleName,omitempty"`
	Unit                       any `json:"unit,omitempty"`
	BannerGameCharacterID      any `json:"bannerGameCharacterId,omitempty"`
	StartAt                    any `json:"startAt,omitempty"`
	AggregateAt                any `json:"aggregateAt,omitempty"`
	ClosedAt                   any `json:"closedAt,omitempty"`
	IsCountLeaderCharacterPlay any `json:"isCountLeaderCharacterPlay,omitempty"`
}

type EventListResponse struct {
	Items      []EventListItemResponse `json:"items"`
	Pagination PaginationResponse      `json:"pagination"`
}

type ReleaseConditionResponse struct {
	ID                   int    `json:"id,omitempty"`
	ReleaseConditionType string `json:"releaseConditionType,omitempty"`
	Sentence             string `json:"sentence,omitempty"`
}

type EventCardBonusLimitResponse struct {
	EventID          int                       `json:"eventId,omitempty"`
	MemberCountLimit int                       `json:"memberCountLimit,omitempty"`
	ReleaseCondition *ReleaseConditionResponse `json:"releaseCondition,omitempty"`
}

type EventDeckBonusResponse struct {
	EventID             int                       `json:"eventId,omitempty"`
	GameCharacterUnitID int                       `json:"gameCharacterUnitId,omitempty"`
	CardAttr            string                    `json:"cardAttr,omitempty"`
	BonusRate           int                       `json:"bonusRate,omitempty"`
	ReleaseCondition    *ReleaseConditionResponse `json:"releaseCondition,omitempty"`
}

type EventHonorBonusResponse struct {
	EventID          int                       `json:"eventId,omitempty"`
	HonorID          int                       `json:"honorId,omitempty"`
	BonusRate        int                       `json:"bonusRate,omitempty"`
	ReleaseCondition *ReleaseConditionResponse `json:"releaseCondition,omitempty"`
}

type EventMysekaiFixtureGameCharacterPerformanceBonusLimitResponse struct {
	EventID          int                       `json:"eventId,omitempty"`
	BonusRateLimit   int                       `json:"bonusRateLimit,omitempty"`
	ReleaseCondition *ReleaseConditionResponse `json:"releaseCondition,omitempty"`
}

type EventRarityBonusRateResponse struct {
	CardRarityType string `json:"cardRarityType,omitempty"`
	MasterRank     int    `json:"masterRank,omitempty"`
	BonusRate      int    `json:"bonusRate,omitempty"`
}

type EventRewardRangeResponse struct {
	ID                  int                          `json:"id,omitempty"`
	EventID             int                          `json:"eventId,omitempty"`
	FromRank            int                          `json:"fromRank,omitempty"`
	ToRank              int                          `json:"toRank,omitempty"`
	IsToRankBorder      bool                         `json:"isToRankBorder,omitempty"`
	EventRankingRewards []EventRankingRewardResponse `json:"eventRankingRewards,omitempty"`
}

type EventRankingRewardResponse struct {
	ID                        int    `json:"id,omitempty"`
	EventRankingRewardRangeID int    `json:"eventRankingRewardRangeId,omitempty"`
	Seq                       int    `json:"seq,omitempty"`
	ResourceBoxID             int    `json:"resourceBoxId,omitempty"`
	RewardConditionType       string `json:"rewardConditionType,omitempty"`
	ConditionValue            int    `json:"conditionValue,omitempty"`
}

type EventRewardsResponse struct {
	Items []EventRewardRangeResponse `json:"items"`
}

type EventMusicResponse struct {
	EventID          int                       `json:"eventId,omitempty"`
	MusicID          int                       `json:"musicId,omitempty"`
	Seq              int                       `json:"seq,omitempty"`
	ReleaseCondition *ReleaseConditionResponse `json:"releaseCondition,omitempty"`
}

type EventMusicsResponse struct {
	Items []EventMusicResponse `json:"items"`
}

type EventCardResponse struct {
	EventID          int                       `json:"eventId,omitempty"`
	CardID           int                       `json:"cardId,omitempty"`
	BonusRate        int                       `json:"bonusRate,omitempty"`
	ReleaseCondition *ReleaseConditionResponse `json:"releaseCondition,omitempty"`
}

type EventCardsResponse struct {
	Items []EventCardResponse `json:"items"`
}

type EventBonusesResponse struct {
	EventCardBonusLimits                                   []EventCardBonusLimitResponse                                   `json:"eventCardBonusLimits"`
	EventDeckBonuses                                       []EventDeckBonusResponse                                        `json:"eventDeckBonuses"`
	EventHonorBonuses                                      []EventHonorBonusResponse                                       `json:"eventHonorBonuses"`
	EventMysekaiFixtureGameCharacterPerformanceBonusLimits []EventMysekaiFixtureGameCharacterPerformanceBonusLimitResponse `json:"eventMysekaiFixtureGameCharacterPerformanceBonusLimits"`
	EventRarityBonusRates                                  []EventRarityBonusRateResponse                                  `json:"eventRarityBonusRates"`
}

type VirtualLiveObjectResponse map[string]any

type VirtualLiveListResponse struct {
	Items      []VirtualLiveObjectResponse `json:"items"`
	Pagination PaginationResponse          `json:"pagination"`
}

type MasterDataStatusListResponse struct {
	Items []masterdata.SyncStatus `json:"items"`
}

type MasterDataSyncResponse struct {
	Status       string                  `json:"status"`
	Items        []masterdata.SyncStatus `json:"items"`
	Regions      []string                `json:"regions"`
	SyncRunning  bool                    `json:"sync_running"`
	StartupReady bool                    `json:"startup_ready"`
}

type MasterDataAdminStatusResponse struct {
	Status       string                  `json:"status"`
	Items        []masterdata.SyncStatus `json:"items"`
	Regions      []string                `json:"regions"`
	SyncRunning  bool                    `json:"sync_running"`
	StartupReady bool                    `json:"startup_ready"`
}

type ProfileUser struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

type ProfileAuthDebug struct {
	AdminClaim    string   `json:"admin_claim"`
	ClaimValues   []string `json:"claim_values"`
	MatchedValues []string `json:"matched_values"`
}

type ProfileResponse struct {
	User      ProfileUser       `json:"user"`
	AuthDebug *ProfileAuthDebug `json:"auth_debug,omitempty"`
}
