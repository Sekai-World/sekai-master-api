package masterdata

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestIndexKeysFollowNestedArraysAndCanonicalizeNumbers(t *testing.T) {
	var gacha map[string]any
	if err := json.Unmarshal([]byte(`{"id":1,"gachaPickups":[{"cardId":1010201},{"cardId":7},{"cardId":7}]}`), &gacha); err != nil {
		t.Fatal(err)
	}
	if got := IndexKeys(gacha, "gachaPickups.cardId"); !reflect.DeepEqual(got, []string{"1010201", "7"}) {
		t.Fatalf("expected distinct decimal pickup card keys, got %v", got)
	}
	if got := IndexKeys(map[string]any{"id": 1}, "gachaPickups.cardId"); got != nil {
		t.Fatalf("expected no keys for a record without the field, got %v", got)
	}
}

func TestMultiFieldIndexKeysMatchLookupKeys(t *testing.T) {
	detail := map[string]any{"resourceBoxId": float64(5), "resourceBoxPurpose": "mission_reward", "seq": float64(1)}
	keys := IndexKeys(detail, "resourceBoxId,resourceBoxPurpose")
	lookup, ok := IndexLookupKey(int64(5), " mission_reward ")
	if !ok || !reflect.DeepEqual(keys, []string{lookup}) {
		t.Fatalf("expected record keys %v to equal lookup key %q", keys, lookup)
	}
	// Length prefixes keep "1"+"23" and "12"+"3" apart.
	left, _ := IndexLookupKey("1", "23")
	right, _ := IndexLookupKey("12", "3")
	if left == right {
		t.Fatalf("expected distinct multi-field keys, both were %q", left)
	}
	if single, _ := IndexLookupKey(float64(1010201)); single != "1010201" {
		t.Fatalf("expected single-field keys to stay readable, got %q", single)
	}
}

func TestIndexVersionNamesItsIndexes(t *testing.T) {
	version := IndexVersion("ResourceBoxDetails")
	if version == "" || !reflect.DeepEqual(IndexNamesFromVersion(version), []string{"resourceBoxId,resourceBoxPurpose"}) {
		t.Fatalf("expected the version to record the index names, got %q", version)
	}
	if IndexVersion("musics") != "" || IndexNamesFromVersion("") != nil {
		t.Fatal("expected entities without indexes to have no version")
	}
}
