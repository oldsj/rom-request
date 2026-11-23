package library

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type fixture struct {
	downloads, lib string
	l              *Library
}

func setup(t *testing.T) fixture {
	t.Helper()
	f := fixture{downloads: t.TempDir(), lib: t.TempDir()}
	for _, d := range []string{"GBA", "PS1", "bios", "#recycle", ".rom-request"} {
		if err := os.MkdirAll(filepath.Join(f.lib, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.l = New(f.lib, []string{"BIOS"})
	return f
}

func write(t *testing.T, dir, name, body string) File {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return File{Name: name, Size: int64(len(body))}
}

func TestPlatforms(t *testing.T) {
	f := setup(t)
	got, err := f.l.Platforms()
	if err != nil || !slices.Equal(got, []string{"GBA", "PS1"}) {
		t.Fatalf("Platforms = %v, %v", got, err)
	}
	for name, want := range map[string]bool{"GBA": true, "bios": false, "../GBA": false, ".rom-request": false, "N64": false} {
		if ok, _ := f.l.HasPlatform(name); ok != want {
			t.Errorf("HasPlatform(%q) = %v", name, ok)
		}
	}
}

func TestImportSingleFile(t *testing.T) {
	f := setup(t)
	// qBittorrent's "create subfolder" layout: one file still lands as a file.
	file := write(t, filepath.Join(f.downloads, "req1"), "Homebrew Quest/Homebrew Quest.gba", "rom bytes")
	res, err := f.l.Import(f.downloads, Job{ID: "req1", SourceDir: "req1", Files: []File{file}, Platform: "GBA"})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("rom bytes"))
	if res.Path != "GBA/Homebrew Quest.gba" || res.Files != 1 || res.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("result %+v", res)
	}
	if b, err := os.ReadFile(filepath.Join(f.lib, "GBA", "Homebrew Quest.gba")); err != nil || string(b) != "rom bytes" {
		t.Errorf("imported file: %q %v", b, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.lib, StagingDir)); len(entries) != 0 {
		t.Errorf("staging left behind: %v", entries)
	}
	// The download is untouched.
	if _, err := os.Stat(filepath.Join(f.downloads, "req1", "Homebrew Quest", "Homebrew Quest.gba")); err != nil {
		t.Error(err)
	}

	// Again: a conflict, and the existing file is not replaced.
	if err := os.WriteFile(filepath.Join(f.downloads, "req1", "Homebrew Quest", "Homebrew Quest.gba"), []byte("ROM BYTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.l.Import(f.downloads, Job{ID: "req2", SourceDir: "req1", Files: []File{file}, Platform: "GBA"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second import: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.lib, "GBA", "Homebrew Quest.gba")); string(b) != "rom bytes" {
		t.Error("existing file overwritten")
	}
}

func TestImportMultiFile(t *testing.T) {
	f := setup(t)
	src := filepath.Join(f.downloads, "req")
	files := []File{
		write(t, src, "Disc Game/Disc Game.cue", "cue"),
		write(t, src, "Disc Game/Disc Game.bin", "bin data"),
		write(t, src, "Disc Game/LICENSE.txt", "CC BY 4.0"),
	}
	res, err := f.l.Import(f.downloads, Job{ID: "req", SourceDir: "req", Files: files, Platform: "PS1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != "PS1/Disc Game" || res.Files != 3 || res.SHA256 != "" {
		t.Errorf("result %+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(f.lib, "PS1", "Disc Game", "LICENSE.txt")); string(b) != "CC BY 4.0" {
		t.Error("license file not kept")
	}

	// Files with no common folder go under the release title.
	src2 := filepath.Join(f.downloads, "loose")
	loose := []File{write(t, src2, "a.gba", "a"), write(t, src2, "readme.txt", "r")}
	res, err = f.l.Import(f.downloads, Job{ID: "loose", SourceDir: "loose", Files: loose, Platform: "GBA", Title: "Two: Pack/Set"})
	if err != nil || res.Path != "GBA/Two_ Pack_Set" {
		t.Fatalf("loose: %+v %v", res, err)
	}
}

func TestImportRefusesUnsafeAndIncomplete(t *testing.T) {
	f := setup(t)
	good := write(t, filepath.Join(f.downloads, "r"), "game.gba", "data")
	cases := map[string]Job{
		"traversal":        {ID: "r", SourceDir: "r", Files: []File{{Name: "../../etc/passwd", Size: 1}}, Platform: "GBA"},
		"absolute":         {ID: "r", SourceDir: "r", Files: []File{{Name: "/etc/passwd", Size: 1}}, Platform: "GBA"},
		"platform escape":  {ID: "r", SourceDir: "r", Files: []File{good}, Platform: "../GBA"},
		"excluded":         {ID: "r", SourceDir: "r", Files: []File{good}, Platform: "bios"},
		"wrong size":       {ID: "r", SourceDir: "r", Files: []File{{Name: "game.gba", Size: 99}}, Platform: "GBA"},
		"missing":          {ID: "r", SourceDir: "r", Files: []File{{Name: "gone.gba", Size: 1}}, Platform: "GBA"},
		"no files":         {ID: "r", SourceDir: "r", Platform: "GBA"},
		"source traversal": {ID: "r", SourceDir: "..", Files: []File{good}, Platform: "GBA"},
	}
	for name, job := range cases {
		if _, err := f.l.Import(f.downloads, job); err == nil {
			t.Errorf("%s: imported", name)
		}
	}

	// A symlink in the download pointing outside it is refused by os.Root.
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.downloads, "r", "link.gba")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.l.Import(f.downloads, Job{ID: "r", SourceDir: "r", Files: []File{{Name: "link.gba", Size: 6}}, Platform: "GBA"}); err == nil {
		t.Error("symlink escaping the downloads was followed")
	}
	if _, err := os.Stat(filepath.Join(f.lib, "GBA", "link.gba")); err == nil {
		t.Error("escaped file imported")
	}
}

func TestFolderName(t *testing.T) {
	for in, want := range map[string]string{"  ..hidden ": "hidden", "a/b\\c": "a_b_c", "tab\there": "tabhere"} {
		if got := folderName(in); got != want {
			t.Errorf("folderName(%q) = %q, want %q", in, got, want)
		}
	}
	long := folderName(string(make([]rune, 0)) + "ééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééé")
	if len(long) > 120 {
		t.Errorf("long name %d bytes", len(long))
	}
}
