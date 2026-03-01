package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sekai-master-api/internal/domain/masterdata"
)

type fileMasterDataPayloadBackupStore struct {
	baseDir string
}

type backupSnapshot struct {
	Region    string            `json:"region"`
	Source    masterdata.Source `json:"source"`
	Commit    string            `json:"commit"`
	Files     []string          `json:"files"`
	UpdatedAt time.Time         `json:"updated_at"`
}

func NewFileMasterDataPayloadBackupStore(baseDir string) MasterDataPayloadBackupStore {
	cleanBaseDir := strings.TrimSpace(baseDir)
	if cleanBaseDir == "" {
		cleanBaseDir = "tmp/master-data-backup"
	}

	return &fileMasterDataPayloadBackupStore{baseDir: cleanBaseDir}
}

func (store *fileMasterDataPayloadBackupStore) SaveRegionPayload(_ context.Context, source masterdata.Source, commit string, payload map[string]any) error {
	region := strings.TrimSpace(source.Region)
	commit = strings.TrimSpace(commit)
	if region == "" || commit == "" || len(payload) == 0 {
		return nil
	}

	regionDir := store.regionDir(region)
	latestDir := filepath.Join(regionDir, "latest")

	if err := os.RemoveAll(latestDir); err != nil {
		return fmt.Errorf("clear latest backup dir: %w", err)
	}

	if err := os.MkdirAll(latestDir, 0o755); err != nil {
		return fmt.Errorf("create latest backup dir: %w", err)
	}

	files := make([]string, 0, len(payload))
	for rawPath, value := range payload {
		relPath, ok := normalizeJSONRelativePath(rawPath)
		if !ok {
			continue
		}

		body, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("marshal backup file %s: %w", relPath, err)
		}

		targetPath := filepath.Join(latestDir, relPath)
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("create backup subdir for %s: %w", relPath, err)
		}
		if err := os.WriteFile(targetPath, body, 0o644); err != nil {
			return fmt.Errorf("write backup file %s: %w", relPath, err)
		}

		files = append(files, relPath)
	}

	if len(files) == 0 {
		return nil
	}

	snapshot := backupSnapshot{
		Region:    region,
		Source:    source,
		Commit:    commit,
		Files:     files,
		UpdatedAt: time.Now().UTC(),
	}

	body, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal backup snapshot: %w", err)
	}

	if err := os.MkdirAll(regionDir, 0o755); err != nil {
		return fmt.Errorf("create region backup dir: %w", err)
	}

	if err := os.WriteFile(store.metaFilePath(region), body, 0o644); err != nil {
		return fmt.Errorf("write backup snapshot: %w", err)
	}

	return nil
}

func (store *fileMasterDataPayloadBackupStore) LoadRegionPayload(_ context.Context, source masterdata.Source, commit string) (map[string]any, bool, error) {
	region := strings.TrimSpace(source.Region)
	commit = strings.TrimSpace(commit)
	if region == "" || commit == "" {
		return nil, false, nil
	}

	body, err := os.ReadFile(store.metaFilePath(region))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read backup snapshot: %w", err)
	}

	var snapshot backupSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return nil, false, fmt.Errorf("decode backup snapshot: %w", err)
	}

	if strings.TrimSpace(snapshot.Commit) != commit {
		return nil, false, nil
	}
	if !sameSource(snapshot.Source, source) {
		return nil, false, nil
	}
	if len(snapshot.Files) == 0 {
		return nil, false, nil
	}

	latestDir := filepath.Join(store.regionDir(region), "latest")
	payload := make(map[string]any, len(snapshot.Files))
	for _, relPath := range snapshot.Files {
		if _, ok := normalizeJSONRelativePath(relPath); !ok {
			continue
		}

		filePath := filepath.Join(latestDir, relPath)
		content, readErr := os.ReadFile(filePath)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("read backup file %s: %w", relPath, readErr)
		}

		var value any
		if decodeErr := json.Unmarshal(content, &value); decodeErr != nil {
			return nil, false, fmt.Errorf("decode backup file %s: %w", relPath, decodeErr)
		}

		payload[relPath] = value
	}

	if len(payload) == 0 {
		return nil, false, nil
	}

	return payload, true, nil
}

func (store *fileMasterDataPayloadBackupStore) regionDir(region string) string {
	return filepath.Join(store.baseDir, sanitizeRegion(region))
}

func (store *fileMasterDataPayloadBackupStore) metaFilePath(region string) string {
	return filepath.Join(store.regionDir(region), "meta.json")
}

func sanitizeRegion(region string) string {
	trimmed := strings.ToLower(strings.TrimSpace(region))
	if trimmed == "" {
		return "unknown"
	}

	builder := strings.Builder{}
	builder.Grow(len(trimmed))
	for _, character := range trimmed {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			builder.WriteRune(character)
			continue
		}
		builder.WriteRune('_')
	}

	result := strings.Trim(builder.String(), "_")
	if result == "" {
		return "unknown"
	}

	return result
}

func normalizeJSONRelativePath(rawPath string) (string, bool) {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return "", false
	}

	cleaned := filepath.Clean(filepath.FromSlash(trimmed))
	if cleaned == "." || strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return "", false
	}

	rel := filepath.ToSlash(cleaned)
	if !strings.HasSuffix(strings.ToLower(rel), ".json") {
		return "", false
	}

	return rel, true
}

func sameSource(left masterdata.Source, right masterdata.Source) bool {
	return strings.TrimSpace(left.Region) == strings.TrimSpace(right.Region) &&
		strings.TrimSpace(left.Owner) == strings.TrimSpace(right.Owner) &&
		strings.TrimSpace(left.Repo) == strings.TrimSpace(right.Repo) &&
		strings.TrimSpace(left.Ref) == strings.TrimSpace(right.Ref) &&
		strings.TrimSpace(left.Path) == strings.TrimSpace(right.Path)
}
