package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BlobFile writes plaintext to a content-addressed Zstd file under root.
// Layout: <root>/sha256/<hh>/<sha>.bin.zst
// Returns the content SHA-256 (of plaintext) and compressed size.
func WriteBlobFile(root string, plaintext []byte) (sha string, compressedLen int, err error) {
	if len(plaintext) == 0 {
		return "", 0, nil
	}
	compressed, sha, err := DefaultCodec().Compress(plaintext)
	if err != nil {
		return "", 0, err
	}
	dir := filepath.Join(root, "sha256", sha[:2])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	path := filepath.Join(dir, sha+".bin") // zstd payload, legacy name
	if _, err := os.Stat(path); err == nil {
		return sha, len(compressed), nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, compressed, 0o644); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", 0, err
	}
	return sha, len(compressed), nil
}

// ReadBlobFile loads and decompresses a blob written by WriteBlobFile,
// or a legacy uncompressed <sha>.bin next to it.
func ReadBlobFile(root, sha string) ([]byte, error) {
	if len(sha) != 64 {
		return nil, fmt.Errorf("invalid blob hash")
	}
	candidates := []string{
		filepath.Join(root, "sha256", sha[:2], sha+".bin"),
		filepath.Join(root, "sha256", sha[:2], sha+".bin.zst"),
		filepath.Join(root, "sha256", sha[:2], sha[2:4], sha+".bin"),
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if isZstdFrame(data) || strings.HasSuffix(p, ".zst") {
			if out, err := DefaultCodec().Decompress(data); err == nil {
				return out, nil
			}
		}
		return data, nil
	}
	return nil, fmt.Errorf("blob %s does not exist", sha)
}

func isZstdFrame(b []byte) bool {
	// zstd magic: 0xFD2FB528 little-endian
	return len(b) >= 4 && b[0] == 0x28 && b[1] == 0xB5 && b[2] == 0x2F && b[3] == 0xFD
}
