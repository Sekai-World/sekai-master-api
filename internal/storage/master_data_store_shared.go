package storage

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"

	"sekai-master-api/internal/domain/masterdata"
)

// ErrUnknownIndex reports a lookup on an index the entity does not define.
var ErrUnknownIndex = errors.New("master data index is not defined for entity")

// ErrUnknownProjection reports a read of a projection the entity does not define.
var ErrUnknownProjection = errors.New("master data projection is not defined for entity")

// maxBlobBodySize bounds the memory one decompressed blob may use.
const maxBlobBodySize = 64 << 20

// Each pooled encoder only serves one EncodeAll call at a time, so it keeps a
// single internal encoder instead of one per GOMAXPROCS. EncodeAll output does
// not depend on the concurrency setting.
var zstdEncoderPool = sync.Pool{New: func() any {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		panic(err)
	}
	return encoder
}}

// zstdDecoder is shared by every read; DecodeAll is safe for concurrent use,
// and building a decoder per read costs ~20 KiB of garbage.
var zstdDecoder = func() *zstd.Decoder {
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(maxBlobBodySize))
	if err != nil {
		panic(err)
	}
	return decoder
}()

func newEntityIndexes(entity string) map[string]map[string][]string {
	names := masterdata.EntityIndexes(entity)
	if len(names) == 0 {
		return nil
	}
	indexes := make(map[string]map[string][]string, len(names))
	for _, name := range names {
		indexes[name] = make(map[string][]string)
	}
	return indexes
}

func addToEntityIndexes(indexes map[string]map[string][]string, storageKey string, record map[string]any) {
	if record == nil {
		return
	}
	for name, index := range indexes {
		for _, key := range masterdata.IndexKeys(record, name) {
			index[key] = append(index[key], storageKey)
		}
	}
}

// derivedDataEntities returns the entities with relation indexes or a list
// projection.
func derivedDataEntities() []string {
	entities := append(masterdata.IndexedEntities(), masterdata.ProjectedEntities()...)
	slices.Sort(entities)
	return slices.Compact(entities)
}

// buildEntityLocks creates one mutex per entity referenced by the payload
// files so concurrent writes to the same entity serialize.
func buildEntityLocks(filePaths []string) map[string]*sync.Mutex {
	entityLocks := make(map[string]*sync.Mutex)
	for _, filePath := range filePaths {
		if entity := entityNameFromPath(filePath); entity != "" {
			if _, exists := entityLocks[entity]; !exists {
				entityLocks[entity] = &sync.Mutex{}
			}
		}
	}
	return entityLocks
}

// effectiveFileConcurrency clamps the configured file concurrency to at least
// one worker and at most the number of files.
func effectiveFileConcurrency(fileConcurrency, totalFiles int) int {
	effective := fileConcurrency
	if effective <= 0 {
		effective = 1
	}
	if effective > totalFiles && totalFiles > 0 {
		effective = totalFiles
	}
	return effective
}

// rawRecordMap decodes one raw JSON record into a generic map. Empty or
// undecodable records return nil.
func rawRecordMap(rawRecord json.RawMessage) map[string]any {
	if len(rawRecord) == 0 {
		return nil
	}
	var recordMap map[string]any
	if err := json.Unmarshal(rawRecord, &recordMap); err != nil {
		return nil
	}
	if len(recordMap) == 0 {
		return nil
	}
	return recordMap
}

func reportCacheWriteProgress(reporter masterdata.ProgressReporter, region string, filePath string, processedFiles int, totalFiles int) {
	if reporter == nil {
		return
	}

	reporter(masterdata.SyncUpdatedEvent{
		Event:          "master_data_sync_progress",
		Status:         "running",
		Region:         strings.TrimSpace(region),
		Phase:          "cache",
		Message:        "writing cache",
		FilePath:       strings.TrimSpace(filePath),
		FileCount:      totalFiles,
		ProcessedFiles: processedFiles,
		TotalFiles:     totalFiles,
		UpdatedAt:      time.Now().UTC(),
	})
}

func normalizeKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func entityNameFromPath(filePath string) string {
	base := filepath.Base(strings.TrimSpace(filePath))
	if base == "" {
		return ""
	}
	name := strings.TrimSuffix(base, filepath.Ext(base))
	return normalizeKey(name)
}
