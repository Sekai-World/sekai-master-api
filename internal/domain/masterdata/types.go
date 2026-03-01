package masterdata

import "time"

type Source struct {
	Region string `json:"region"`
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Ref    string `json:"ref"`
	Path   string `json:"path"`
}

type SyncStatus struct {
	Region         string    `json:"region"`
	Status         string    `json:"status"`
	FileCount      int       `json:"file_count"`
	SyncDurationMS int64     `json:"sync_duration_ms"`
	LastSyncedAt   time.Time `json:"last_synced_at"`
	ErrorMessage   string    `json:"error_message"`
	Source         Source    `json:"source"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type SearchMatch struct {
	Item         map[string]any `json:"item"`
	MatchScore   int            `json:"match_score"`
	MatchType    string         `json:"match_type"`
	MatchedField string         `json:"matched_field"`
}
