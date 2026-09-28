package responsecache

import (
	"encoding/binary"
	"errors"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// entry is a cached response.
type entry struct {
	status      int
	contentType string
	body        []byte
}

// Encoded layout: version byte, flags byte (bit 0: zstd body), status as
// uint16, content type as a uint16 length and bytes, then the body.
const (
	entryVersion     byte = 1
	entryFlagZstd    byte = 1
	compressMinBytes      = 1 << 10
	maxEntryBodySize      = 64 << 20
)

var errBadEntry = errors.New("malformed response cache entry")

var zstdEncoders = sync.Pool{New: func() any {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		panic(err)
	}
	return encoder
}}

var zstdDecoder = func() *zstd.Decoder {
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(maxEntryBodySize))
	if err != nil {
		panic(err)
	}
	return decoder
}()

func encodeEntry(value entry) []byte {
	flags := byte(0)
	body := value.body
	if len(body) >= compressMinBytes {
		encoder := zstdEncoders.Get().(*zstd.Encoder)
		body = encoder.EncodeAll(body, nil)
		zstdEncoders.Put(encoder)
		flags |= entryFlagZstd
	}
	contentType := value.contentType
	if len(contentType) > 0xffff {
		contentType = contentType[:0xffff]
	}
	out := make([]byte, 0, 6+len(contentType)+len(body))
	out = append(out, entryVersion, flags)
	out = binary.BigEndian.AppendUint16(out, uint16(value.status))
	out = binary.BigEndian.AppendUint16(out, uint16(len(contentType)))
	out = append(out, contentType...)
	return append(out, body...)
}

func decodeEntry(raw []byte) (entry, error) {
	if len(raw) < 6 || raw[0] != entryVersion {
		return entry{}, errBadEntry
	}
	flags := raw[1]
	status := int(binary.BigEndian.Uint16(raw[2:4]))
	typeLength := int(binary.BigEndian.Uint16(raw[4:6]))
	if len(raw) < 6+typeLength {
		return entry{}, errBadEntry
	}
	value := entry{status: status, contentType: string(raw[6 : 6+typeLength])}
	body := raw[6+typeLength:]
	if flags&entryFlagZstd != 0 {
		decoded, err := zstdDecoder.DecodeAll(body, nil)
		if err != nil {
			return entry{}, errBadEntry
		}
		body = decoded
	}
	value.body = body
	return value, nil
}
