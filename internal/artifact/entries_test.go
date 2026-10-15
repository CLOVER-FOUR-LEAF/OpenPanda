// SPDX-License-Identifier: AGPL-3.0-or-later

package artifact

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// packFixture builds a small tree and packs it into a temp-pool store,
// returning (store, hash). Entries/OpenEntry tests read through the same
// pack/unpack format the bus ships.
func packFixture(t *testing.T, files map[string]string) (*Store, string) {
	t.Helper()
	tree := t.TempDir()
	for name, body := range files {
		p := filepath.Join(tree, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := NewStore(t.TempDir())
	m, err := s.PackDir(tree)
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	return s, m.Hash
}

func TestEntriesListsRegularFiles(t *testing.T) {
	s, hash := packFixture(t, map[string]string{
		"screen.png":      "\x89PNG\r\n\x1a\n" + "pixels",
		"logs/run.log":    "line1\nline2\n",
		"deep/a/b/out.md": "# result",
	})
	entries, err := s.Entries(hash)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	got := map[string]int64{}
	for _, e := range entries {
		got[e.Path] = e.Size
	}
	for want, size := range map[string]int64{
		"screen.png":      8 + 6,
		"logs/run.log":    12,
		"deep/a/b/out.md": 8,
	} {
		if got[want] != size {
			t.Errorf("entry %q size = %d, want %d (entries %v)", want, got[want], size, entries)
		}
	}
}

func TestOpenEntryStreamsOneFile(t *testing.T) {
	s, hash := packFixture(t, map[string]string{
		"a.txt":   "first",
		"b/c.txt": "nested-content",
	})
	rc, size, err := s.OpenEntry(hash, "b/c.txt")
	if err != nil {
		t.Fatalf("OpenEntry: %v", err)
	}
	body, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != "nested-content" || size != int64(len(body)) {
		t.Fatalf("got %q size %d", body, size)
	}
}

func TestOpenEntryRejectsTraversalAndMisses(t *testing.T) {
	s, hash := packFixture(t, map[string]string{"a.txt": "x"})
	if _, _, err := s.OpenEntry(hash, "../a.txt"); err == nil {
		t.Fatal("traversal path must be refused, not sanitized")
	}
	if _, _, err := s.OpenEntry(hash, "nope.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing entry err = %v, want ErrNotFound", err)
	}
	if _, _, err := s.OpenEntry("deadbeef", "a.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing artifact err = %v, want ErrNotFound", err)
	}
}
