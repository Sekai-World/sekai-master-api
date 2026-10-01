package shared

// StampListItemResponse is one stamp. CharacterIDs are the game characters
// the stamp shows, in slot order; Category is derived from them and the stamp
// type: character (one character), bond (two or more), text (text and
// Cheerful Carnival message stamps), or other (no character).
type StampListItemResponse struct {
	ID                  int64   `json:"id" binding:"required"`
	Seq                 *int64  `json:"seq,omitempty"`
	StampType           string  `json:"stampType" binding:"required"`
	Category            string  `json:"category" binding:"required"`
	Name                string  `json:"name" binding:"required"`
	AssetbundleName     *string `json:"assetbundleName,omitempty"`
	CharacterIDs        []int64 `json:"characterIds" binding:"required"`
	GameCharacterUnitID *int64  `json:"gameCharacterUnitId,omitempty"`
	Description         *string `json:"description,omitempty"`
}

type StampListResponse struct {
	Items      []StampListItemResponse `json:"items" binding:"required"`
	Pagination PaginationResponse      `json:"pagination" binding:"required"`
}
