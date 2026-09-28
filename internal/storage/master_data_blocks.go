package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"

	"sekai-master-api/internal/domain/masterdata"
)

// Record blocks (docs/postgres-master-data-store.md#blocks) hold an entity's
// records sorted by masterdata.BlockSortKey. A block is one zstd frame of a
// JSON array with one entry per record key:
//
//	[record_key, [position, ...], record]
//
// positions are the key's positions in the entity's source order (a key the
// source repeats has several, and every one reads the record stored last
// under it, as the Redis store does), and record is the stored JSON, byte for
// byte.

const (
	// masterBlockMaxRecords closes a block at this many record keys.
	masterBlockMaxRecords = 32
	// masterBlockMaxBytes closes a block once its records hold this much JSON.
	masterBlockMaxBytes = 64 << 10
)

// blockEntry is one record key of an entity with its record and positions.
type blockEntry struct {
	key       string
	sortKey   string
	positions []int
	record    []byte
}

// encodedBlock is one master_blocks row.
type encodedBlock struct {
	firstKey string
	body     []byte
}

// buildBlocks sorts entries by sort key and encodes them into blocks.
func buildBlocks(entries []blockEntry) ([]encodedBlock, error) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].sortKey < entries[j].sortKey })

	blocks := make([]encodedBlock, 0, len(entries)/masterBlockMaxRecords+1)
	start := 0
	size := 0
	for index := range entries {
		size += len(entries[index].record)
		if index+1-start < masterBlockMaxRecords && size < masterBlockMaxBytes && index+1 < len(entries) {
			continue
		}
		body, err := encodeBlock(entries[start : index+1])
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, encodedBlock{firstKey: entries[start].sortKey, body: body})
		start = index + 1
		size = 0
	}
	return blocks, nil
}

func encodeBlock(entries []blockEntry) ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('[')
	for index, entry := range entries {
		if index > 0 {
			buffer.WriteByte(',')
		}
		key, err := json.Marshal(entry.key)
		if err != nil {
			return nil, fmt.Errorf("encode block key %q: %w", entry.key, err)
		}
		buffer.WriteByte('[')
		buffer.Write(key)
		buffer.WriteString(",[")
		for positionIndex, position := range entry.positions {
			if positionIndex > 0 {
				buffer.WriteByte(',')
			}
			buffer.WriteString(strconv.Itoa(position))
		}
		buffer.WriteString("],")
		buffer.Write(entry.record)
		buffer.WriteByte(']')
	}
	buffer.WriteByte(']')
	return compressBlob(buffer.Bytes()), nil
}

// decodedEntry is one entry of a decoded block.
type decodedEntry struct {
	key       string
	positions []int
	record    json.RawMessage
}

func decodeBlock(body []byte) ([]decodedEntry, error) {
	plain, err := decompressBlob(body)
	if err != nil {
		return nil, fmt.Errorf("decompress block: %w", err)
	}

	var rows [][]json.RawMessage
	if err := json.Unmarshal(plain, &rows); err != nil {
		return nil, fmt.Errorf("decode block: %w", err)
	}
	entries := make([]decodedEntry, len(rows))
	for index, row := range rows {
		if len(row) != 3 {
			return nil, fmt.Errorf("decode block: entry %d has %d fields, want 3", index, len(row))
		}
		if err := json.Unmarshal(row[0], &entries[index].key); err != nil {
			return nil, fmt.Errorf("decode block key: %w", err)
		}
		if err := json.Unmarshal(row[1], &entries[index].positions); err != nil {
			return nil, fmt.Errorf("decode block positions: %w", err)
		}
		entries[index].record = row[2]
	}
	return entries, nil
}

// decodeBlocks decodes bodies on every available core and returns their
// entries, block by block.
func decodeBlocks(bodies [][]byte) ([][]decodedEntry, error) {
	decoded := make([][]decodedEntry, len(bodies))
	err := parallelEach(len(bodies), func(index int) error {
		entries, err := decodeBlock(bodies[index])
		decoded[index] = entries
		return err
	})
	return decoded, err
}

// decodeRecords unmarshals raw records into maps on every available core,
// the way the Redis store decodes them. A nil raw record stays nil.
func decodeRecords(raws []json.RawMessage) ([]map[string]any, error) {
	records := make([]map[string]any, len(raws))
	err := parallelEach(len(raws), func(index int) error {
		if raws[index] == nil {
			return nil
		}
		return json.Unmarshal(raws[index], &records[index])
	})
	if err != nil {
		return nil, fmt.Errorf("decode record: %w", err)
	}
	return records, nil
}

// parallelEach runs fn for every index in [0, count) on up to GOMAXPROCS
// workers and returns the first error.
func parallelEach(count int, fn func(index int) error) error {
	if count == 0 {
		return nil
	}
	workers := min(runtime.GOMAXPROCS(0), count)
	var next atomic.Int64
	var firstErr error
	var errOnce sync.Once
	var wait sync.WaitGroup
	for range workers {
		wait.Go(func() {
			for {
				index := int(next.Add(1)) - 1
				if index >= count {
					return
				}
				if err := fn(index); err != nil {
					errOnce.Do(func() { firstErr = err })
					return
				}
			}
		})
	}
	wait.Wait()
	return firstErr
}

// encodeOrderKeys stores an entity's record keys in source order.
func encodeOrderKeys(keys []string) ([]byte, error) {
	body, err := json.Marshal(keys)
	if err != nil {
		return nil, fmt.Errorf("encode order keys: %w", err)
	}
	return compressBlob(body), nil
}

func decodeOrderKeys(body []byte) ([]string, error) {
	plain, err := decompressBlob(body)
	if err != nil {
		return nil, fmt.Errorf("decompress order keys: %w", err)
	}
	var keys []string
	if err := json.Unmarshal(plain, &keys); err != nil {
		return nil, fmt.Errorf("decode order keys: %w", err)
	}
	return keys, nil
}

func compressBlob(plain []byte) []byte {
	encoder := redisEntityEncoderPool.Get().(*zstd.Encoder)
	defer redisEntityEncoderPool.Put(encoder)
	return encoder.EncodeAll(plain, nil)
}

func decompressBlob(body []byte) ([]byte, error) {
	return redisEntityDecoder.DecodeAll(body, nil)
}

// blockEntries groups an entity's records, in source order, by record key.
// Each key keeps the record stored last under it and every position it
// occupies.
func blockEntries(keys []string, records [][]byte) []blockEntry {
	indexByKey := make(map[string]int, len(keys))
	entries := make([]blockEntry, 0, len(keys))
	for position, key := range keys {
		index, seen := indexByKey[key]
		if !seen {
			index = len(entries)
			indexByKey[key] = index
			entries = append(entries, blockEntry{key: key, sortKey: masterdata.BlockSortKey(key)})
		}
		entries[index].positions = append(entries[index].positions, position)
		entries[index].record = records[position]
	}
	return entries
}
