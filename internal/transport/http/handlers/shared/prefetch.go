package shared

import (
	"context"
	"sort"
	"sync"
)

// RecordLookup reads one master-data record by entity and ID, like
// usecase.MasterDataSyncUsecase.GetByID.
type RecordLookup func(ctx context.Context, region string, entity string, id string) (map[string]any, bool, error)

// BatchRecordReader reads records one at a time or many at once.
type BatchRecordReader interface {
	GetByID(ctx context.Context, region string, entity string, id string) (map[string]any, bool, error)
	GetByIDs(ctx context.Context, region string, entity string, ids []string) ([]map[string]any, error)
}

// PrefetchedRecords answers record lookups for one region from records read
// up front with one GetByIDs per entity. Handlers that enrich many items (a
// page of cards, every reward of an event) collect the IDs they will look up,
// prefetch them, and pass Lookup where they used GetByID, so a page costs one
// read per related entity instead of one per item. A lookup that was not
// prefetched falls back to GetByID, so an incomplete prefetch stays correct.
type PrefetchedRecords struct {
	reader BatchRecordReader
	region string

	mu      sync.Mutex
	records map[string]map[string]map[string]any // entity -> id -> record (nil: known missing)
}

// PrefetchRecords reads the records named by idsByEntity for region.
func PrefetchRecords(ctx context.Context, reader BatchRecordReader, region string, idsByEntity map[string][]string) (*PrefetchedRecords, error) {
	prefetched := &PrefetchedRecords{reader: reader, region: region, records: map[string]map[string]map[string]any{}}
	if err := prefetched.Add(ctx, idsByEntity); err != nil {
		return nil, err
	}
	return prefetched, nil
}

// Add reads more records into the prefetch, for lookups whose IDs are only
// known after a first prefetch (for example a title's group).
func (prefetched *PrefetchedRecords) Add(ctx context.Context, idsByEntity map[string][]string) error {
	if prefetched == nil || prefetched.reader == nil {
		return nil
	}
	entities := make([]string, 0, len(idsByEntity))
	for entity := range idsByEntity {
		entities = append(entities, entity)
	}
	sort.Strings(entities)

	for _, entity := range entities {
		ids := prefetched.missing(entity, idsByEntity[entity])
		if len(ids) == 0 {
			continue
		}
		records, err := prefetched.reader.GetByIDs(ctx, prefetched.region, entity, ids)
		if err != nil {
			return err
		}
		prefetched.mu.Lock()
		byID := prefetched.records[entity]
		if byID == nil {
			byID = make(map[string]map[string]any, len(ids))
			prefetched.records[entity] = byID
		}
		for position, id := range ids {
			if position < len(records) {
				byID[id] = records[position]
			} else {
				byID[id] = nil
			}
		}
		prefetched.mu.Unlock()
	}
	return nil
}

// missing returns the distinct, non-empty IDs not prefetched yet, sorted.
func (prefetched *PrefetchedRecords) missing(entity string, ids []string) []string {
	prefetched.mu.Lock()
	defer prefetched.mu.Unlock()
	known := prefetched.records[entity]
	seen := make(map[string]struct{}, len(ids))
	result := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := NormalizeAnyID(raw)
		if id == "" {
			continue
		}
		if _, done := known[id]; done {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

// Lookup answers from the prefetched records of its region, and falls back to
// GetByID for anything else.
func (prefetched *PrefetchedRecords) Lookup(ctx context.Context, region string, entity string, id string) (map[string]any, bool, error) {
	if prefetched == nil || prefetched.reader == nil {
		return nil, false, nil
	}
	if region == prefetched.region {
		prefetched.mu.Lock()
		record, known := prefetched.records[entity][NormalizeAnyID(id)]
		prefetched.mu.Unlock()
		if known {
			return record, record != nil, nil
		}
	}
	return prefetched.reader.GetByID(ctx, region, entity, id)
}

// Record returns a prefetched record, if any, without falling back.
func (prefetched *PrefetchedRecords) Record(entity string, id string) (map[string]any, bool) {
	if prefetched == nil {
		return nil, false
	}
	prefetched.mu.Lock()
	defer prefetched.mu.Unlock()
	record := prefetched.records[entity][NormalizeAnyID(id)]
	return record, record != nil
}
