package syncbundlebuild

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/DeanStr/readyci-cli/pkg/syncbundle"
)

type sourceContentMode uint8

const (
	retainSourceContent sourceContentMode = iota
	hashSourceContent
)

type sourceContent struct {
	size   int64
	data   []byte
	digest [sha256.Size]byte
}

// Both modes consume the selected bytes. Only packaging needs to retain them;
// path previews compare fixed-size hashes instead of buffering entire files.
func readSourceContent(reader io.Reader, size int64, mode sourceContentMode) (sourceContent, error) {
	if size < 0 || size > int64(int(^uint(0)>>1)) {
		return sourceContent{}, fmt.Errorf("unsupported source size %d", size)
	}
	content := sourceContent{size: size}
	if mode == retainSourceContent {
		content.data = make([]byte, int(size))
		_, err := io.ReadFull(reader, content.data)
		return content, err
	}
	hash := sha256.New()
	if _, err := io.CopyN(hash, reader, size); err != nil {
		return sourceContent{}, err
	}
	copy(content.digest[:], hash.Sum(nil))
	return content, nil
}

func plannedEntriesEqual(left, right plannedEntry, mode sourceContentMode) bool {
	if left.entry != right.entry {
		return false
	}
	if left.entry.Type == syncbundle.EntryTypeSymlink {
		return true
	}
	if mode == retainSourceContent {
		return bytes.Equal(left.data, right.data)
	}
	return left.digest == right.digest
}
