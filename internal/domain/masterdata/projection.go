package masterdata

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// projectionLayoutVersion tags stored projections. Bump it when the encoding
// changes, so the next sync rebuilds every projection.
const projectionLayoutVersion = "1"

// entityProjections lists, per entity, the fields its list projection keeps:
// everything list endpoints filter, sort, and spoiler-check on, and for small
// lists everything they return. Sync builds the projection from the records it
// stores, so a list request decodes one compact value instead of the entity.
var entityProjections = map[string][]string{
	"cards": {
		"id", "seq", "characterId", "cardRarityType", "attr", "supportUnit", "skillId",
		"cardSkillName", "cardSupplyId", "prefix", "assetbundleName", "gachaPhrase",
		"flavorText", "releaseAt", "releastAt", "publishedAt", "startAt",
		"archivePublishedAt", "initialSpecialTrainingStatus",
	},
	"costume3dgroups": {
		"groupId", "colorId", "costume3dColorId", "partType", "costume3dPartType", "seq",
		"name", "designer", "characterId", "rarity", "costume3dRarity", "type",
		"costume3dType", "assetbundleName", "publishedAt",
	},
	"costume3ds": {
		"id", "groupId", "costume3dGroupId", "colorId", "costume3dColorId", "partType",
		"costume3dPartType", "seq", "name", "designer", "characterId", "rarity",
		"costume3dRarity", "type", "costume3dType", "assetbundleName", "publishedAt",
	},
	// The event window CurrentEvent compares; the events list does not use it yet.
	"events": {"id", "startAt", "closedAt"},
	"gachas": {"id", "gachaType", "name", "assetbundleName", "startAt", "endAt"},
	// What MySekai music records show of their music and hide as spoilers.
	"musics":           {"id", "title", "assetbundleName", "publishedAt"},
	"musicsoundtracks": {"id", "seq", "title", "musicSoundTrackCategoryId", "assetbundleName", "assetbundleFileName"},
	"mysekaifixtures": {
		"id", "seq", "name", "pronunciation", "mysekaiFixtureType", "mysekaiFixtureMainGenreId",
		"mysekaiFixtureSubGenreId", "mysekaiSettableLayoutType", "gridSize", "mysekaiFixtureTagGroup",
		"assetbundleName",
	},
	"mysekaimaterials": {
		"id", "seq", "name", "pronunciation", "description", "mysekaiMaterialType",
		"mysekaiMaterialRarityType", "iconAssetbundleName", "mysekaiSiteIds",
	},
	"mysekaimusicrecords": {"id", "mysekaiMusicTrackType", "externalId"},
	"stamps": {
		"id", "seq", "stampType", "name", "assetbundleName", "characterId1", "characterId2",
		"characterId3", "characterId4", "characterId5", "gameCharacterUnitId", "archivePublishedAt",
		"description",
	},
	"mysekaishops": {
		"id", "seq", "mysekaiShopType", "resourceBoxId", "mysekaiShopExchangeLimitType",
		"mysekaiShopExchangeLimitValue",
	},
}

// ProjectionFields returns the fields entity's list projection keeps, or nil
// when the entity has no projection.
func ProjectionFields(entity string) []string {
	return entityProjections[normalizeEntity(entity)]
}

// ProjectionVersion identifies entity's projection definition. Storage records
// it next to the projection; a mismatch means the projection must be rebuilt.
// Entities without a projection have an empty version.
func ProjectionVersion(entity string) string {
	fields := ProjectionFields(entity)
	if len(fields) == 0 {
		return ""
	}
	return projectionLayoutVersion + ":" + strings.Join(fields, ",")
}

// ProjectedEntities returns the entities that define a list projection.
func ProjectedEntities() []string {
	entities := make([]string, 0, len(entityProjections))
	for entity := range entityProjections {
		entities = append(entities, entity)
	}
	sort.Strings(entities)
	return entities
}

// ColumnKind is how a projection column stores its values.
type ColumnKind uint8

const (
	// ColumnEmpty holds no values: no record has the field.
	ColumnEmpty ColumnKind = iota
	ColumnInt
	ColumnFloat
	ColumnString
	ColumnBool
	// ColumnRaw holds each value's JSON text, for fields of mixed or nested type.
	ColumnRaw
)

// ProjectionColumn stores one field for every row in the slice its kind uses.
// Missing marks rows whose record lacks the field or holds null; it is nil
// when every row has a value.
type ProjectionColumn struct {
	Kind    ColumnKind
	Missing []bool
	Ints    []int64
	Floats  []float64
	Strings []string
	Bools   []bool
	Raw     []string
}

// Projection holds selected fields of every record of an entity by column, in
// stored order. Keys are the records' storage keys, for batched reads.
type Projection struct {
	Keys    []string
	Columns map[string]*ProjectionColumn
}

// Len returns the number of rows.
func (projection *Projection) Len() int {
	if projection == nil {
		return 0
	}
	return len(projection.Keys)
}

// Value returns a row's field as JSON decoding would produce it (float64,
// string, bool, or a decoded raw value), and whether the row has it.
func (projection *Projection) Value(field string, row int) (any, bool) {
	if projection == nil {
		return nil, false
	}
	column := projection.Columns[field]
	if column == nil || column.Kind == ColumnEmpty || (column.Missing != nil && column.Missing[row]) {
		return nil, false
	}
	switch column.Kind {
	case ColumnInt:
		return float64(column.Ints[row]), true
	case ColumnFloat:
		return column.Floats[row], true
	case ColumnString:
		return column.Strings[row], true
	case ColumnBool:
		return column.Bools[row], true
	default:
		var value any
		if err := json.Unmarshal([]byte(column.Raw[row]), &value); err != nil {
			return nil, false
		}
		return value, true
	}
}

// Row returns a row's present fields as a record map.
func (projection *Projection) Row(row int) map[string]any {
	record := make(map[string]any, len(projection.Columns))
	for field := range projection.Columns {
		if value, ok := projection.Value(field, row); ok {
			record[field] = value
		}
	}
	return record
}

// Rows returns every row as a record map, in stored order. Use it for small
// entities; large ones should read columns directly.
func (projection *Projection) Rows() []map[string]any {
	rows := make([]map[string]any, projection.Len())
	for row := range rows {
		rows[row] = projection.Row(row)
	}
	return rows
}

// ProjectionBuilder collects records into a projection.
type ProjectionBuilder struct {
	fields []string
	keys   []string
	values map[string][]any
}

// NewProjectionBuilder returns a builder for entity's projection, or nil when
// the entity has none.
func NewProjectionBuilder(entity string) *ProjectionBuilder {
	fields := ProjectionFields(entity)
	if len(fields) == 0 {
		return nil
	}
	values := make(map[string][]any, len(fields))
	for _, field := range fields {
		values[field] = nil
	}
	return &ProjectionBuilder{fields: fields, values: values}
}

// Add appends a record stored under key.
func (builder *ProjectionBuilder) Add(key string, record map[string]any) {
	builder.keys = append(builder.keys, key)
	for _, field := range builder.fields {
		builder.values[field] = append(builder.values[field], record[field])
	}
}

// Build returns the projection, storing each column in the narrowest kind
// that holds all of its values.
func (builder *ProjectionBuilder) Build() *Projection {
	projection := &Projection{Keys: builder.keys, Columns: make(map[string]*ProjectionColumn, len(builder.fields))}
	if projection.Keys == nil {
		projection.Keys = []string{}
	}
	for _, field := range builder.fields {
		projection.Columns[field] = buildProjectionColumn(builder.values[field])
	}
	return projection
}

func buildProjectionColumn(values []any) *ProjectionColumn {
	column := &ProjectionColumn{Kind: projectionColumnKind(values)}
	if column.Kind == ColumnEmpty {
		return column
	}

	for row, value := range values {
		if value == nil {
			if column.Missing == nil {
				column.Missing = make([]bool, len(values))
			}
			column.Missing[row] = true
		}
	}
	switch column.Kind {
	case ColumnInt:
		column.Ints = make([]int64, len(values))
		for row, value := range values {
			if number, ok := value.(float64); ok {
				column.Ints[row] = int64(number)
			}
		}
	case ColumnFloat:
		column.Floats = make([]float64, len(values))
		for row, value := range values {
			column.Floats[row], _ = value.(float64)
		}
	case ColumnString:
		column.Strings = make([]string, len(values))
		for row, value := range values {
			column.Strings[row], _ = value.(string)
		}
	case ColumnBool:
		column.Bools = make([]bool, len(values))
		for row, value := range values {
			column.Bools[row], _ = value.(bool)
		}
	case ColumnRaw:
		column.Raw = make([]string, len(values))
		for row, value := range values {
			if value != nil {
				body, _ := json.Marshal(value)
				column.Raw[row] = string(body)
			}
		}
	}
	return column
}

func projectionColumnKind(values []any) ColumnKind {
	kind := ColumnEmpty
	for _, value := range values {
		var valueKind ColumnKind
		switch typed := value.(type) {
		case nil:
			continue
		case float64:
			valueKind = ColumnFloat
			if typed == math.Trunc(typed) && math.Abs(typed) < 1<<53 {
				valueKind = ColumnInt
			}
		case string:
			valueKind = ColumnString
		case bool:
			valueKind = ColumnBool
		default:
			return ColumnRaw
		}

		switch {
		case kind == ColumnEmpty || kind == valueKind:
			kind = valueKind
		case (kind == ColumnInt && valueKind == ColumnFloat) || (kind == ColumnFloat && valueKind == ColumnInt):
			kind = ColumnFloat
		default:
			return ColumnRaw
		}
	}
	return kind
}

// EncodeProjection serializes a projection for storage.
func EncodeProjection(projection *Projection) ([]byte, error) {
	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(projection); err != nil {
		return nil, fmt.Errorf("encode projection: %w", err)
	}
	return buffer.Bytes(), nil
}

// DecodeProjection reads a projection written by EncodeProjection.
func DecodeProjection(body []byte) (*Projection, error) {
	var projection Projection
	if err := gob.NewDecoder(bytes.NewReader(body)).Decode(&projection); err != nil {
		return nil, fmt.Errorf("decode projection: %w", err)
	}
	if projection.Keys == nil {
		projection.Keys = []string{}
	}
	if projection.Columns == nil {
		projection.Columns = map[string]*ProjectionColumn{}
	}
	return &projection, nil
}

// BuildProjection builds entity's projection from records stored under keys,
// for callers that already hold the records.
func BuildProjection(entity string, keys []string, records []map[string]any) *Projection {
	builder := NewProjectionBuilder(entity)
	if builder == nil {
		return &Projection{Keys: []string{}, Columns: map[string]*ProjectionColumn{}}
	}
	for position, record := range records {
		if record != nil {
			builder.Add(keys[position], record)
		}
	}
	return builder.Build()
}
