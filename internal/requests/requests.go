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
	// maxSubmitBackoff caps the wait between submit attempts; maxSubmitAttempts gives up
	// (about five hours in all) so a request never waits silently forever.
	maxSubmitBackoff  = time.Hour
	maxSubmitAttempts = 10
	// submittedGrace allows for qBittorrent adding a torrent in the background: a torrent not
	// listed yet isn't treated as removed until then.
	submittedGrace = 2 * time.Minute
	// An import renews its claim every renewEvery; one not renewed for staleImport is taken over
	// (its pod died). An importer that can't renew for staleImport/2 stops, so it is gone before
	// another pod may take over.
	renewEvery  = 30 * time.Second
	staleImport = 5 * time.Minute
	// writeTimeout bounds the state writes that must happen even while shutting down.
	writeTimeout = 10 * time.Second
)

// errClaimLost stops an import whose claim another pod has taken or that can't be renewed.
var errClaimLost = errors.New("import claim lost")

// Config is what the service needs from the server configuration.
type Config struct {
	// SavePath is the download folder as qBittorrent sees it; DownloadDir is the same folder
	// here. Each request downloads into <folder>/<request id>.
	SavePath    string
	DownloadDir string
	Category    string
	// Categories limits catalog searches to these Newznab categories.
	Categories []int
}

// Service runs requests against qBittorrent and the library.
type Service struct {
	q  *dbq.Queries
	qb *qbittorrent.Client
	// search finds releases for catalog requests; nil without Prowlarr.
	search Searcher
	lib    *library.Library
	hc     *http.Client
	cfg    Config
	log    *slog.Logger
	now    func() time.Time
}

// SetClock replaces the clock the worker's waits are measured with (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

func (s *Service) since(t time.Time) time.Duration { return s.now().Sub(t) }

// New makes a Service. hc fetches .torrent files from Prowlarr; search (nil without Prowlarr)
// finds the releases of catalog requests.
func New(q *dbq.Queries, qb *qbittorrent.Client, search Searcher, lib *library.Library, hc *http.Client, cfg Config, log *slog.Logger) *Service {
	return &Service{q: q, qb: qb, search: search, lib: lib, hc: hc, cfg: cfg, log: log, now: time.Now}
}

// Failure is a reason a request failed, safe to show.
type Failure struct{ Code, Message string }

var (
	failReleaseUnreadable = Failure{"release_unreadable", "This release's torrent couldn't be read, or the indexer no longer has it. Try another search result."}
	failReleaseChanged    = Failure{"release_changed", "The indexer now serves a different torrent for this release. Search again and request it anew."}
	failElsewhere         = Failure{"already_in_qbittorrent", "qBittorrent already has this torrent outside ROM Request's folder. Remove it there, then retry."}
	failRejected          = Failure{"qbittorrent_rejected", "qBittorrent refused the torrent. Check qBittorrent, then retry."}
	failGaveUp            = Failure{"submit_failed", "qBittorrent or the indexer stayed unreachable, so the torrent was never added. Retry once they're back."}
	failRemoved           = Failure{"torrent_removed", "The torrent was removed from qBittorrent. Retry to add it again."}
	failImport            = Failure{"import_failed", "Copying into the library failed. Retry; if it fails again, check the server log."}
)

// detached is for state writes that must land even when ctx is ending (shutdown).
func detached(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
}

// fail marks a queued or downloading request failed, if it is still in the status r has.
func (s *Service) fail(ctx context.Context, r dbq.Request, f Failure) {
	ctx, cancel := detached(ctx)
	defer cancel()
	if _, err := s.q.MarkFailed(ctx, dbq.MarkFailedParams{ID: r.ID, ErrorCode: f.Code, ErrorMessage: f.Message, Status: r.Status}); err != nil {
		s.log.ErrorContext(ctx, "mark failed", "request", r.ID, "err", err)
	}
}

// Resolve fetches a release's torrent and computes its ids.
func (s *Service) Resolve(ctx context.Context, magnet, downloadURL string) (torrent.Source, error) {
	return torrent.Resolve(ctx, s.hc, magnet, downloadURL)
}

// SavePath is where qBittorrent downloads a request's torrent.
func (s *Service) SavePath(id uuid.UUID) string {
	return path.Join(s.cfg.SavePath, id.String())
}

func (s *Service) savePath(r dbq.Request) string { return s.SavePath(r.ID) }

func (s *Service) ours(r dbq.Request, t *qbittorrent.Torrent) bool {
	return strings.TrimRight(t.SavePath, "/") == s.savePath(r)
}

// Submit hands a queued request to qBittorrent. src is the already-resolved torrent, or nil to
// fetch it again. A transient error (qBittorrent or Prowlarr unreachable) is returned and the
// request stays queued for the worker to try again; anything else marks it failed. It is safe
// to repeat: a torrent qBittorrent already has in this request's folder counts as submitted,
// and is looked for before the release is fetched again.
func (s *Service) Submit(ctx context.Context, r dbq.Request, src *torrent.Source) (dbq.Request, error) {
	if len(r.TorrentIds) > 0 {
		have, err := s.find(ctx, r.TorrentIds)
		if err != nil {
			return r, err
		}
		if have != nil {
			if !s.ours(r, have) {
				s.fail(ctx, r, failElsewhere)
				return s.reload(ctx, r)
			}
			return s.submitted(ctx, r, r.TorrentHash, r.TorrentIds)
		}
	}
	if src == nil {
		got, err := s.Resolve(ctx, r.MagnetUrl, r.DownloadUrl)
		if errors.Is(err, torrent.ErrInvalid) {
			s.fail(ctx, r, failReleaseUnreadable)
			return s.reload(ctx, r)
		}
		if err != nil {
			return r, err
		}
		src = &got
	}
	if r.TorrentHash != "" && src.Hash != r.TorrentHash {
		s.fail(ctx, r, failReleaseChanged)
		return s.reload(ctx, r)
	}

	have, err := s.find(ctx, src.IDs)
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
			if have, err = s.find(ctx, src.IDs); err != nil {
				return r, err
			}
			if have == nil {
				s.fail(ctx, r, failRejected)
				return s.reload(ctx, r)
			}
		case err != nil:
			return r, err
		}
	}
	if have != nil && !s.ours(r, have) {
		s.fail(ctx, r, failElsewhere)
		return s.reload(ctx, r)
	}
	return s.submitted(ctx, r, src.Hash, src.IDs)
}

// submitted records that qBittorrent has the request's torrent.
func (s *Service) submitted(ctx context.Context, r dbq.Request, hash string, ids []string) (dbq.Request, error) {
	updated, err := s.q.MarkSubmitted(ctx, dbq.MarkSubmittedParams{ID: r.ID, TorrentHash: hash, TorrentIds: ids})
	if !errors.Is(err, pgx.ErrNoRows) {
		return updated, err
	}
	// Another replica got there first, or the request was removed meanwhile; then the torrent
	// just added is no one's.
	current, err := s.reload(ctx, r)
	if errors.Is(err, pgx.ErrNoRows) {
		wctx, cancel := detached(ctx)
		defer cancel()
		if _, err := s.q.QueueTorrentRemoval(wctx, dbq.QueueTorrentRemovalParams{TorrentIds: ids, SavePath: s.savePath(r)}); err != nil {
			s.log.ErrorContext(ctx, "queue torrent removal", "request", r.ID, "err", err)
		}
		return r, nil
	}
	return current, err
}

// find returns the torrent qBittorrent knows by any of ids, or nil.
func (s *Service) find(ctx context.Context, ids []string) (*qbittorrent.Torrent, error) {
	ts, err := s.qb.Info(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, t := range ts {
		if t.Matches(ids) {
			return &t, nil
		}
	}
	return nil, nil
}

func (s *Service) reload(ctx context.Context, r dbq.Request) (dbq.Request, error) {
	return s.q.GetRequest(ctx, dbq.GetRequestParams{ID: r.ID, UserID: r.UserID})
}

// Remove carries out a queued torrent removal: it deletes the torrent and its downloaded files
// from qBittorrent if the torrent is in the removed request's own folder, then forgets the
// removal. On error the removal stays queued for the worker. It never touches the library.
func (s *Service) Remove(ctx context.Context, id uuid.UUID) error {
	rm, err := s.q.GetTorrentRemoval(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // done by another replica
	}
	if err != nil {
		return err
	}
	return s.remove(ctx, rm)
}

func (s *Service) remove(ctx context.Context, rm dbq.TorrentRemoval) error {
	have, err := s.find(ctx, rm.TorrentIds)
	if err != nil {
		return err
	}
	if have != nil && strings.TrimRight(have.SavePath, "/") == rm.SavePath {
		if err := s.qb.Delete(ctx, have.Hash, true); err != nil {
			return err
		}
	}
	return s.q.DeleteTorrentRemoval(ctx, rm.ID)
}

// Run checks requests every interval until ctx ends. It returns once an import in progress has
// stopped and given its claim back, so callers can wait for it before exiting.
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

// submitBackoff is how long a queued request waits after attempts failed submits.
func submitBackoff(attempts int32) time.Duration {
	if attempts <= 0 {
		return queuedGrace
	}
	return min(queuedGrace<<min(attempts, 16), maxSubmitBackoff)
}

// Tick does one round: removes torrents of removed requests, finds releases for catalog
// requests (alongside the rest, and waited for), submits queued requests, updates downloads,
// and imports finished ones.
func (s *Service) Tick(ctx context.Context) error {
	var errs []error
	removals, err := s.q.TorrentRemovals(ctx)
	if err != nil {
		return err
	}
	for _, rm := range removals {
		if err := s.remove(ctx, rm); err != nil {
			errs = append(errs, fmt.Errorf("remove torrent: %w", err))
			break // qBittorrent is likely down; the rest can wait for the next round
		}
	}

	active, err := s.q.ActiveRequests(ctx)
	if err != nil {
		return err
	}
	var searching []dbq.Request
	for _, r := range active {
		if r.Status == dbq.RequestStatusSearching {
			searching = append(searching, r)
		}
	}
	searched := make(chan struct{})
	go func() {
		defer close(searched)
		s.searchAll(ctx, searching)
	}()
	defer func() { <-searched }()

	var downloading []dbq.Request
	for _, r := range active {
		if ctx.Err() != nil {
			break
		}
		switch r.Status {
		case dbq.RequestStatusQueued:
			if s.since(r.StepStartedAt) < submitBackoff(r.SubmitAttempts) {
				continue
			}
			if _, err := s.Submit(ctx, r, nil); err != nil {
				errs = append(errs, fmt.Errorf("submit %s: %w", r.ID, err))
				s.noteSubmitFailure(ctx, r)
			}
		case dbq.RequestStatusDownloading:
			downloading = append(downloading, r)
		case dbq.RequestStatusImporting:
			if s.since(r.StepStartedAt) > staleImport {
				s.importRequest(ctx, r, "")
			}
		case dbq.RequestStatusSearching, dbq.RequestStatusImported, dbq.RequestStatusFailed:
		}
	}
	if err := s.followDownloads(ctx, downloading); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (s *Service) noteSubmitFailure(ctx context.Context, r dbq.Request) {
	n, err := s.q.NoteSubmitAttempt(ctx, r.ID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			s.log.WarnContext(ctx, "note submit attempt", "request", r.ID, "err", err)
		}
		return
	}
	if n >= maxSubmitAttempts {
		s.fail(ctx, r, failGaveUp)
	}
}

func idsOf(r dbq.Request) []string {
	if len(r.TorrentIds) > 0 {
		return r.TorrentIds
	}
	return []string{r.TorrentHash}
}

func (s *Service) followDownloads(ctx context.Context, rs []dbq.Request) error {
	if len(rs) == 0 {
		return nil
	}
	var ids []string
	for _, r := range rs {
		ids = append(ids, idsOf(r)...)
	}
	ts, err := s.qb.Info(ctx, ids)
	if err != nil {
		return err
	}
	for _, r := range rs {
		if ctx.Err() != nil {
			return nil
		}
		var t *qbittorrent.Torrent
		for i := range ts {
			if ts[i].Matches(idsOf(r)) {
				t = &ts[i]
				break
			}
		}
		switch {
		case t == nil:
			if s.since(r.StepStartedAt) > submittedGrace {
				s.fail(ctx, r, failRemoved)
			}
		case t.Errored():
			s.fail(ctx, r, Failure{"download_failed", fmt.Sprintf("qBittorrent reports %q for this torrent. Fix it there, then retry.", t.State)})
		case t.Complete():
			s.importRequest(ctx, r, t.Hash)
		default:
			p := float32(math.Round(math.Min(t.Progress, 0.999)*1000) / 1000)
			if err := s.q.SetProgress(ctx, dbq.SetProgressParams{ID: r.ID, Progress: p}); err != nil {
				return err
			}
		}
	}
	return nil
}

// importRequest claims a request's import and copies its files into the library. qbID is
// qBittorrent's id for the torrent, or empty to look it up (an import taken over from a pod
// that died).
func (s *Service) importRequest(ctx context.Context, r dbq.Request, qbID string) {
	claim := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	claimed, err := s.q.ClaimImport(ctx, dbq.ClaimImportParams{
		ID: r.ID, Claim: claim, Stale: pgtype.Interval{Microseconds: staleImport.Microseconds(), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return // another replica has it
	}
	if err != nil {
		s.log.ErrorContext(ctx, "claim import", "request", r.ID, "err", err)
		return
	}
	release := func(why string, err error) {
		s.log.WarnContext(ctx, "import put back: "+why, "request", r.ID, "err", err)
		wctx, cancel := detached(ctx)
		defer cancel()
		if _, err := s.q.ReleaseImport(wctx, dbq.ReleaseImportParams{ID: r.ID, ImportClaim: claim}); err != nil {
			s.log.ErrorContext(ctx, "release import", "request", r.ID, "err", err)
		}
	}
	finish := func(f Failure) {
		wctx, cancel := detached(ctx)
		defer cancel()
		if _, err := s.q.FailImport(wctx, dbq.FailImportParams{ID: r.ID, ImportClaim: claim, ErrorCode: f.Code, ErrorMessage: f.Message}); err != nil {
			s.log.ErrorContext(ctx, "mark import failed", "request", r.ID, "err", err)
		}
	}

	if qbID == "" {
		t, err := s.find(ctx, idsOf(claimed))
		if err != nil {
			release("qBittorrent unreachable", err)
			return
		}
		if t == nil {
			finish(failRemoved)
			return
		}
		qbID = t.Hash
	}
	files, err := s.qb.Files(ctx, qbID)
	if err != nil {
		release("couldn't list files", err) // the next round tries again
		return
	}
	job := library.Job{ID: claimed.ID.String(), SourceDir: claimed.ID.String(), Platform: claimed.Platform, Title: claimed.Title}
	for _, f := range files {
		if f.Priority == 0 {
			continue // not selected for download
		}
		job.Files = append(job.Files, library.File{Name: f.Name, Size: f.Size})
	}

	ictx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	renewed := make(chan struct{})
	go func() {
		defer close(renewed)
		s.renew(ictx, stop, r.ID, claim)
	}()
	res, err := s.lib.Import(ictx, s.cfg.DownloadDir, job)
	stop(nil)
	<-renewed
	switch {
	case err == nil:
	case ctx.Err() != nil:
		release("shutting down", err)
		return
	case errors.Is(context.Cause(ictx), errClaimLost):
		s.log.WarnContext(ctx, "import stopped: claim lost", "request", r.ID)
		return
	default:
		s.log.WarnContext(ctx, "import", "request", r.ID, "err", err)
		finish(importFailure(err, claimed.Platform))
		return
	}

	wctx, cancel := detached(ctx)
	defer cancel()
	n, err := s.q.MarkImported(wctx, dbq.MarkImportedParams{
		ID: r.ID, ImportClaim: claim, ImportedPath: res.Path,
		ImportedFiles:  int32(min(res.Files, math.MaxInt32)), //nolint:gosec // bounded
		ImportedSha256: res.SHA256,
	})
	if err != nil || n == 0 {
		s.log.ErrorContext(ctx, "mark imported", "request", r.ID, "path", res.Path, "rows", n, "err", err)
		return
	}
	s.log.InfoContext(ctx, "imported", "request", r.ID, "path", res.Path, "files", res.Files)
}

// renew keeps an import's claim fresh until ctx ends, and stops the import when the claim is
// gone or can't be renewed for long enough that another pod may take it over.
func (s *Service) renew(ctx context.Context, stop context.CancelCauseFunc, id uuid.UUID, claim pgtype.UUID) {
	t := time.NewTicker(renewEvery)
	defer t.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n, err := s.q.RenewImport(ctx, dbq.RenewImportParams{ID: id, ImportClaim: claim})
		switch {
		case err == nil && n == 0:
			stop(errClaimLost)
			return
		case err == nil:
			last = time.Now()
		case time.Since(last) > staleImport/2:
			stop(errClaimLost)
			return
		}
	}
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
