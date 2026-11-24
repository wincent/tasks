package git

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

// Validate extensions before libgit2 can discard an optional extension on write.
// v4 prefix compression is parsed only to locate extension boundaries; libgit2
// remains responsible for checksum, entry semantics and object validation.
func validateIndexFormat(path string) error {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("Git index must be a regular non-symlink file")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return err
	}
	if len(data) < 32 || string(data[:4]) != "DIRC" {
		return errors.New("invalid Git index header")
	}
	version := binary.BigEndian.Uint32(data[4:8])
	if version < 2 || version > 4 {
		return fmt.Errorf("unsupported Git index version %d; preserve the index and convert it explicitly before retrying", version)
	}
	count := binary.BigEndian.Uint32(data[8:12])
	end, offset := len(data)-20, 12
	malformed := errors.New("malformed Git index entries or extensions")
	for i := uint32(0); i < count; i++ {
		start := offset
		if offset+62 > end {
			return malformed
		}
		flags := binary.BigEndian.Uint16(data[offset+60 : offset+62])
		offset += 62
		if flags&0x4000 != 0 {
			if version == 2 || offset+2 > end {
				return malformed
			}
			offset += 2
		}
		if version == 4 {
			// Strip-count uses Git's OFS_DELTA variable-length encoding.
			for n := 0; ; n++ {
				if offset >= end || n > 9 {
					return malformed
				}
				b := data[offset]
				offset++
				if b&0x80 == 0 {
					break
				}
			}
		}
		if offset >= end {
			return malformed
		}
		n := bytes.IndexByte(data[offset:end], 0)
		if n < 0 {
			return malformed
		}
		offset += n + 1
		if version != 4 {
			offset = start + ((offset-start+7)/8)*8
		}
		if offset > end {
			return malformed
		}
	}
	for offset < end {
		if offset+8 > end {
			return malformed
		}
		name := string(data[offset : offset+4])
		size := uint64(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		offset += 8
		if size > uint64(end-offset) {
			return malformed
		}
		switch name {
		case "link":
			return errors.New("unsupported split Git index (link extension); preserve it and disable split index explicitly before retrying")
		case "sdir":
			return errors.New("unsupported sparse Git index (sdir extension); preserve it and expand to a full index explicitly before retrying")
		case "TREE", "REUC", "NAME", "EOIE", "IEOT", "UNTR", "FSMN":
			// Known cache extensions are optional; libgit2 may invalidate them.
		default:
			return fmt.Errorf("unsupported Git index extension %q; preserving the index rather than dropping unknown state", name)
		}
		offset += int(size)
	}
	return nil
}
