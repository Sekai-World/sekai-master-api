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
	DataVersion  string `json:"dataVersion,omitempty" example:"20260423"`
	AssetVersion string `json:"assetVersion,omitempty" example:"20260423"`
}

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

type CardSupplyResponse map[string]any

type SkillResponse map[string]any

type CharacterResponse map[string]any

type CardRarityResponse map[string]any

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

	SpecialTrainingPower1BonusFixed any `json:"specialTrainingPower1BonusFixed,omitempty"`
	SpecialTrainingPower2BonusFixed any `json:"specialTrainingPower2BonusFixed,omitempty"`
	SpecialTrainingPower3BonusFixed any `json:"specialTrainingPower3BonusFixed,omitempty"`
	CardParameters                  any `json:"cardParameters,omitempty"`
}

type CardItemsResponse struct {
	Items []CardObjectResponse `json:"items"`
}

type CardParamsResponse struct {
	ID                              any `json:"id,omitempty"`
	SpecialTrainingPower1BonusFixed any `json:"specialTrainingPower1BonusFixed,omitempty"`
	SpecialTrainingPower2BonusFixed any `json:"specialTrainingPower2BonusFixed,omitempty"`
	SpecialTrainingPower3BonusFixed any `json:"specialTrainingPower3BonusFixed,omitempty"`
	CardParameters                  any `json:"cardParameters,omitempty"`
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

type MusicObjectResponse map[string]any

type MusicListResponse struct {
	Items      []MusicObjectResponse `json:"items"`
	Pagination PaginationResponse    `json:"pagination"`
}

type EventObjectResponse map[string]any

type EventListResponse struct {
	Items      []EventObjectResponse `json:"items"`
	Pagination PaginationResponse    `json:"pagination"`
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
