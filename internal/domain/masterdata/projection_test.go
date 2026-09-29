package masterdata

import (
	"encoding/json"
	"reflect"
	"testing"
)

func decodeRecords(t *testing.T, body string) []map[string]any {
	t.Helper()
	var records []map[string]any
	if err := json.Unmarshal([]byte(body), &records); err != nil {
		t.Fatal(err)
	}
	return records
}

func TestProjectionRowsMatchDecodedRecordsAfterRoundTrip(t *testing.T) {
	records := decodeRecords(t, `[
		{"id":1,"gachaType":"normal","name":"A","assetbundleName":"ab_1","startAt":1700000000000,"endAt":1700000001000,"gachaDetails":[{"cardId":1}]},
		{"id":2,"gachaType":"beginner","name":"B","startAt":1.5,"endAt":null},
		{"id":3,"gachaType":"normal","name":"C","assetbundleName":"ab_3","startAt":"2020-01-02T03:04:05Z"}
	]`)
	projection := BuildProjection("gachas", []string{"1", "2", "3"}, records)

	body, err := EncodeProjection(projection)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeProjection(body)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.Len() != 3 || !reflect.DeepEqual(decoded.Keys, []string{"1", "2", "3"}) {
		t.Fatalf("expected three rows keyed in stored order, got %v", decoded.Keys)
	}
	for row, record := range records {
		want := map[string]any{}
		for _, field := range ProjectionFields("gachas") {
			if value, ok := record[field]; ok && value != nil {
				want[field] = value
			}
		}
		if got := decoded.Row(row); !reflect.DeepEqual(got, want) {
			t.Fatalf("row %d: expected %v, got %v", row, want, got)
		}
	}

	kinds := map[string]ColumnKind{}
	for field, column := range decoded.Columns {
		kinds[field] = column.Kind
	}
	want := map[string]ColumnKind{
		"id": ColumnInt, "gachaType": ColumnString, "name": ColumnString, "assetbundleName": ColumnString,
		"startAt": ColumnRaw, "endAt": ColumnInt,
	}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("expected the narrowest column kinds, got %v", kinds)
	}
}

func TestProjectionColumnKindsWidenOnlyAsNeeded(t *testing.T) {
	for _, test := range []struct {
		values []any
		want   ColumnKind
	}{
		{values: []any{nil, nil}, want: ColumnEmpty},
		{values: []any{float64(1), nil, float64(2)}, want: ColumnInt},
		{values: []any{float64(1), 0.5}, want: ColumnFloat},
		{values: []any{"a", nil}, want: ColumnString},
		{values: []any{true, false}, want: ColumnBool},
		{values: []any{"a", float64(1)}, want: ColumnRaw},
		{values: []any{[]any{float64(1)}}, want: ColumnRaw},
	} {
		column := buildProjectionColumn(test.values)
		if column.Kind != test.want {
			t.Errorf("%v: expected kind %d, got %d", test.values, test.want, column.Kind)
		}
		for row, value := range test.values {
			projection := &Projection{Keys: make([]string, len(test.values)), Columns: map[string]*ProjectionColumn{"f": column}}
			got, ok := projection.Value("f", row)
			if ok != (value != nil) || (ok && !reflect.DeepEqual(got, value)) {
				t.Errorf("%v row %d: expected %v, got %v (%v)", test.values, row, value, got, ok)
			}
		}
	}
}

func TestProjectionVersionNamesItsFields(t *testing.T) {
	if ProjectionVersion("Gachas") != projectionLayoutVersion+":id,gachaType,name,assetbundleName,startAt,endAt" {
		t.Fatalf("unexpected gacha projection version %q", ProjectionVersion("Gachas"))
	}
	if ProjectionVersion("cardepisodes") != "" || NewProjectionBuilder("cardepisodes") != nil {
		t.Fatal("expected entities without projections to have no version or builder")
	}
}
