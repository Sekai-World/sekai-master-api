package storage

import (
	"strconv"

	"sekai-master-api/internal/domain/masterdata"
)

// recordKeyer derives the record keys of one entity's records, fed in source
// order. Every store keys records through it, so they agree on keys. A
// composite-key record missing a key field falls back to an auto key, and a
// repeated fallback gets an occurrence suffix so it stays distinct.
type recordKeyer struct {
	entity              string
	fallbackOccurrences map[string]int
}

func newRecordKeyer(entity string) *recordKeyer {
	return &recordKeyer{entity: entity, fallbackOccurrences: make(map[string]int)}
}

// key returns the key of a decoded record whose JSON body is body.
func (keyer *recordKeyer) key(record map[string]any, body []byte) string {
	if !masterdata.UsesCompositeKey(keyer.entity) {
		return masterdata.RecordKey(record, body)
	}
	if key, ok := masterdata.CompositeRecordKey(keyer.entity, record); ok {
		return key
	}
	return keyer.fallbackKey(body)
}

// keyFromRaw returns the key of a record given as its JSON body.
func (keyer *recordKeyer) keyFromRaw(body []byte) string {
	if !masterdata.UsesCompositeKey(keyer.entity) {
		return masterdata.RecordKeyFromRaw(body)
	}
	if key, ok := masterdata.CompositeRecordKeyFromRaw(keyer.entity, body); ok {
		return key
	}
	return keyer.fallbackKey(body)
}

func (keyer *recordKeyer) fallbackKey(body []byte) string {
	baseKey := masterdata.AutoRecordKey(body)
	occurrence := keyer.fallbackOccurrences[baseKey]
	keyer.fallbackOccurrences[baseKey] = occurrence + 1
	if occurrence == 0 {
		return baseKey
	}
	return baseKey + ":" + strconv.Itoa(occurrence)
}
