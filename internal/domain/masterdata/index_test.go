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
	if version == "" || !reflect.DeepEqual(IndexNamesFromVersion(version), []string{"resourceBoxId,resourceBoxPurpose", "resourceType,resourceId"}) {
		t.Fatalf("expected the version to record the index names, got %q", version)
	}
	if version := IndexVersion("MusicOriginals"); version == "" || !reflect.DeepEqual(IndexNamesFromVersion(version), []string{"musicId"}) {
		t.Fatalf("expected the music originals version to record its musicId index, got %q", version)
	}
	if IndexVersion("musics") != "" || IndexNamesFromVersion("") != nil {
		t.Fatal("expected entities without indexes to have no version")
	}
}

func TestElementIndexKeysPairFieldsOfTheSameElement(t *testing.T) {
	var box map[string]any
	if err := json.Unmarshal([]byte(`{"id":1,"details":[
		{"resourceType":"stamp","resourceId":941},
		{"resourceType":"material","resourceId":7},
		{"resourceType":"stamp","resourceId":941},
		{"resourceType":"jewel","resourceQuantity":300}]}`), &box); err != nil {
		t.Fatal(err)
	}
	const index = "details[resourceType+resourceId]"

	stamp, _ := IndexLookupKey("stamp", int64(941))
	material, _ := IndexLookupKey("material", int64(7))
	if got := IndexKeys(box, index); !reflect.DeepEqual(got, []string{stamp, material}) {
		t.Fatalf("expected one distinct key per element with both fields, got %v", got)
	}
	// The multi-field form would also have paired the stamp with the material's ID.
	crossed, _ := IndexLookupKey("stamp", int64(7))
	for _, key := range IndexKeys(box, index) {
		if key == crossed {
			t.Fatalf("expected no key pairing one element's type with another's ID, got %q", key)
		}
	}
	if IndexKeys(map[string]any{"id": 1}, index) != nil || IndexKeys(map[string]any{"details": []any{}}, index) != nil {
		t.Fatal("expected no keys for a record without elements")
	}
}

func TestElementIndexKeysStayLinearInTheElements(t *testing.T) {
	details := make([]any, 0, 211)
	for id := range 211 {
		details = append(details, map[string]any{"resourceType": "material", "resourceId": float64(id)})
	}
	if got := len(IndexKeys(map[string]any{"details": details}, "details[resourceType+resourceId]")); got != 211 {
		t.Fatalf("expected one key per element, got %d", got)
	}
}

func TestParseElementIndexOnlyAcceptsTheElementForm(t *testing.T) {
	for _, index := range []string{"id", "a.b", "a,b", "details[x]", "[a+b]", "details[a+b],id", "details[a+b"} {
		if _, _, ok := parseElementIndex(index); ok {
			t.Fatalf("expected %q not to be an element index", index)
		}
	}
	path, fields, ok := parseElementIndex("a.details[x+y+z]")
	if !ok || !reflect.DeepEqual(path, []string{"a", "details"}) || !reflect.DeepEqual(fields, []string{"x", "y", "z"}) {
		t.Fatalf("unexpected parse %v %v %v", path, fields, ok)
	}
}
