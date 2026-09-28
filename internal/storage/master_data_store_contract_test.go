package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"sekai-master-api/internal/domain/masterdata"
)

// The master-data store contract: every store (Redis today, PostgreSQL
// during the migration in docs/postgres-master-data-store.md) must answer
// these reads identically. Each scenario runs against every store, and
// TestStoreContractParity compares the stores' answers directly.

// contractStore is the storage surface handlers and sync use.
type contractStore interface {
	StoreRegion(ctx context.Context, region string, payload map[string]any) error
	StoreRegionWithSourceDigests(ctx context.Context, region string, payload map[string]any, fileDigests map[string]string) error
	GetByID(ctx context.Context, region string, entity string, id string) (map[string]any, bool, error)
	GetByIDs(ctx context.Context, region string, entity string, ids []string) ([]map[string]any, error)
	GetByCompositeKeys(ctx context.Context, region string, entity string, keys []map[string]any) ([]map[string]any, error)
	ListAll(ctx context.Context, region string, entity string) ([]map[string]any, error)
	ListByPage(ctx context.Context, region string, entity string, page int, pageSize int) ([]map[string]any, int, error)
	ListByIndex(ctx context.Context, region string, entity string, index string, lookups [][]any) ([][]map[string]any, error)
	LoadProjection(ctx context.Context, region string, entity string) (*masterdata.Projection, error)
	HasEntityRecords(ctx context.Context, region string, entity string) (bool, error)
	EnsureDerivedEntityData(ctx context.Context, region string) ([]string, error)
	StoreRegionVersionPayload(ctx context.Context, region string, version any) error
	LoadRegionVersionPayload(ctx context.Context, region string) (any, bool, error)
}

var (
	_ contractStore = (*RedisMasterDataCache)(nil)
	_ contractStore = (*PostgresMasterDataStore)(nil)
)

type contractBackend struct {
	name string
	open func(t *testing.T) contractStore
	// markDerivedDataStale makes the stored relation index and projection
	// versions of entity out of date, as a definition change would.
	markDerivedDataStale func(t *testing.T, store contractStore, region, entity string)
}

func contractBackends() []contractBackend {
	return []contractBackend{
		{
			name: "redis",
			open: func(t *testing.T) contractStore {
				return newStoreRegionTestCache(t, startTestMiniRedis(t))
			},
			markDerivedDataStale: func(t *testing.T, store contractStore, region, entity string) {
				cache := store.(*RedisMasterDataCache)
				ctx := context.Background()
				for _, key := range []string{cache.redisEntityIndexVersionKey(region, entity), cache.redisEntityProjectionVersionKey(region, entity)} {
					if err := cache.client.Set(ctx, key, "stale", 0).Err(); err != nil {
						t.Fatalf("mark stale: %v", err)
					}
				}
			},
		},
		{
			name: "postgres",
			open: func(t *testing.T) contractStore {
				return NewPostgresMasterDataStore(openMigratedTestDB(t).Pool, 4, "")
			},
			markDerivedDataStale: func(t *testing.T, store contractStore, region, entity string) {
				pool := store.(*PostgresMasterDataStore).pool
				if _, err := pool.Exec(context.Background(), `
UPDATE master_entities SET index_version = 'stale', projection_version = 'stale'
WHERE region = $1 AND entity = $2`, region, entity); err != nil {
					t.Fatalf("mark stale: %v", err)
				}
			},
		},
	}
}

func forEachStore(t *testing.T, run func(t *testing.T, backend contractBackend, store contractStore)) {
	t.Helper()
	for _, backend := range contractBackends() {
		t.Run(backend.name, func(t *testing.T) {
			run(t, backend, backend.open(t))
		})
	}
}

func rawRecords(t *testing.T, records ...string) []json.RawMessage {
	t.Helper()
	raws := make([]json.RawMessage, len(records))
	for index, record := range records {
		if !json.Valid([]byte(record)) {
			t.Fatalf("invalid fixture record %s", record)
		}
		raws[index] = json.RawMessage(record)
	}
	return raws
}

// contractPayload is a region payload that exercises ordering, duplicate and
// missing ids, decimal ids of a million or more, composite keys with auto-key
// fallbacks, legacy decoded records, relation indexes, projections, block
// splits by record count and by bytes, and a non-record file.
func contractPayload(t *testing.T) map[string]any {
	t.Helper()

	large := strings.Repeat("x", 9000)
	musicVocals := make([]string, 0, 90)
	for id := 90; id >= 1; id-- {
		caption := fmt.Sprintf("vocal %d", id)
		if id%10 == 0 {
			caption = large
		}
		musicVocals = append(musicVocals, fmt.Sprintf(`{"id":%d,"musicId":%d,"caption":%q}`, id, id%7, caption))
	}
	musicVocals = append(musicVocals,
		`{"id":"007","musicId":1,"caption":"string id with leading zero"}`,
		`{"id":"abc","musicId":2,"caption":"string id"}`,
		`{"id":100000000000000000000,"musicId":3,"caption":"21 digits"}`,
		`{"musicId":4,"caption":"no id"}`,
	)

	return map[string]any{
		"cards.json": rawRecords(t,
			`{"id":3,"characterId":1,"prefix":"third","cardRarityType":"rarity_4","releaseAt":1600000000000}`,
			`{"id":1,"characterId":2,"prefix":"first","cardRarityType":"rarity_2","releaseAt":1500000000000}`,
			`{"id":2,"characterId":1,"prefix":"second (old)","cardRarityType":"rarity_3"}`,
			`{"id":1010201,"characterId":3,"prefix":"million","cardRarityType":"rarity_birthday"}`,
			`{"id":2,"characterId":1,"prefix":"second","cardRarityType":"rarity_3","attr":"cute"}`,
			`{"prefix":"no id","characterId":4}`,
			`{"id":4, "characterId": 5 ,"prefix":"spaced json","nested":{"b":[1,2,{"c":null}],"a":1.50}}`,
		),
		"cardEpisodes.json": rawRecords(t,
			`{"id":11,"cardId":1,"seq":1}`,
			`{"id":12,"cardId":1,"seq":2}`,
			`{"id":21,"cardId":2,"seq":1}`,
			`{"id":31,"cardId":1010201,"seq":1}`,
		),
		"resourceBoxes.json": rawRecords(t,
			`{"id":5,"resourceBoxPurpose":"mission_reward","resourceBoxType":"expand"}`,
			`{"id":5,"resourceBoxPurpose":"event_ranking_reward","resourceBoxType":"expand"}`,
			`{"id":6,"resourceBoxPurpose":"mission_reward","resourceBoxType":"expand"}`,
		),
		"resourceBoxDetails.json": rawRecords(t,
			`{"resourceBoxId":5,"resourceBoxPurpose":"mission_reward","seq":1,"resourceType":"jewel","resourceQuantity":100}`,
			`{"resourceBoxId":5,"resourceBoxPurpose":"mission_reward","seq":2,"resourceType":"coin","resourceQuantity":5000}`,
			`{"resourceBoxId":5,"resourceBoxPurpose":"event_ranking_reward","seq":1,"resourceType":"honor","resourceId":7}`,
			`{"resourceBoxId":6,"resourceBoxPurpose":"mission_reward","resourceType":"no seq"}`,
			`{"resourceBoxId":6,"resourceBoxPurpose":"mission_reward","resourceType":"no seq"}`,
		),
		"gachas.json": []any{
			map[string]any{"id": float64(20), "gachaType": "ceil", "name": "second gacha", "startAt": float64(1700000000000),
				"gachaPickups": []any{map[string]any{"id": float64(1), "cardId": float64(2)}, map[string]any{"id": float64(2), "cardId": float64(3)}}},
			map[string]any{"id": float64(10), "gachaType": "normal", "name": "first gacha", "startAt": float64(1600000000000),
				"gachaPickups": []any{map[string]any{"id": float64(3), "cardId": float64(1)}}},
			"not a record",
		},
		"musicVocals.json": rawRecords(t, musicVocals...),
		"versions.json":    map[string]any{"dataVersion": "1.0.0", "appVersion": "3.0.0"},
	}
}

var contractEntities = []string{"cards", "cardepisodes", "resourceboxes", "resourceboxdetails", "gachas", "musicvocals", "musiccategories", "versions"}

// contractReads performs every read of the contract on store and returns the
// answers keyed by a description, for comparison between stores.
func contractReads(t *testing.T, store contractStore) map[string]any {
	t.Helper()
	ctx := context.Background()
	answers := map[string]any{}
	record := func(name string, value any, err error) {
		if err != nil {
			answers[name] = "error: " + err.Error()
			return
		}
		answers[name] = value
	}

	for _, entity := range contractEntities {
		all, err := store.ListAll(ctx, "jp", entity)
		record("ListAll "+entity, all, err)
		has, err := store.HasEntityRecords(ctx, "jp", entity)
		record("HasEntityRecords "+entity, has, err)
		for _, pageSize := range []int{-1, 0, 1, 7, 20, 100, 101} {
			for page := -1; page <= 16; page++ {
				items, total, err := store.ListByPage(ctx, "jp", entity, page, pageSize)
				record(fmt.Sprintf("ListByPage %s page=%d size=%d", entity, page, pageSize), []any{items, total}, err)
			}
		}
	}

	ids := []string{"1", " 2 ", "3", "4", "1010201", "1.010201e+06", "5", "", "007", "abc", "100000000000000000000", "90", "10", "50", "1", "missing"}
	for _, entity := range []string{"cards", "musicvocals", "resourceboxes", "musiccategories"} {
		for _, id := range ids {
			value, found, err := store.GetByID(ctx, "jp", entity, id)
			record(fmt.Sprintf("GetByID %s %q", entity, id), []any{value, found}, err)
		}
		values, err := store.GetByIDs(ctx, "jp", entity, ids)
		record("GetByIDs "+entity, values, err)
	}
	autoKey := masterdata.AutoRecordKey([]byte(`{"prefix":"no id","characterId":4}`))
	values, err := store.GetByIDs(ctx, "jp", "cards", []string{autoKey})
	record("GetByIDs cards auto key", values, err)

	compositeKeys := []map[string]any{
		{"id": 5, "resourceBoxPurpose": "mission_reward"},
		{"id": float64(5), "resourceBoxPurpose": "event_ranking_reward"},
		{"id": "6", "resourceBoxPurpose": "mission_reward"},
		{"id": 5},
		{"id": 7, "resourceBoxPurpose": "mission_reward"},
		{"resourceBoxId": 5, "resourceBoxPurpose": "mission_reward", "seq": 2},
		{"resourceBoxId": json.Number("5"), "resourceBoxPurpose": "event_ranking_reward", "seq": 1},
		nil,
	}
	for _, entity := range []string{"resourceboxes", "resourceboxdetails", "cards"} {
		values, err := store.GetByCompositeKeys(ctx, "jp", entity, compositeKeys)
		record("GetByCompositeKeys "+entity, values, err)
	}

	indexReads := []struct {
		entity, index string
		lookups       [][]any
	}{
		{"cardepisodes", "cardId", [][]any{{1}, {"2"}, {1010201}, {99}, {nil}, {1}, {json.Number("1")}}},
		{"resourceboxdetails", "resourceBoxId,resourceBoxPurpose", [][]any{{5, "mission_reward"}, {6, "mission_reward"}, {5, "event_ranking_reward"}, {5}, {7, "none"}}},
		{"resourceboxes", "id", [][]any{{5}, {6}, {8}}},
		{"gachas", "gachaPickups.cardId", [][]any{{1}, {2}, {3}, {4}}},
		{"musicvocals", "musicId", [][]any{{0}, {1}, {2}, {3}, {4}, {5}, {6}}},
		{"musiccategories", "musicId", [][]any{{1}, {nil}}},
		{"cards", "characterId", [][]any{{1}}},
	}
	for _, read := range indexReads {
		values, err := store.ListByIndex(ctx, "jp", read.entity, read.index, read.lookups)
		record("ListByIndex "+read.entity+" "+read.index, values, err)
	}

	for _, entity := range []string{"cards", "gachas", "costume3ds", "musicvocals"} {
		projection, err := store.LoadProjection(ctx, "jp", entity)
		record("LoadProjection "+entity, projection, err)
	}

	version, found, err := store.LoadRegionVersionPayload(ctx, "jp")
	record("LoadRegionVersionPayload", []any{version, found}, err)
	return answers
}

func storeContractPayload(t *testing.T, store contractStore) {
	t.Helper()
	ctx := context.Background()
	payload := contractPayload(t)
	if err := store.StoreRegion(ctx, "JP", payload); err != nil {
		t.Fatalf("store region: %v", err)
	}
	if err := store.StoreRegionVersionPayload(ctx, "jp", payload["versions.json"]); err != nil {
		t.Fatalf("store version payload: %v", err)
	}
}

// TestStoreContractParity stores the same payload in every store and requires
// identical answers to every read, which is what keeps response bodies
// byte-for-byte identical across stores.
func TestStoreContractParity(t *testing.T) {
	backends := contractBackends()
	answers := make([]map[string]any, len(backends))
	for index, backend := range backends {
		store := backend.open(t)
		storeContractPayload(t, store)
		answers[index] = contractReads(t, store)
	}

	reference := answers[0]
	if len(reference) < 900 {
		t.Fatalf("parity compared only %d reads", len(reference))
	}
	for index := 1; index < len(answers); index++ {
		for name, want := range reference {
			got, ok := answers[index][name]
			if !ok {
				t.Errorf("%s: %s has no answer", name, backends[index].name)
				continue
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s differs:\n%s: %s\n%s: %s", name, backends[0].name, describe(want), backends[index].name, describe(got))
			}
		}
	}
}

func describe(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%#v", value)
	}
	if len(body) > 600 {
		return string(body[:600]) + "…"
	}
	return string(body)
}

// TestStoreContractReads pins the answers themselves, so the stores cannot
// agree on a wrong one.
func TestStoreContractReads(t *testing.T) {
	forEachStore(t, func(t *testing.T, _ contractBackend, store contractStore) {
		storeContractPayload(t, store)
		ctx := context.Background()

		cards, err := store.ListAll(ctx, "jp", "cards")
		if err != nil {
			t.Fatalf("list cards: %v", err)
		}
		// Source order; the repeated id 2 keeps both positions and reads the
		// record stored last under it.
		if got := joinedFieldValues(cards, "prefix"); got != "third,first,second,million,second,no id,spaced json" {
			t.Fatalf("cards order = %s", got)
		}

		page, total, err := store.ListByPage(ctx, "jp", "cards", 2, 3)
		if err != nil || total != 7 || joinedFieldValues(page, "prefix") != "million,second,no id" {
			t.Fatalf("cards page 2 = %s total %d err %v", joinedFieldValues(page, "prefix"), total, err)
		}
		if page, total, err := store.ListByPage(ctx, "jp", "cards", 3, 3); err != nil || total != 7 || joinedFieldValues(page, "prefix") != "spaced json" {
			t.Fatalf("cards page 3 = %s total %d err %v", joinedFieldValues(page, "prefix"), total, err)
		}

		million, found, err := store.GetByID(ctx, "jp", "cards", "1010201")
		if err != nil || !found || million["prefix"] != "million" {
			t.Fatalf("GetByID 1010201 = %v %v %v", million, found, err)
		}
		if _, found, _ := store.GetByID(ctx, "jp", "cards", "1.010201e+06"); found {
			t.Fatal("exponent-form id matched")
		}
		spaced, _, _ := store.GetByID(ctx, "jp", "cards", "4")
		if nested, ok := spaced["nested"].(map[string]any); !ok || nested["a"] != 1.5 {
			t.Fatalf("spaced record = %v", spaced)
		}

		byIDs, err := store.GetByIDs(ctx, "jp", "cards", []string{"3", "missing", "2", "3"})
		if err != nil || len(byIDs) != 4 || byIDs[1] != nil || byIDs[0]["prefix"] != "third" || byIDs[2]["attr"] != "cute" || byIDs[3]["prefix"] != "third" {
			t.Fatalf("GetByIDs = %v %v", byIDs, err)
		}

		vocals, err := store.ListAll(ctx, "jp", "musicvocals")
		if err != nil || len(vocals) != 94 {
			t.Fatalf("musicvocals = %d records, %v", len(vocals), err)
		}
		boundary, err := store.GetByIDs(ctx, "jp", "musicvocals", []string{"1", "32", "33", "64", "65", "90", "007", "abc", "100000000000000000000", "7"})
		if err != nil {
			t.Fatalf("GetByIDs musicvocals: %v", err)
		}
		for index, record := range boundary {
			if record == nil {
				t.Fatalf("GetByIDs musicvocals position %d missing", index)
			}
		}

		boxes, err := store.GetByCompositeKeys(ctx, "jp", "resourceboxes", []map[string]any{
			{"id": 5, "resourceBoxPurpose": "event_ranking_reward"},
			{"id": 5},
		})
		if err != nil || boxes[0]["resourceBoxPurpose"] != "event_ranking_reward" || boxes[1] != nil {
			t.Fatalf("GetByCompositeKeys = %v %v", boxes, err)
		}
		if _, found, _ := store.GetByID(ctx, "jp", "resourceboxes", "5"); found {
			t.Fatal("bare id matched a composite-key record")
		}
		details, err := store.ListAll(ctx, "jp", "resourceboxdetails")
		if err != nil || len(details) != 5 {
			t.Fatalf("resourceboxdetails = %d records, %v", len(details), err)
		}

		episodes, err := store.ListByIndex(ctx, "jp", "cardepisodes", "cardId", [][]any{{1}, {99}, {nil}})
		if err != nil || joinedFieldValues(episodes[0], "id") != "11,12" || episodes[1] == nil || len(episodes[1]) != 0 || episodes[2] != nil {
			t.Fatalf("ListByIndex cardepisodes = %v %v", episodes, err)
		}
		absent, err := store.ListByIndex(ctx, "jp", "musiccategories", "musicId", [][]any{{1}, {nil}})
		if err != nil || absent[0] == nil || len(absent[0]) != 0 || absent[1] != nil {
			t.Fatalf("ListByIndex absent entity = %v %v", absent, err)
		}
		if _, err := store.ListByIndex(ctx, "jp", "cards", "characterId", [][]any{{1}}); !errors.Is(err, ErrUnknownIndex) {
			t.Fatalf("unknown index error = %v", err)
		}

		projection, err := store.LoadProjection(ctx, "jp", "cards")
		if err != nil || strings.Join(projection.Keys, ",") != "3,1,2,1010201,2,"+masterdata.AutoRecordKey([]byte(`{"prefix":"no id","characterId":4}`))+",4" {
			t.Fatalf("cards projection keys = %v %v", projection, err)
		}
		if empty, err := store.LoadProjection(ctx, "jp", "costume3ds"); err != nil || empty.Len() != 0 {
			t.Fatalf("absent projection = %v %v", empty, err)
		}

		version, found, err := store.LoadRegionVersionPayload(ctx, "jp")
		if err != nil || !found || version.(map[string]any)["dataVersion"] != "1.0.0" {
			t.Fatalf("version payload = %v %v %v", version, found, err)
		}
		if has, err := store.HasEntityRecords(ctx, "jp", "versions"); err != nil || has {
			t.Fatalf("versions.json stored as records: %v %v", has, err)
		}
	})
}

func TestStoreContractRestoreReplacesRecords(t *testing.T) {
	forEachStore(t, func(t *testing.T, _ contractBackend, store contractStore) {
		ctx := context.Background()
		storeContractPayload(t, store)

		changed := map[string]any{
			"cards.json": rawRecords(t,
				`{"id":1,"characterId":2,"prefix":"first (changed)"}`,
				`{"id":5,"characterId":9,"prefix":"new"}`,
			),
			"cardEpisodes.json": []json.RawMessage{},
		}
		if err := store.StoreRegion(ctx, "jp", changed); err != nil {
			t.Fatalf("restore: %v", err)
		}

		cards, err := store.ListAll(ctx, "jp", "cards")
		if err != nil || joinedFieldValues(cards, "prefix") != "first (changed),new" {
			t.Fatalf("cards after restore = %s %v", joinedFieldValues(cards, "prefix"), err)
		}
		if _, found, _ := store.GetByID(ctx, "jp", "cards", "3"); found {
			t.Fatal("removed record still readable")
		}
		if has, _ := store.HasEntityRecords(ctx, "jp", "cardepisodes"); has {
			t.Fatal("emptied entity still has records")
		}
		episodes, err := store.ListByIndex(ctx, "jp", "cardepisodes", "cardId", [][]any{{1}})
		if err != nil || len(episodes[0]) != 0 {
			t.Fatalf("index of emptied entity = %v %v", episodes, err)
		}
		projection, err := store.LoadProjection(ctx, "jp", "cards")
		if err != nil || strings.Join(projection.Keys, ",") != "1,5" {
			t.Fatalf("projection after restore = %v %v", projection, err)
		}
		// Files the partial payload does not name are untouched.
		if gachas, err := store.ListAll(ctx, "jp", "gachas"); err != nil || len(gachas) != 2 {
			t.Fatalf("gachas after partial store = %d %v", len(gachas), err)
		}
	})
}

func TestStoreContractSkipsUnchangedSourceDigest(t *testing.T) {
	forEachStore(t, func(t *testing.T, _ contractBackend, store contractStore) {
		ctx := context.Background()
		first := map[string]any{"cards.json": rawRecords(t, `{"id":1,"prefix":"first"}`)}
		second := map[string]any{"cards.json": rawRecords(t, `{"id":1,"prefix":"second"}`)}

		if err := store.StoreRegionWithSourceDigests(ctx, "jp", first, map[string]string{"cards.json": "digest-1"}); err != nil {
			t.Fatalf("store first: %v", err)
		}
		// The same digest means the same source file: the store skips it.
		if err := store.StoreRegionWithSourceDigests(ctx, "jp", second, map[string]string{"cards.json": "digest-1"}); err != nil {
			t.Fatalf("store second: %v", err)
		}
		if record, _, _ := store.GetByID(ctx, "jp", "cards", "1"); record["prefix"] != "first" {
			t.Fatalf("unchanged digest was not skipped: %v", record)
		}

		if err := store.StoreRegionWithSourceDigests(masterdata.WithForceFullStore(ctx), "jp", second, map[string]string{"cards.json": "digest-1"}); err != nil {
			t.Fatalf("force store: %v", err)
		}
		if record, _, _ := store.GetByID(ctx, "jp", "cards", "1"); record["prefix"] != "second" {
			t.Fatalf("forced store was skipped: %v", record)
		}

		third := map[string]any{"cards.json": rawRecords(t, `{"id":1,"prefix":"third"}`)}
		if err := store.StoreRegionWithSourceDigests(ctx, "jp", third, map[string]string{"cards.json": "digest-3"}); err != nil {
			t.Fatalf("store third: %v", err)
		}
		if record, _, _ := store.GetByID(ctx, "jp", "cards", "1"); record["prefix"] != "third" {
			t.Fatalf("changed digest was skipped: %v", record)
		}
	})
}

func TestStoreContractRebuildsStaleDerivedData(t *testing.T) {
	forEachStore(t, func(t *testing.T, backend contractBackend, store contractStore) {
		ctx := context.Background()
		// No repeated keys here: a projection rebuilt from stored records
		// reads the record stored last under a repeated key, while sync
		// builds it from every occurrence, in every store.
		payload := contractPayload(t)
		payload["cards.json"] = rawRecords(t,
			`{"id":3,"characterId":1,"prefix":"third"}`,
			`{"id":1,"characterId":2,"prefix":"first"}`,
			`{"prefix":"no id","characterId":4}`,
		)
		if err := store.StoreRegion(ctx, "jp", payload); err != nil {
			t.Fatalf("store region: %v", err)
		}
		wantEpisodes, _ := store.ListByIndex(ctx, "jp", "cardepisodes", "cardId", [][]any{{1}, {2}})
		wantProjection, _ := store.LoadProjection(ctx, "jp", "cards")

		backend.markDerivedDataStale(t, store, "jp", "cardepisodes")
		backend.markDerivedDataStale(t, store, "jp", "cards")

		// Reads stay correct by scanning until the data is rebuilt.
		if got, err := store.ListByIndex(ctx, "jp", "cardepisodes", "cardId", [][]any{{1}, {2}}); err != nil || !reflect.DeepEqual(got, wantEpisodes) {
			t.Fatalf("stale index read = %v %v", got, err)
		}
		if got, err := store.LoadProjection(ctx, "jp", "cards"); err != nil || !reflect.DeepEqual(got, wantProjection) {
			t.Fatalf("stale projection read = %v %v", got, err)
		}

		rebuilt, err := store.EnsureDerivedEntityData(ctx, "jp")
		if err != nil || strings.Join(rebuilt, ",") != "cardepisodes,cards" {
			t.Fatalf("rebuilt = %v %v", rebuilt, err)
		}
		if again, err := store.EnsureDerivedEntityData(ctx, "jp"); err != nil || len(again) != 0 {
			t.Fatalf("second ensure rebuilt %v %v", again, err)
		}
		if got, err := store.ListByIndex(ctx, "jp", "cardepisodes", "cardId", [][]any{{1}, {2}}); err != nil || !reflect.DeepEqual(got, wantEpisodes) {
			t.Fatalf("rebuilt index read = %v %v", got, err)
		}
		if got, err := store.LoadProjection(ctx, "jp", "cards"); err != nil || !reflect.DeepEqual(got, wantProjection) {
			t.Fatalf("rebuilt projection read = %v %v", got, err)
		}
	})
}

func TestStoreContractEmptyRegion(t *testing.T) {
	forEachStore(t, func(t *testing.T, _ contractBackend, store contractStore) {
		ctx := context.Background()
		if all, err := store.ListAll(ctx, "kr", "cards"); err != nil || all == nil || len(all) != 0 {
			t.Fatalf("ListAll = %v %v", all, err)
		}
		if page, total, err := store.ListByPage(ctx, "kr", "cards", 1, 20); err != nil || page == nil || len(page) != 0 || total != 0 {
			t.Fatalf("ListByPage = %v %d %v", page, total, err)
		}
		if _, found, err := store.GetByID(ctx, "kr", "cards", "1"); err != nil || found {
			t.Fatalf("GetByID = %v %v", found, err)
		}
		if _, found, err := store.LoadRegionVersionPayload(ctx, "kr"); err != nil || found {
			t.Fatalf("version payload = %v %v", found, err)
		}
		if rebuilt, err := store.EnsureDerivedEntityData(ctx, "kr"); err != nil || len(rebuilt) != 0 {
			t.Fatalf("ensure = %v %v", rebuilt, err)
		}
		if err := store.StoreRegion(ctx, " ", map[string]any{}); err == nil {
			t.Fatal("store without region succeeded")
		}
	})
}

func joinedFieldValues(records []map[string]any, field string) string {
	values := make([]string, len(records))
	for index, record := range records {
		values[index] = fmt.Sprint(record[field])
	}
	return strings.Join(values, ",")
}
