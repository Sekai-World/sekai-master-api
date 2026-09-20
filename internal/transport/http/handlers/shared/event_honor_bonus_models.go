package shared

type EventHonorBonusHonorGroupResponse struct {
	ID                        *int64  `json:"id,omitempty"`
	Name                      *string `json:"name,omitempty"`
	HonorType                 *string `json:"honorType,omitempty"`
	BackgroundAssetbundleName *string `json:"backgroundAssetbundleName,omitempty"`
	FrameName                 *string `json:"frameName,omitempty"`
}

type EventHonorBonusHonorResponse struct {
	ID               *int64                             `json:"id,omitempty"`
	GroupID          *int64                             `json:"groupId,omitempty"`
	Name             *string                            `json:"name,omitempty"`
	HonorRarity      *string                            `json:"honorRarity,omitempty"`
	HonorMissionType *string                            `json:"honorMissionType,omitempty"`
	HonorTypeID      *int64                             `json:"honorTypeId,omitempty"`
	AssetbundleName  *string                            `json:"assetbundleName,omitempty"`
	Group            *EventHonorBonusHonorGroupResponse `json:"group,omitempty"`
}

type EventHonorBonusLeaderGameCharacterUnitResponse struct {
	ID              *int64  `json:"id,omitempty"`
	GameCharacterID *int64  `json:"gameCharacterId,omitempty"`
	Unit            *string `json:"unit,omitempty"`
}

type EventHonorBonusObjectResponse struct {
	ID                      *int64                                          `json:"id,omitempty"`
	EventID                 *int64                                          `json:"eventId,omitempty"`
	HonorID                 *int64                                          `json:"honorId,omitempty"`
	LeaderGameCharacterID   *int64                                          `json:"leaderGameCharacterId,omitempty"`
	BonusRate               *int64                                          `json:"bonusRate,omitempty"`
	Honor                   *EventHonorBonusHonorResponse                   `json:"honor,omitempty"`
	LeaderGameCharacterUnit *EventHonorBonusLeaderGameCharacterUnitResponse `json:"leaderGameCharacterUnit,omitempty"`
}

type EventHonorBonusListResponse struct {
	Items []EventHonorBonusObjectResponse `json:"items"`
}
