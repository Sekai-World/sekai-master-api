package masterdata

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

func TestBlockSortKeyEncoding(t *testing.T) {
	cases := []struct {
		recordKey string
		want      string
	}{
		{"0", "000000000000000000000"},
		{"7", "000000000000000000007"},
		{"1010201", "000000000000001010201"},
		{"18446744073709551615", "018446744073709551615"},
		{"99999999999999999999", "099999999999999999999"},
		// Not canonical decimals: too long, leading zero, sign, fraction.
		{"100000000000000000000", "1100000000000000000000"},
		{"007", "1007"},
		{"-1", "1-1"},
		{"1.5", "11.5"},
		{"", "1"},
		{"auto:ab12", "1auto:ab12"},
		{"composite:v1:13:resourceboxes2:id1:5", "1composite:v1:13:resourceboxes2:id1:5"},
	}
	for _, tc := range cases {
		if got := BlockSortKey(tc.recordKey); got != tc.want {
			t.Errorf("BlockSortKey(%q) = %q, want %q", tc.recordKey, got, tc.want)
		}
	}
}

func TestBlockSortKeyOrdersNumbersNumericallyBeforeOtherKeys(t *testing.T) {
	recordKeys := []string{"10", "auto:ff", "2", "007", "18446744073709551615", "0", "composite:v1:1:a", "100000000000000000000", "9"}
	want := []string{"0", "2", "9", "10", "18446744073709551615", "007", "100000000000000000000", "auto:ff", "composite:v1:1:a"}

	sorted := append([]string(nil), recordKeys...)
	sort.Slice(sorted, func(i, j int) bool { return BlockSortKey(sorted[i]) < BlockSortKey(sorted[j]) })
	if strings.Join(sorted, ",") != strings.Join(want, ",") {
		t.Fatalf("sorted by block sort key = %v, want %v", sorted, want)
	}

	seen := map[string]string{}
	for _, recordKey := range recordKeys {
		sortKey := BlockSortKey(recordKey)
		if previous, duplicate := seen[sortKey]; duplicate {
			t.Fatalf("record keys %q and %q share sort key %q", previous, recordKey, sortKey)
		}
		seen[sortKey] = recordKey
	}
}

func TestCompositeRecordKeyMatchesDecodedAndRawRecords(t *testing.T) {
	body := []byte(`{"resourceBoxId":1010201,"resourceBoxPurpose":"mission_reward","seq":2,"quantity":1}`)
	var record map[string]any
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatalf("decode record: %v", err)
	}

	fromRaw, ok := CompositeRecordKeyFromRaw("resourceBoxDetails", body)
	if !ok {
		t.Fatal("raw record has no composite key")
	}
	fromDecoded, ok := CompositeRecordKey("resourceboxdetails", record)
	if !ok {
		t.Fatal("decoded record has no composite key")
	}
	want := "composite:v1:18:resourceboxdetails13:resourceBoxId7:101020118:resourceBoxPurpose14:mission_reward3:seq1:2"
	if fromRaw != want || fromDecoded != want {
		t.Fatalf("composite keys = %q (raw), %q (decoded), want %q", fromRaw, fromDecoded, want)
	}

	if _, ok := CompositeRecordKeyFromRaw("resourceboxdetails", []byte(`{"resourceBoxId":1,"seq":2}`)); ok {
		t.Fatal("record missing a key field produced a composite key")
	}
	if _, ok := CompositeRecordKey("cards", record); ok {
		t.Fatal("id-keyed entity produced a composite key")
	}
	if !UsesCompositeKey("ResourceBoxes") || UsesCompositeKey("cards") {
		t.Fatal("UsesCompositeKey does not match the composite-key entities")
	}
	if len(CompositeKeyEntities()) != 3 {
		t.Fatalf("composite-key entities = %v, want 3", CompositeKeyEntities())
	}
}

func TestRecordKeyUsesDecimalIDOrAutoKey(t *testing.T) {
	body := []byte(`{"id":1010201,"name":"x"}`)
	var record map[string]any
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if got := RecordKey(record, body); got != "1010201" {
		t.Fatalf("RecordKey = %q, want decimal id", got)
	}
	if got := RecordKeyFromRaw(body); got != "1010201" {
		t.Fatalf("RecordKeyFromRaw = %q, want decimal id", got)
	}

	noID := []byte(`{"name":"x"}`)
	auto := AutoRecordKey(noID)
	if !strings.HasPrefix(auto, AutoKeyPrefix) || len(auto) != len(AutoKeyPrefix)+64 {
		t.Fatalf("AutoRecordKey = %q, want auto: + sha256 hex", auto)
	}
	if got := RecordKeyFromRaw(noID); got != auto {
		t.Fatalf("RecordKeyFromRaw without id = %q, want %q", got, auto)
	}
	if got := RecordKeyFromRaw([]byte(`{"id":null,"name":"x"}`)); !strings.HasPrefix(got, AutoKeyPrefix) {
		t.Fatalf("RecordKeyFromRaw with null id = %q, want an auto key", got)
	}
	if got := RecordKey(nil, nil); got != "" {
		t.Fatalf("RecordKey without id or body = %q, want empty", got)
	}
}
