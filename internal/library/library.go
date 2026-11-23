// Package library reads finished downloads and copies them into the ROM library that RomM
// scans. Reads stay inside the download folder and writes inside the library: both go through
// os.Root, which refuses any path (including a symlink) that escapes it. An import never
// overwrites: an existing file or folder of the same name is a conflict for a person to settle.
package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
)

// StagingDir is the library's top-level folder for imports in progress. RomM must not treat it
// as a platform: names starting with "." are skipped here, and RomM's config.yml should list it
// under exclude.platforms.
const StagingDir = ".rom-request"

// Errors with a message for the user, wrapped with detail.
var (
	ErrConflict        = errors.New("already in the library")
	ErrUnknownPlatform = errors.New("no such platform folder")
	ErrIncomplete      = errors.New("download incomplete")
	ErrUnsafePath      = errors.New("unsafe path in the torrent")
)

// Library is the ROM library: one top-level folder per platform.
type Library struct {
	dir     string
	exclude []string
}

// New describes the library at dir. exclude names top-level folders that aren't platforms.
func New(dir string, exclude []string) *Library {
	lower := make([]string, 0, len(exclude))
	for _, e := range exclude {
		if e = strings.TrimSpace(e); e != "" {
			lower = append(lower, strings.ToLower(e))
		}
	}
	return &Library{dir: dir, exclude: lower}
}

// Platforms lists the platform folders, sorted.
func (l *Library) Platforms() ([]string, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, fmt.Errorf("library: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && l.isPlatform(e.Name()) {
			out = append(out, e.Name())
		}
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return out, nil
}

func (l *Library) isPlatform(name string) bool {
	if name == "" || strings.ContainsAny(name[:1], ".#@") || strings.ContainsAny(name, `/\`) {
		return false
	}
	return !slices.Contains(l.exclude, strings.ToLower(name))
}

// HasPlatform reports whether name is a platform folder.
func (l *Library) HasPlatform(name string) (bool, error) {
	if !l.isPlatform(name) {
		return false, nil
	}
	fi, err := os.Stat(path.Join(l.dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("library: %w", err)
	}
	return fi.IsDir(), nil
}

// File is one downloaded file: its path relative to the download folder ("/"-separated, as
// qBittorrent reports it) and the size it should have.
type File struct {
	Name string
	Size int64
}

// Job is one import.
type Job struct {
	// ID names the staging folder, so a retry finds and replaces its own leftovers.
	ID string
	// SourceDir is the request's folder inside the download folder.
	SourceDir string
	Files     []File
	Platform  string
	// Title names the library folder of a multi-file release that has no single top folder.
	Title string
}

// Result describes a finished import.
type Result struct {
	// Path is library-relative: "GBA/Game.gba" or "PS1/Game".
	Path  string
	Files int
	// SHA256 is the file's checksum, for a single-file release.
	SHA256 string
}

// Import copies a finished download into the platform's folder. A single file lands as
// <platform>/<file>; several files keep their layout under one folder, <platform>/<folder>,
// which RomM reads as one multi-file game (with any license and readme files beside it). The
// copy is staged under StagingDir and moved into place last, so RomM never sees half a game.
// downloads is the download folder. Cancelling ctx stops the copy and leaves the library as
// it was.
func (l *Library) Import(ctx context.Context, downloads string, job Job) (Result, error) {
	if len(job.Files) == 0 {
		return Result{}, fmt.Errorf("%w: the torrent has no files", ErrIncomplete)
	}
	if ok, err := l.HasPlatform(job.Platform); err != nil {
		return Result{}, err
	} else if !ok {
		return Result{}, fmt.Errorf("%w: %q", ErrUnknownPlatform, job.Platform)
	}
	for _, f := range job.Files {
		if !safeRel(f.Name) {
			return Result{}, fmt.Errorf("%w: %q", ErrUnsafePath, f.Name)
		}
	}
	if !safeRel(job.SourceDir) || !safeRel(job.ID) {
		return Result{}, fmt.Errorf("%w: job", ErrUnsafePath)
	}

	src, err := os.OpenRoot(downloads)
	if err != nil {
		return Result{}, fmt.Errorf("library: downloads: %w", err)
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenRoot(l.dir)
	if err != nil {
		return Result{}, fmt.Errorf("library: %w", err)
	}
	defer func() { _ = dst.Close() }()

	// Where it goes: a lone file keeps its name; several files go under their common top folder,
	// or a folder named after the release.
	var entry string
	// One file stays one file even if qBittorrent's content layout put it in a subfolder.
	single := len(job.Files) == 1
	strip := ""
	if single {
		entry = path.Base(job.Files[0].Name)
	} else if top, ok := commonTop(job.Files); ok {
		entry, strip = top, top+"/"
	} else {
		entry = folderName(job.Title)
	}
	if entry == "" || !safeRel(entry) || strings.Contains(entry, "/") {
		return Result{}, fmt.Errorf("%w: %q", ErrUnsafePath, entry)
	}
	final := path.Join(job.Platform, entry)
	if _, err := dst.Lstat(final); err == nil {
		return Result{}, fmt.Errorf("%w: %s", ErrConflict, final)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Result{}, fmt.Errorf("library: %w", err)
	}

	stage := path.Join(StagingDir, job.ID)
	if err := dst.RemoveAll(stage); err != nil {
		return Result{}, fmt.Errorf("library: clear staging: %w", err)
	}
	if err := dst.MkdirAll(stage, 0o775); err != nil {
		return Result{}, fmt.Errorf("library: staging: %w", err)
	}
	defer func() { _ = dst.RemoveAll(stage) }()

	var sum string
	for _, f := range job.Files {
		rel := entry
		if !single {
			rel = path.Join(entry, strings.TrimPrefix(f.Name, strip))
		}
		target := path.Join(stage, rel)
		if err := dst.MkdirAll(path.Dir(target), 0o775); err != nil {
			return Result{}, fmt.Errorf("library: staging: %w", err)
		}
		h, err := copyFile(ctx, src, path.Join(job.SourceDir, f.Name), dst, target, f.Size)
		if err != nil {
			return Result{}, err
		}
		if single {
			sum = h
		}
	}

	// Move into place without replacing anything. A file is hard-linked (which fails if the
	// name exists) and the staged name removed with the staging folder. A folder is renamed
	// after a last check: rename(2) would only replace an empty folder.
	staged := path.Join(stage, entry)
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if single {
		if err := dst.Link(staged, final); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return Result{}, fmt.Errorf("%w: %s", ErrConflict, final)
			}
			return Result{}, fmt.Errorf("library: move into place: %w", err)
		}
	} else {
		if _, err := dst.Lstat(final); err == nil {
			return Result{}, fmt.Errorf("%w: %s", ErrConflict, final)
		}
		if err := dst.Rename(staged, final); err != nil {
			return Result{}, fmt.Errorf("library: move into place: %w", err)
		}
	}
	return Result{Path: final, Files: len(job.Files), SHA256: sum}, nil
}

// copyFile copies one file between roots, checking its size and syncing it to disk, and
// returns its SHA-256.
func copyFile(ctx context.Context, src *os.Root, from string, dst *os.Root, to string, size int64) (string, error) {
	in, err := src.Open(from)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: %s is missing", ErrIncomplete, from)
		}
		return "", fmt.Errorf("library: open download: %w", err)
	}
	defer func() { _ = in.Close() }()
	fi, err := in.Stat()
	if err != nil {
		return "", fmt.Errorf("library: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s is not a regular file", ErrUnsafePath, from)
	}
	if fi.Size() != size {
		return "", fmt.Errorf("%w: %s is %d bytes, expected %d", ErrIncomplete, from, fi.Size(), size)
	}
	out, err := dst.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o664)
	if err != nil {
		return "", fmt.Errorf("library: create: %w", err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), ctxReader{ctx, in})
	if err == nil && n != size {
		err = fmt.Errorf("%w: %s changed while copying", ErrIncomplete, from)
	}
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("library: copy %s: %w", from, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// safeRel reports whether p is a clean, relative, "/"-separated path with no "..".
func safeRel(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) || strings.ContainsRune(p, 0) {
		return false
	}
	if path.Clean(p) != p {
		return false
	}
	for part := range strings.SplitSeq(p, "/") {
		if part == ".." || part == "." {
			return false
		}
	}
	return true
}

// commonTop returns the first path element if every file is under it.
func commonTop(files []File) (string, bool) {
	top, _, ok := strings.Cut(files[0].Name, "/")
	if !ok {
		return "", false
	}
	for _, f := range files[1:] {
		if t, _, ok := strings.Cut(f.Name, "/"); !ok || t != top {
			return "", false
		}
	}
	return top, true
}

// folderName makes a release title into a folder name: no separators or control characters,
// no leading dot, at most 120 bytes.
func folderName(title string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(title) {
		switch {
		case r < 0x20 || r == 0x7f:
		case strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name := strings.TrimLeft(strings.TrimSpace(b.String()), ".")
	for len(name) > 120 {
		_, size := lastRune(name)
		name = name[:len(name)-size]
	}
	return strings.TrimSpace(name)
}

func lastRune(s string) (rune, int) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i]&0xc0 != 0x80 {
			return rune(s[i]), len(s) - i
		}
	}
	return 0, 1
}

// ctxReader stops a copy once ctx ends.
type ctxReader struct {
	ctx context.Context //nolint:containedctx // scoped to one copy
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
