package session

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/yurika0211/luckyagent-runtime/provider"
)

const (
	initialMessageIndexStride = 128
	maxMessageIndexEntries    = 8192
)

type messageOffset struct {
	message int
	bytePos int64
	line    int
}

// messagePageIndex is immutable once published. It retains offsets, never
// message bodies. A file fingerprint prevents stale pages after replacement.
type messagePageIndex struct {
	info    os.FileInfo
	total   int
	stride  int
	offsets []messageOffset
}

var errMessagePageComplete = errors.New("message page complete")

func readMessagesJSONLPage(path string, limit, offset int, cached *messagePageIndex) ([]provider.Message, int, bool, *messagePageIndex, error) {
	return readMessagesJSONLPageFrom(path, limit, offset, cached, 0, 0, 0, 0)
}

// readMessagesJSONLPageFrom can seed a cold page from a durable tail offset.
// tailStart is the message ordinal at tailOffset, and tailCount is the durable
// message count recorded with that file size. A page before the recorded
// window, or a file whose size no longer matches, scans from the head.
func readMessagesJSONLPageFrom(path string, limit, offset int, cached *messagePageIndex, tailOffset, tailSize int64, tailStart, tailCount int) ([]provider.Message, int, bool, *messagePageIndex, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, false, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, false, nil, err
	}
	start := pageStart(tailCount, limit, offset)
	end := tailCount - offset
	if end < 0 {
		end = 0
	}
	if tailStart < 0 {
		tailStart = 0
	}
	if cached == nil && tailSize > 0 && tailSize != info.Size() {
		tailCount = 0
	}
	if cached == nil && tailCount > 0 && tailSize == info.Size() && start >= tailStart && end <= tailCount {
		page, err := readJSONLRange(f, tailOffset, tailStart, start, end)
		if err == nil {
			return page, tailCount, start > 0, nil, nil
		}
		if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
			return nil, 0, false, nil, seekErr
		}
	}
	index := cached
	if index == nil || !os.SameFile(index.info, info) || index.info.Size() != info.Size() || !index.info.ModTime().Equal(info.ModTime()) {
		index = &messagePageIndex{info: info, stride: initialMessageIndexStride}
		err = walkMessagesJSONL(f, 0, 1, func(_ provider.Message, pos int64, line int) error {
			if index.total%index.stride == 0 {
				if len(index.offsets) >= maxMessageIndexEntries {
					index.stride *= 2
					kept := index.offsets[:0]
					for _, entry := range index.offsets {
						if entry.message%index.stride == 0 {
							kept = append(kept, entry)
						}
					}
					index.offsets = kept
				}
				if index.total%index.stride == 0 {
					index.offsets = append(index.offsets, messageOffset{index.total, pos, line})
				}
			}
			index.total++
			return nil
		})
		if err != nil {
			return nil, 0, false, nil, err
		}
	}
	start = pageStart(index.total, limit, offset)
	end = index.total - offset
	if end < 0 {
		end = 0
	}
	page := make([]provider.Message, 0, end-start)
	if start == end {
		return page, index.total, start > 0, index, nil
	}
	point := sort.Search(len(index.offsets), func(i int) bool { return index.offsets[i].message > start }) - 1
	entry := index.offsets[point]
	if _, err := f.Seek(entry.bytePos, io.SeekStart); err != nil {
		return nil, 0, false, nil, err
	}
	ordinal := entry.message
	err = walkMessagesJSONL(f, entry.bytePos, entry.line, func(msg provider.Message, _ int64, _ int) error {
		if ordinal >= start {
			page = append(page, msg)
		}
		ordinal++
		if ordinal == end {
			return errMessagePageComplete
		}
		return nil
	})
	if err != nil && !errors.Is(err, errMessagePageComplete) {
		return nil, 0, false, nil, err
	}
	return page, index.total, start > 0, index, nil
}

// jsonlOffsetAfter returns the file position of the message that is skip
// records after from. skip is the number of messages leaving the cold window.
func jsonlOffsetAfter(path string, from int64, skip int) (int64, error) {
	if skip < 0 {
		return 0, fmt.Errorf("negative jsonl skip %d", skip)
	}
	if skip == 0 {
		return from, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return 0, err
	}
	next := int64(-1)
	seen := 0
	err = walkMessagesJSONL(f, from, 1, func(_ provider.Message, pos int64, _ int) error {
		if seen == skip {
			next = pos
			return errMessagePageComplete
		}
		seen++
		return nil
	})
	if err != nil && !errors.Is(err, errMessagePageComplete) {
		return 0, err
	}
	if next < 0 {
		return 0, fmt.Errorf("jsonl tail ended after %d messages, needed %d", seen, skip)
	}
	return next, nil
}

func readJSONLRange(f *os.File, from int64, fromMessage, start, end int) ([]provider.Message, error) {
	if start < fromMessage || end < start {
		return nil, fmt.Errorf("jsonl range %d-%d starts before recorded tail %d", start, end, fromMessage)
	}
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return nil, err
	}
	page := make([]provider.Message, 0, end-start)
	ordinal := fromMessage
	err := walkMessagesJSONL(f, from, 1, func(msg provider.Message, _ int64, _ int) error {
		if ordinal >= start {
			page = append(page, msg)
		}
		ordinal++
		if ordinal == end {
			return errMessagePageComplete
		}
		return nil
	})
	if err != nil && !errors.Is(err, errMessagePageComplete) {
		return nil, err
	}
	if ordinal != end {
		return nil, fmt.Errorf("jsonl tail ended at message %d, recorded range ends at %d", ordinal, end)
	}
	return page, nil
}
