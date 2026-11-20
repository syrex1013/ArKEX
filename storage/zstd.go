// Package storage provides Zstd compression with trained dictionaries,
// FastCDC content-addressed storage, and the Storage Governor.
package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"
)

const (
	DictHTTP = "http"
	DictJSON = "json"
	DictBin  = "binary"
)

// Codec holds a trained Zstd encoder/decoder pair.
type Codec struct {
	enc *zstd.Encoder
	dec *zstd.Decoder
	mu  sync.Mutex
}

var (
	codecOnce  sync.Once
	defaultCod *Codec
)

// DefaultCodec returns the shared codec (no dictionary, default level 3).
func DefaultCodec() *Codec {
	codecOnce.Do(func() {
		enc, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
		dec, _ := zstd.NewReader(nil)
		defaultCod = &Codec{enc: enc, dec: dec}
	})
	return defaultCod
}

// NewCodec creates a codec with an optional pre-trained Zstd dictionary.
func NewCodec(dict []byte, level zstd.EncoderLevel) (*Codec, error) {
	var opts []zstd.EOption
	opts = append(opts, zstd.WithEncoderLevel(level))
	if len(dict) > 0 {
		opts = append(opts, zstd.WithEncoderDict(dict))
	}
	enc, err := zstd.NewWriter(nil, opts...)
	if err != nil {
		return nil, fmt.Errorf("zstd encoder: %w", err)
	}
	var dOpts []zstd.DOption
	if len(dict) > 0 {
		dOpts = append(dOpts, zstd.WithDecoderDicts(dict))
	}
	dec, err := zstd.NewReader(nil, dOpts...)
	if err != nil {
		return nil, fmt.Errorf("zstd decoder: %w", err)
	}
	return &Codec{enc: enc, dec: dec}, nil
}

// Compress compresses src and returns (compressed, sha256hex, error).
func (c *Codec) Compress(src []byte) ([]byte, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	compressed := c.enc.EncodeAll(src, nil)
	sum := sha256.Sum256(src)
	return compressed, hex.EncodeToString(sum[:]), nil
}

// Decompress decompresses src.
func (c *Codec) Decompress(src []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.dec.Reset(bytes.NewReader(src)); err != nil {
		return nil, err
	}
	out, err := io.ReadAll(c.dec)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// MustCompress panics on error; suitable for init-time static data.
func MustCompress(data []byte) []byte {
	out, _, err := DefaultCodec().Compress(data)
	if err != nil {
		panic(err)
	}
	return out
}

// SHA256Hex returns the hex SHA-256 of data without compressing.
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
