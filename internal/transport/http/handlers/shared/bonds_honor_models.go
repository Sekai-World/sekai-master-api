package shared

type BondsHonorLevelResponse struct {
	ID           *int64  `json:"id,omitempty"`
	BondsHonorID *int64  `json:"bondsHonorId,omitempty"`
	Level        *int64  `json:"level,omitempty"`
	Description  *string `json:"description,omitempty"`
}

type BondsHonorWordResponse struct {
	ID              *int64  `json:"id,omitempty"`
	Seq             *int64  `json:"seq,omitempty"`
	BondsGroupID    *int64  `json:"bondsGroupId,omitempty"`
	AssetbundleName *string `json:"assetbundleName,omitempty"`
	Name            *string `json:"name,omitempty"`
	Description     *string `json:"description,omitempty"`
}

type BondsHonorGroupResponse struct {
	GroupID      *int64 `json:"groupId,omitempty"`
	CharacterID1 *int64 `json:"characterId1,omitempty"`
	CharacterID2 *int64 `json:"characterId2,omitempty"`
}

type BondsHonorCharacterUnitResponse struct {
	ID              *int64  `json:"id,omitempty"`
	GameCharacterID *int64  `json:"gameCharacterId,omitempty"`
	Unit            *string `json:"unit,omitempty"`
	// ColorCode is the unit's color, such as "#33aaee"; it tints that
	// character's half of the degree background.
	ColorCode *string `json:"colorCode,omitempty"`
}

type BondsHonorObjectResponse struct {
	ID                            *int64                           `json:"id,omitempty"`
	Seq                           *int64                           `json:"seq,omitempty"`
	BondsGroupID                  *int64                           `json:"bondsGroupId,omitempty"`
	GameCharacterUnitID1          *int64                           `json:"gameCharacterUnitId1,omitempty"`
	GameCharacterUnitID2          *int64                           `json:"gameCharacterUnitId2,omitempty"`
	HonorRarity                   *string                          `json:"honorRarity,omitempty"`
	Name                          *string                          `json:"name,omitempty"`
	Pronunciation                 *string                          `json:"pronunciation,omitempty"`
	Description                   *string                          `json:"description,omitempty"`
	ConfigurableUnitVirtualSinger *bool                            `json:"configurableUnitVirtualSinger,omitempty"`
	Levels                        *[]BondsHonorLevelResponse       `json:"levels,omitempty"`
	Words                         *[]BondsHonorWordResponse        `json:"words,omitempty"`
	BondsGroup                    *BondsHonorGroupResponse         `json:"bondsGroup,omitempty"`
	CharacterUnit1                *BondsHonorCharacterUnitResponse `json:"characterUnit1,omitempty"`
	CharacterUnit2                *BondsHonorCharacterUnitResponse `json:"characterUnit2,omitempty"`
}

type BondsHonorListResponse struct {
	Items      []BondsHonorObjectResponse `json:"items"`
	Pagination PaginationResponse         `json:"pagination"`
}
