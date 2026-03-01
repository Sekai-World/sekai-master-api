package masterdata

import (
	"context"
	"time"
)

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
	SourceCommit   string    `json:"source_commit"`
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

type SyncUpdatedEvent struct {
	Event          string      `json:"event"`
	Status         string      `json:"status"`
	Region         string      `json:"region,omitempty"`
	StatusItem     *SyncStatus `json:"status_item,omitempty"`
	Phase          string      `json:"phase,omitempty"`
	Message        string      `json:"message,omitempty"`
	CurrentStep    int         `json:"current_step,omitempty"`
	TotalSteps     int         `json:"total_steps,omitempty"`
	FileCount      int         `json:"file_count,omitempty"`
	ProcessedFiles int         `json:"processed_files,omitempty"`
	TotalFiles     int         `json:"total_files,omitempty"`
	FailedFiles    int         `json:"failed_files,omitempty"`
	FilePath       string      `json:"file_path,omitempty"`
	DurationMS     int64       `json:"duration_ms,omitempty"`
	Regions        []string    `json:"regions"`
	FailedRegions  []string    `json:"failed_regions"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

type ProgressReporter func(event SyncUpdatedEvent)

type progressReporterContextKey struct{}

func WithProgressReporter(ctx context.Context, reporter ProgressReporter) context.Context {
	if reporter == nil {
		return ctx
	}

	return context.WithValue(ctx, progressReporterContextKey{}, reporter)
}

func ProgressReporterFromContext(ctx context.Context) ProgressReporter {
	if ctx == nil {
		return nil
	}

	reporter, _ := ctx.Value(progressReporterContextKey{}).(ProgressReporter)
	return reporter
}
