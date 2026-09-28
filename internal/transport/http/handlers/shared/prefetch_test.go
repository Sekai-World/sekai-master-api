package shared

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type countingReader struct {
	records  map[string]map[string]map[string]map[string]any // region -> entity -> id -> record
	batches  []string
	singles  []string
	batchErr error
}

func (reader *countingReader) GetByID(_ context.Context, region string, entity string, id string) (map[string]any, bool, error) {
	reader.singles = append(reader.singles, region+"/"+entity+"/"+id)
	record, ok := reader.records[region][entity][id]
	return record, ok, nil
}

func (reader *countingReader) GetByIDs(_ context.Context, region string, entity string, ids []string) ([]map[string]any, error) {
	reader.batches = append(reader.batches, region+"/"+entity+"/"+joinIDs(ids))
	if reader.batchErr != nil {
		return nil, reader.batchErr
	}
	records := make([]map[string]any, len(ids))
	for position, id := range ids {
		records[position] = reader.records[region][entity][id]
	}
	return records, nil
}

func joinIDs(ids []string) string {
	joined := ""
	for position, id := range ids {
		if position > 0 {
			joined += ","
		}
		joined += id
	}
	return joined
}

func TestPrefetchRecordsReadsEachEntityOnceAndAnswersLookups(t *testing.T) {
	ctx := context.Background()
	reader := &countingReader{records: map[string]map[string]map[string]map[string]any{"jp": {
		"skills": {"1": {"id": 1.0, "name": "a"}, "2": {"id": 2.0, "name": "b"}},
		"cards":  {"9": {"id": 9.0}},
	}}}

	prefetched, err := PrefetchRecords(ctx, reader, "jp", map[string][]string{
		"skills": {"2", "1", "2", "", "404"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reader.batches, []string{"jp/skills/1,2,404"}) {
		t.Fatalf("expected one sorted, deduplicated batch per entity, got %v", reader.batches)
	}

	if record, found, err := prefetched.Lookup(ctx, "jp", "skills", "2"); err != nil || !found || record["name"] != "b" {
		t.Fatalf("expected the prefetched record, got %v %v %v", record, found, err)
	}
	if _, found, _ := prefetched.Lookup(ctx, "jp", "skills", "404"); found {
		t.Fatal("expected a prefetched miss to stay a miss")
	}
	if len(reader.singles) != 0 {
		t.Fatalf("expected prefetched lookups not to read again, got %v", reader.singles)
	}

	if _, found, _ := prefetched.Lookup(ctx, "jp", "cards", "9"); !found {
		t.Fatal("expected an entity that was not prefetched to fall back to GetByID")
	}
	if _, found, _ := prefetched.Lookup(ctx, "en", "skills", "1"); found {
		t.Fatal("expected another region to fall back to GetByID")
	}
	if !reflect.DeepEqual(reader.singles, []string{"jp/cards/9", "en/skills/1"}) {
		t.Fatalf("expected fallbacks for unprefetched lookups only, got %v", reader.singles)
	}

	if err := prefetched.Add(ctx, map[string][]string{"skills": {"1", "2"}, "cards": {"9"}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reader.batches, []string{"jp/skills/1,2,404", "jp/cards/9"}) {
		t.Fatalf("expected Add to read only IDs not prefetched yet, got %v", reader.batches)
	}
	if record, ok := prefetched.Record("cards", "9"); !ok || record["id"] != 9.0 {
		t.Fatalf("expected Record to return the added record, got %v", record)
	}
}

func TestPrefetchRecordsReturnsBatchErrors(t *testing.T) {
	reader := &countingReader{batchErr: errors.New("down")}
	if _, err := PrefetchRecords(context.Background(), reader, "jp", map[string][]string{"skills": {"1"}}); err == nil {
		t.Fatal("expected a failed batch read to fail the prefetch")
	}
}
