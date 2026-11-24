package git

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func indexFixture(t *testing.T, file string, version uint32, extension string, id ID) {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("DIRC")
	binary.Write(&b, binary.BigEndian, version)
	count := uint32(0)
	if version == 4 {
		count = 2
	}
	binary.Write(&b, binary.BigEndian, count)
	for i, name := range []string{"prefix/assumed", "prefix/skipped"} {
		if count == 0 {
			break
		}
		for j := 0; j < 10; j++ {
			v := uint32(0)
			if j == 6 {
				v = 0100644
			}
			binary.Write(&b, binary.BigEndian, v)
		}
		b.Write(id[:])
		flags := uint16(len(name))
		if i == 0 {
			flags |= 0x8000
		} else {
			flags |= 0x4000
		}
		binary.Write(&b, binary.BigEndian, flags)
		if i == 1 {
			binary.Write(&b, binary.BigEndian, uint16(0x4000))
		}
		if i == 0 {
			b.WriteByte(0)
			b.WriteString(name)
		} else {
			b.WriteByte(7)
			b.WriteString("skipped")
		}
		b.WriteByte(0)
	}
	if extension != "" {
		b.WriteString(extension)
		binary.Write(&b, binary.BigEndian, uint32(0))
	}
	sum := sha1.Sum(b.Bytes())
	b.Write(sum[:])
	writeFile(t, file, b.Bytes())
}

func TestNativeIndexVersionFourPreservation(t *testing.T) {
	t.Parallel()
	r, _ := fixture(t, false)
	if err := r.SetLocalConfig("core.sparseCheckout", "true"); err != nil {
		t.Fatal(err)
	}
	paths, _ := r.Paths()
	id, err := r.WriteBlob([]byte("content"))
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(paths.GitDir, "index")
	indexFixture(t, real, 4, "", id)
	before, err := r.IndexEntries()
	if err != nil || len(before) != 2 || !before[0].AssumeValid || !before[1].SkipWorktree {
		t.Fatalf("%+v %v", before, err)
	}
	original := readFile(t, real)
	isolated := filepath.Join(paths.GitDir, "isolated")
	writeFile(t, isolated, original)
	if err := r.EditIndexFile(isolated, ID{}, []TreeEdit{{Path: "z-task", ID: id, Mode: 0100644}}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, readFile(t, real)) {
		t.Fatal("changed real index")
	}
	if version := binary.BigEndian.Uint32(readFile(t, isolated)[4:8]); version != 4 {
		t.Fatalf("downgraded v4 to %d", version)
	}
	if err := os.Rename(isolated, real); err != nil {
		t.Fatal(err)
	}
	after, err := r.IndexEntries()
	if err != nil || len(after) != 3 || !reflect.DeepEqual(before, after[:2]) {
		t.Fatalf("%+v %v", after, err)
	}
}

func TestNativeIndexUnsupportedFormatsPreserveBytes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		version         uint32
		extension, want string
	}{{2, "link", "split"}, {2, "sdir", "sparse"}, {2, "ZUNK", "unknown state"}, {5, "", "version 5"}} {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			r, _ := fixture(t, false)
			paths, _ := r.Paths()
			real := filepath.Join(paths.GitDir, "index")
			indexFixture(t, real, tc.version, tc.extension, ID{})
			before := readFile(t, real)
			if _, err := r.IndexEntries(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v", err)
			}
			isolated := filepath.Join(paths.GitDir, "isolated")
			writeFile(t, isolated, before)
			if err := r.EditIndexFile(isolated, ID{}, nil); err == nil {
				t.Fatal("edited unsupported index")
			}
			if !bytes.Equal(before, readFile(t, real)) || !bytes.Equal(before, readFile(t, isolated)) {
				t.Fatal("changed unsupported index")
			}
		})
	}
}
