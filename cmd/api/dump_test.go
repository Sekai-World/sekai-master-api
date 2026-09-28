package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"sekai-master-api/internal/config"
	"sekai-master-api/internal/storage"
	"sekai-master-api/internal/storage/pgtest"
)

func TestMain(m *testing.M) {
	pgtest.Main(m)
}

type fakeDumpReader struct {
	records   map[string]map[string]any
	indexCall []any
}

func (reader *fakeDumpReader) GetByID(_ context.Context, _ string, _ string, id string) (map[string]any, bool, error) {
	record, ok := reader.records[id]
	return record, ok, nil
}

func (reader *fakeDumpReader) GetByCompositeKeys(_ context.Context, _ string, _ string, keys []map[string]any) ([]map[string]any, error) {
	results := make([]map[string]any, len(keys))
	for position, key := range keys {
		for _, record := range reader.records {
			if record["cardId"] == key["cardId"] && record["level"] == key["level"] {
				results[position] = record
			}
		}
	}
	return results, nil
}

func (reader *fakeDumpReader) ListAll(_ context.Context, _ string, _ string) ([]map[string]any, error) {
	return []map[string]any{reader.records["1"], reader.records["2"]}, nil
}

func (reader *fakeDumpReader) ListByIndex(_ context.Context, _ string, _ string, index string, lookups [][]any) ([][]map[string]any, error) {
	reader.indexCall = append([]any{index}, lookups[0]...)
	return [][]map[string]any{{reader.records["2"]}}, nil
}

func TestParseDumpArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want dumpRequest
	}{
		{"all records", []string{"--region", "JP", "--entity", "Cards"}, dumpRequest{region: "jp", entity: "cards"}},
		{"record ID", []string{"--region", "jp", "--entity", "cards", "--key", "1"}, dumpRequest{region: "jp", entity: "cards", id: "1"}},
		{"composite key", []string{"--region", "jp", "--entity", "resourceboxes", "--key", "id=1,resourceBoxPurpose=shop_item"}, dumpRequest{region: "jp", entity: "resourceboxes", compositeKey: map[string]any{"id": "1", "resourceBoxPurpose": "shop_item"}}},
		{"index lookup", []string{"--region", "jp", "--entity", "resourceboxdetails", "--index", "resourceBoxId=5,resourceBoxPurpose=mission_reward"}, dumpRequest{region: "jp", entity: "resourceboxdetails", index: "resourceBoxId,resourceBoxPurpose", indexValues: []any{"5", "mission_reward"}}},
	}
	for _, testCase := range cases {
		got, err := parseDumpArgs(testCase.args)
		if err != nil {
			t.Fatalf("%s: %v", testCase.name, err)
		}
		if !reflect.DeepEqual(got, testCase.want) {
			t.Fatalf("%s: got %+v, want %+v", testCase.name, got, testCase.want)
		}
	}
}

func TestParseDumpArgsRejectsInvalidInput(t *testing.T) {
	for _, args := range [][]string{
		{"--entity", "cards"},
		{"--region", "jp"},
		{"--region", "jp", "--entity", "cards", "--key", "1", "--index", "eventId=1"},
		{"--region", "jp", "--entity", "cards", "--index", "eventId"},
		{"--region", "jp", "--entity", "cards", "extra"},
		{"--region", "jp", "--entity", "cards", "--unknown"},
		{"--region", "jp", "--entity", "resourceboxes", "--key", "1"},
		{"--region", "jp", "--entity", "cards", "--key", "id=1"},
	} {
		if _, err := parseDumpArgs(args); err == nil || !strings.Contains(err.Error(), "usage: sekai-master-api dump") {
			t.Fatalf("args %v: expected a usage error, got %v", args, err)
		}
	}
}

func TestDumpRecordsPrintsJSON(t *testing.T) {
	reader := &fakeDumpReader{records: map[string]map[string]any{
		"1": {"id": 1.0, "name": "<first>"},
		"2": {"id": 2.0, "cardId": "7", "level": "3"},
	}}
	ctx := context.Background()

	decode := func(t *testing.T, request dumpRequest) any {
		t.Helper()
		var out bytes.Buffer
		if err := dumpRecords(ctx, reader, request, &out); err != nil {
			t.Fatalf("dump %+v: %v", request, err)
		}
		var decoded any
		if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
			t.Fatalf("dump %+v printed invalid JSON %q: %v", request, out.String(), err)
		}
		return decoded
	}

	if got := decode(t, dumpRequest{region: "jp", entity: "cards", id: "1"}); !reflect.DeepEqual(got, map[string]any{"id": 1.0, "name": "<first>"}) {
		t.Fatalf("record ID dump = %v", got)
	}
	if got := decode(t, dumpRequest{region: "jp", entity: "cards"}); len(got.([]any)) != 2 {
		t.Fatalf("entity dump = %v", got)
	}
	if got := decode(t, dumpRequest{region: "jp", entity: "cardparameters", compositeKey: map[string]any{"cardId": "7", "level": "3"}}); got.(map[string]any)["id"] != 2.0 {
		t.Fatalf("composite key dump = %v", got)
	}
	if got := decode(t, dumpRequest{region: "jp", entity: "eventcards", index: "eventId", indexValues: []any{"150"}}); len(got.([]any)) != 1 {
		t.Fatalf("index dump = %v", got)
	}
	if !reflect.DeepEqual(reader.indexCall, []any{"eventId", "150"}) {
		t.Fatalf("index lookup = %v", reader.indexCall)
	}

	var out bytes.Buffer
	if err := dumpRecords(ctx, reader, dumpRequest{region: "jp", entity: "cards", id: "404"}, &out); !errors.Is(err, errDumpNotFound) {
		t.Fatalf("missing record: expected errDumpNotFound, got %v", err)
	}
	if !strings.Contains(decodeRaw(t, reader, dumpRequest{region: "jp", entity: "cards", id: "1"}), "<first>") {
		t.Fatal("expected HTML characters to print unescaped")
	}
}

func decodeRaw(t *testing.T, reader dumpReader, request dumpRequest) string {
	t.Helper()
	var out bytes.Buffer
	if err := dumpRecords(context.Background(), reader, request, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// TestDumpReadsThePostgresStore runs parsed command-line arguments, whose
// values are strings, against numeric keys stored in PostgreSQL.
func TestDumpReadsThePostgresStore(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenDB(ctx, config.Config{DatabaseURL: pgtest.NewDatabase(t)})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.RunMigrations(ctx, db.SQL); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	store := storage.NewPostgresMasterDataStore(db.Pool, 2, "")
	raw := func(records ...string) []json.RawMessage {
		messages := make([]json.RawMessage, len(records))
		for position, record := range records {
			messages[position] = json.RawMessage(record)
		}
		return messages
	}
	if err := store.StoreRegion(ctx, "jp", map[string]any{
		"eventCards.json": raw(`{"id":1,"cardId":10,"eventId":150}`, `{"id":2,"cardId":11,"eventId":150}`, `{"id":3,"cardId":12,"eventId":151}`),
		"resourceBoxDetails.json": raw(
			`{"resourceBoxId":5,"resourceBoxPurpose":"mission_reward","seq":1,"resourceType":"jewel"}`,
			`{"resourceBoxId":5,"resourceBoxPurpose":"mission_reward","seq":2,"resourceType":"coin"}`,
		),
	}); err != nil {
		t.Fatalf("store region: %v", err)
	}

	run := func(args ...string) []byte {
		t.Helper()
		request, err := parseDumpArgs(append([]string{"--region", "jp"}, args...))
		if err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}
		var out bytes.Buffer
		if err := dumpRecords(ctx, store, request, &out); err != nil {
			t.Fatalf("dump %v: %v", args, err)
		}
		return out.Bytes()
	}
	ids := func(output []byte) []float64 {
		var records []map[string]any
		if err := json.Unmarshal(output, &records); err != nil {
			t.Fatalf("decode %s: %v", output, err)
		}
		result := make([]float64, len(records))
		for position, record := range records {
			result[position], _ = record["id"].(float64)
		}
		return result
	}

	var record map[string]any
	if err := json.Unmarshal(run("--entity", "eventcards", "--key", "2"), &record); err != nil || record["cardId"] != 11.0 {
		t.Fatalf("key dump = %v, %v", record, err)
	}
	if got := ids(run("--entity", "eventcards", "--index", "eventId=150")); !reflect.DeepEqual(got, []float64{1, 2}) {
		t.Fatalf("index dump ids = %v", got)
	}
	if got := ids(run("--entity", "eventcards")); !reflect.DeepEqual(got, []float64{1, 2, 3}) {
		t.Fatalf("entity dump ids = %v", got)
	}
	if err := json.Unmarshal(run("--entity", "resourceboxdetails", "--key", "resourceBoxId=5,resourceBoxPurpose=mission_reward,seq=2"), &record); err != nil || record["resourceType"] != "coin" {
		t.Fatalf("composite key dump = %v, %v", record, err)
	}
}
