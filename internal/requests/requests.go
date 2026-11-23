// Package requests moves a ROM request through its lifecycle: queued (saved, not yet in
// qBittorrent), downloading, importing (copying into the library), then imported or failed.
// Every step is persisted, so a restart picks up where it left off, and every transition is a
// conditional UPDATE, so two replicas never both take the same step.
package requests

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oldsj/rom-request/internal/db/dbq"
	"github.com/oldsj/rom-request/internal/library"
	"github.com/oldsj/rom-request/internal/qbittorrent"
	"github.com/oldsj/rom-request/internal/torrent"
)

// Tag is set on every torrent this app adds, so they're easy to find in qBittorrent.
const Tag = "rom-request"

const (
	// queuedGrace leaves a just-created request to the handler that is submitting it.
	queuedGrace = time.Minute
	// staleImport is when an import left by a crashed pod may be taken over. Generous: large
	// disc images over NFS take a while.
	staleImport = 2 * time.Hour
)

// Config is what the service needs from the server configuration.
type Config struct {
	// SavePath is the download folder as qBittorrent sees it; DownloadDir is the same folder
	// here. Each request downloads into <folder>/<request id>.
	SavePath    string
	DownloadDir string
	Category    string
}

// Service runs requests against qBittorrent and the library.
type Service struct {
	q   *dbq.Queries
	qb  *qbittorrent.Client
	lib *library.Library
	hc  *http.Client
	cfg Config
	log *slog.Logger
}

// New makes a Service. hc fetches .torrent files from Prowlarr.
func New(q *dbq.Queries, qb *qbittorrent.Client, lib *library.Library, hc *http.Client, cfg Config, log *slog.Logger) *Service {
	return &Service{q: q, qb: qb, lib: lib, hc: hc, cfg: cfg, log: log}
}

// Failure is a reason a request failed, safe to show.
type Failure struct{ Code, Message string }

var (
	failReleaseUnreadable = Failure{"release_unreadable", "This release's torrent couldn't be read. Try another search result."}
	failReleaseChanged    = Failure{"release_changed", "The indexer now serves a different torrent for this release. Search again and request it anew."}
	failElsewhere         = Failure{"already_in_qbittorrent", "qBittorrent already has this torrent outside ROM Request's folder. Remove it there, then retry."}
	failRejected          = Failure{"qbittorrent_rejected", "qBittorrent refused the torrent. Check qBittorrent, then retry."}
	failRemoved           = Failure{"torrent_removed", "The torrent was removed from qBittorrent. Retry to add it again."}
	failImport            = Failure{"import_failed", "Copying into the library failed. Retry; if it fails again, check the server log."}
)

func (s *Service) fail(ctx context.Context, id uuid.UUID, f Failure) {
	if err := s.q.MarkFailed(ctx, dbq.MarkFailedParams{ID: id, ErrorCode: f.Code, ErrorMessage: f.Message}); err != nil {
		s.log.ErrorContext(ctx, "mark failed", "request", id, "err", err)
	}
}

// Resolve fetches a release's torrent and computes its id.
func (s *Service) Resolve(ctx context.Context, magnet, downloadURL string) (torrent.Source, error) {
	return torrent.Resolve(ctx, s.hc, magnet, downloadURL)
}

func (s *Service) savePath(r dbq.Request) string {
	return path.Join(s.cfg.SavePath, r.ID.String())
}

// Submit hands a queued request to qBittorrent. src is the already-resolved torrent, or nil to
// fetch it again. A transient error (qBittorrent or Prowlarr unreachable) is returned and the
// request stays queued for the worker to try again; anything else marks it failed. It is safe
// to repeat: a torrent qBittorrent already has in this request's folder counts as submitted.
func (s *Service) Submit(ctx context.Context, r dbq.Request, src *torrent.Source) (dbq.Request, error) {
	id := r.ID
	if src == nil {
		got, err := s.Resolve(ctx, r.MagnetUrl, r.DownloadUrl)
		if errors.Is(err, torrent.ErrInvalid) {
			s.fail(ctx, id, failReleaseUnreadable)
			return s.reload(ctx, r)
		}
		if err != nil {
			return r, err
		}
		src = &got
	}
	if r.TorrentHash != "" && src.Hash != r.TorrentHash {
		s.fail(ctx, id, failReleaseChanged)
		return s.reload(ctx, r)
	}

	have, err := s.existing(ctx, src.Hash)
	if err != nil {
		return r, err
	}
	if have == nil {
		err := s.qb.Add(ctx, qbittorrent.Add{
			File: src.File, Magnet: src.Magnet, SavePath: s.savePath(r), Category: s.cfg.Category, Tags: []string{Tag},
		})
		switch {
		case errors.Is(err, qbittorrent.ErrRejected):
			// Perhaps it was there after all (added a moment ago by another replica).
			if have, err = s.existing(ctx, src.Hash); err != nil {
				return r, err
			}
			if have == nil {
				s.fail(ctx, id, failRejected)
				return s.reload(ctx, r)
			}
		case err != nil:
			return r, err
		}
	}
	if have != nil && strings.TrimRight(have.SavePath, "/") != s.savePath(r) {
		s.fail(ctx, id, failElsewhere)
		return s.reload(ctx, r)
	}

	updated, err := s.q.MarkSubmitted(ctx, dbq.MarkSubmittedParams{ID: r.ID, TorrentHash: src.Hash})
	if errors.Is(err, pgx.ErrNoRows) {
		return s.reload(ctx, r) // another replica got there first
	}
	return updated, err
}

func (s *Service) existing(ctx context.Context, hash string) (*qbittorrent.Torrent, error) {
	ts, err := s.qb.Info(ctx, []string{hash})
	if err != nil {
		return nil, err
	}
	for _, t := range ts {
		if strings.EqualFold(t.Hash, hash) {
			return &t, nil
		}
	}
	return nil, nil
}

func (s *Service) reload(ctx context.Context, r dbq.Request) (dbq.Request, error) {
	return s.q.GetRequest(ctx, dbq.GetRequestParams{ID: r.ID, UserID: r.UserID})
}

// Cancel removes a request's torrent and partial download from qBittorrent, if the torrent is
// the one this request added (in its own folder). It never touches the library.
func (s *Service) Cancel(ctx context.Context, r dbq.Request) error {
	if r.TorrentHash == "" || r.Status == dbq.RequestStatusImported {
		return nil
	}
	have, err := s.existing(ctx, r.TorrentHash)
	if err != nil || have == nil || strings.TrimRight(have.SavePath, "/") != s.savePath(r) {
		return err
	}
	return s.qb.Delete(ctx, r.TorrentHash, true)
}

// Run checks active requests every interval until ctx ends.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.log.WarnContext(ctx, "worker", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick does one round: submits queued requests, updates downloads, and imports finished ones.
func (s *Service) Tick(ctx context.Context) error {
	if _, err := s.q.DeleteExpiredSearchResults(ctx); err != nil {
		return err
	}
	active, err := s.q.ActiveRequests(ctx)
	if err != nil {
		return err
	}
	var downloading []dbq.Request
	var errs []error
	for _, r := range active {
		switch r.Status {
		case dbq.RequestStatusQueued:
			if time.Since(r.StepStartedAt) < queuedGrace {
				continue
			}
			if _, err := s.Submit(ctx, r, nil); err != nil {
				errs = append(errs, fmt.Errorf("submit %s: %w", r.ID, err))
			}
		case dbq.RequestStatusDownloading:
			downloading = append(downloading, r)
		case dbq.RequestStatusImporting:
			if time.Since(r.StepStartedAt) > staleImport {
				s.importRequest(ctx, r)
			}
		case dbq.RequestStatusImported, dbq.RequestStatusFailed:
		}
	}
	if err := s.followDownloads(ctx, downloading); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (s *Service) followDownloads(ctx context.Context, rs []dbq.Request) error {
	if len(rs) == 0 {
		return nil
	}
	hashes := make([]string, len(rs))
	for i, r := range rs {
		hashes[i] = r.TorrentHash
	}
	ts, err := s.qb.Info(ctx, hashes)
	if err != nil {
		return err
	}
	byHash := make(map[string]qbittorrent.Torrent, len(ts))
	for _, t := range ts {
		byHash[strings.ToLower(t.Hash)] = t
	}
	for _, r := range rs {
		id := r.ID
		t, ok := byHash[r.TorrentHash]
		switch {
		case !ok:
			s.fail(ctx, id, failRemoved)
		case t.Errored():
			s.fail(ctx, id, Failure{"download_failed", fmt.Sprintf("qBittorrent reports %q for this torrent. Fix it there, then retry.", t.State)})
		case t.Complete():
			s.importRequest(ctx, r)
		default:
			p := float32(math.Round(math.Min(t.Progress, 0.999)*1000) / 1000)
			if err := s.q.SetProgress(ctx, dbq.SetProgressParams{ID: r.ID, Progress: p}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) importRequest(ctx context.Context, r dbq.Request) {
	id := r.ID
	claimed, err := s.q.ClaimImport(ctx, dbq.ClaimImportParams{
		ID: r.ID, Stale: pgtype.Interval{Microseconds: staleImport.Microseconds(), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return // another replica has it
	}
	if err != nil {
		s.log.ErrorContext(ctx, "claim import", "request", r.ID, "err", err)
		return
	}
	files, err := s.qb.Files(ctx, claimed.TorrentHash)
	if err != nil {
		// Try again on a later round.
		s.log.WarnContext(ctx, "import: list files", "request", r.ID, "err", err)
		s.fail(ctx, id, Failure{"qbittorrent_unavailable", "Couldn't list the download's files in qBittorrent. Retry when it's reachable."})
		return
	}
	job := library.Job{ID: claimed.ID.String(), SourceDir: claimed.ID.String(), Platform: claimed.Platform, Title: claimed.Title}
	for _, f := range files {
		if f.Priority == 0 {
			continue // not selected for download
		}
		job.Files = append(job.Files, library.File{Name: f.Name, Size: f.Size})
	}
	res, err := s.lib.Import(s.cfg.DownloadDir, job)
	if err != nil {
		s.log.WarnContext(ctx, "import", "request", r.ID, "err", err)
		s.fail(ctx, id, importFailure(err, claimed.Platform))
		return
	}
	if err := s.q.MarkImported(ctx, dbq.MarkImportedParams{
		ID: claimed.ID, ImportedPath: res.Path, ImportedFiles: int32(min(res.Files, math.MaxInt32)), //nolint:gosec // bounded
		ImportedSha256: res.SHA256,
	}); err != nil {
		s.log.ErrorContext(ctx, "mark imported", "request", r.ID, "err", err)
		return
	}
	s.log.InfoContext(ctx, "imported", "request", r.ID, "path", res.Path, "files", res.Files)
}

func importFailure(err error, platform string) Failure {
	switch {
	case errors.Is(err, library.ErrConflict):
		_, what, _ := strings.Cut(err.Error(), ": ")
		return Failure{"library_conflict", fmt.Sprintf("%s is already in the library. Rename or remove it, then retry.", what)}
	case errors.Is(err, library.ErrUnknownPlatform):
		return Failure{"unknown_platform", fmt.Sprintf("The %s folder is no longer in the library.", platform)}
	case errors.Is(err, library.ErrIncomplete):
		return Failure{"download_incomplete", "The downloaded files don't match the torrent. Recheck the torrent in qBittorrent, then retry."}
	case errors.Is(err, library.ErrUnsafePath):
		return Failure{"unsafe_path", "The torrent has a file path that can't be imported safely."}
	default:
		return failImport
	}
}
