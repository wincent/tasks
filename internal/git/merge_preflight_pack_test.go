package git

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Construct a small nondelta pack and v2 index without invoking Git or relying
// on a transport fixture. Remove loose copies so a cold handle must read packs.
func mergePreflightPack(t *testing.T, objects string) {
	t.Helper()
	type object struct {
		id          []byte
		data        []byte
		kind        byte
		file        string
		offset, crc uint32
	}
	var entries []object
	files, err := filepath.Glob(filepath.Join(objects, "??", "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		id, err := hex.DecodeString(filepath.Base(filepath.Dir(file)) + filepath.Base(file))
		if err != nil || len(id) != 20 {
			continue
		}
		compressed, err := os.Open(file)
		if err != nil {
			t.Fatal(err)
		}
		reader, err := zlib.NewReader(compressed)
		if err != nil {
			compressed.Close()
			t.Fatal(err)
		}
		raw, err := io.ReadAll(reader)
		closeReader, closeFile := reader.Close(), compressed.Close()
		if err != nil || closeReader != nil || closeFile != nil {
			t.Fatalf("read loose object: %v %v %v", err, closeReader, closeFile)
		}
		header, data, ok := bytes.Cut(raw, []byte{0})
		if !ok {
			t.Fatal("invalid loose object")
		}
		name, _, _ := bytes.Cut(header, []byte(" "))
		kind := map[string]byte{"commit": 1, "tree": 2, "blob": 3, "tag": 4}[string(name)]
		if kind == 0 {
			t.Fatal("invalid object type")
		}
		entries = append(entries, object{id: id, data: data, kind: kind, file: file})
	}
	var pack bytes.Buffer
	pack.WriteString("PACK")
	put := func(buffer *bytes.Buffer, value uint32) {
		if err := binary.Write(buffer, binary.BigEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	put(&pack, 2)
	put(&pack, uint32(len(entries)))
	for i := range entries {
		entry := &entries[i]
		entry.offset = uint32(pack.Len())
		size := len(entry.data)
		first := entry.kind<<4 | byte(size&15)
		size >>= 4
		if size != 0 {
			first |= 128
		}
		pack.WriteByte(first)
		for size != 0 {
			next := byte(size & 127)
			size >>= 7
			if size != 0 {
				next |= 128
			}
			pack.WriteByte(next)
		}
		writer := zlib.NewWriter(&pack)
		if _, err := writer.Write(entry.data); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		entry.crc = crc32.ChecksumIEEE(pack.Bytes()[entry.offset:])
	}
	packHash := sha1.Sum(pack.Bytes())
	pack.Write(packHash[:])
	sort.Slice(entries, func(i, j int) bool { return bytes.Compare(entries[i].id, entries[j].id) < 0 })
	var index bytes.Buffer
	put(&index, 0xff744f63)
	put(&index, 2)
	count := 0
	for bucket := 0; bucket < 256; bucket++ {
		for count < len(entries) && int(entries[count].id[0]) <= bucket {
			count++
		}
		put(&index, uint32(count))
	}
	for _, entry := range entries {
		index.Write(entry.id)
	}
	for _, entry := range entries {
		put(&index, entry.crc)
	}
	for _, entry := range entries {
		put(&index, entry.offset)
	}
	index.Write(packHash[:])
	indexHash := sha1.Sum(index.Bytes())
	index.Write(indexHash[:])
	name := filepath.Join(objects, "pack", "pack-"+hex.EncodeToString(packHash[:]))
	writeFile(t, name+".pack", pack.Bytes())
	writeFile(t, name+".idx", index.Bytes())
	for _, entry := range entries {
		if err := os.Remove(entry.file); err != nil {
			t.Fatal(err)
		}
	}
}
