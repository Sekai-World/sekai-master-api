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

type MasterDataStatusListResponse struct {
	Items []masterdata.SyncStatus `json:"items"`
}

type MasterDataSyncResponse struct {
	Status      string                  `json:"status"`
	Items       []masterdata.SyncStatus `json:"items"`
	Regions     []string                `json:"regions"`
	SyncRunning bool                    `json:"sync_running"`
}

type MasterDataAdminStatusResponse struct {
	Status      string                  `json:"status"`
	Items       []masterdata.SyncStatus `json:"items"`
	Regions     []string                `json:"regions"`
	SyncRunning bool                    `json:"sync_running"`
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
