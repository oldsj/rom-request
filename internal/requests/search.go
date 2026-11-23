package requests

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oldsj/rom-request/internal/db/dbq"
	"github.com/oldsj/rom-request/internal/prowlarr"
	"github.com/oldsj/rom-request/internal/torrent"
)

// Searcher searches Prowlarr.
type Searcher interface {
	Search(ctx context.Context, query string, categories []int) ([]prowlarr.Release, error)
}

const (
	// staleSearch is how long a search may run before another pod takes it over (its pod
	// died). It also spaces out the searches of a request after Prowlarr failed.
	staleSearch = 10 * time.Minute
	// maxSearchAttempts gives up on a request when Prowlarr keeps failing.
	maxSearchAttempts = 4
	// maxSearches is how many catalog requests one pod searches for at once.
	maxSearches = 3
	// tryReleases is how many of the best releases are tried when one's torrent can't be read.
	tryReleases = 3
)

var (
	failNoRelease = Failure{"no_release", "No release of this game for this platform was found. Retry later, or search for one yourself."}
	failSearch    = Failure{"search_failed", "Prowlarr kept failing to answer the search or to hand over the release. Retry later."}
)

// searchAll searches for the searching requests among rs, a few at once, and returns when all
// are done (found, failed, or put back for later).
func (s *Service) searchAll(ctx context.Context, rs []dbq.Request) {
	if s.search == nil || len(rs) == 0 {
		return
	}
	sem := make(chan struct{}, maxSearches)
	var wg sync.WaitGroup
	for _, r := range rs {
		if r.SearchAttempts > 0 && s.since(r.StepStartedAt) < staleSearch {
			continue // being searched, or waiting after a failed search
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return
		}
		wg.Go(func() {
			defer func() { <-sem }()
			s.searchRequest(ctx, r)
		})
	}
	wg.Wait()
}

// searchRequest claims a request's search, searches Prowlarr for the game, and queues the best
// release whose torrent can be read.
func (s *Service) searchRequest(ctx context.Context, r dbq.Request) {
	claimed, err := s.q.ClaimSearch(ctx, dbq.ClaimSearchParams{
		ID: r.ID, Stale: pgtype.Interval{Microseconds: staleSearch.Microseconds(), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return // another pod has it, or it was removed
	}
	if err != nil {
		s.log.ErrorContext(ctx, "claim search", "request", r.ID, "err", err)
		return
	}
	if claimed.SearchAttempts > maxSearchAttempts {
		// Searched again and again (its releases' links kept failing).
		s.fail(ctx, claimed, failSearch)
		return
	}
	g := Game{Name: claimed.GameName, Folder: claimed.Platform, Slug: claimed.CatalogSlug, PlatformName: claimed.CatalogName}

	var ranked []prowlarr.Release
	for _, q := range Queries(g) {
		found, err := s.search.Search(ctx, q, s.cfg.Categories)
		if err != nil {
			if ctx.Err() != nil {
				return // shutting down; another pod takes it over later
			}
			s.log.WarnContext(ctx, "catalog search", "request", r.ID, "attempt", claimed.SearchAttempts, "err", err)
			if claimed.SearchAttempts >= maxSearchAttempts {
				s.fail(ctx, claimed, failSearch)
			}
			return // the next search waits staleSearch
		}
		if ranked = Rank(g, found); len(ranked) > 0 {
			break
		}
	}

	for _, rel := range ranked[:min(len(ranked), tryReleases)] {
		src, err := s.Resolve(ctx, rel.MagnetURL, rel.DownloadURL)
		if errors.Is(err, torrent.ErrInvalid) {
			continue
		}
		if err != nil {
			s.log.WarnContext(ctx, "catalog search: fetch torrent", "request", r.ID, "err", err)
			if claimed.SearchAttempts >= maxSearchAttempts {
				s.fail(ctx, claimed, failSearch)
			}
			return
		}
		if _, err := s.q.RequestForTorrent(ctx, src.Hash); err == nil {
			continue // requested already, by someone; try the next
		} else if !errors.Is(err, pgx.ErrNoRows) {
			s.log.ErrorContext(ctx, "catalog search", "request", r.ID, "err", err)
			return
		}
		queued, err := s.q.FoundRelease(ctx, dbq.FoundReleaseParams{
			ID: claimed.ID, Title: rel.Title, Indexer: rel.Indexer, SizeBytes: rel.Size,
			InfoUrl: httpURL(rel.InfoURL), DownloadUrl: rel.DownloadURL, MagnetUrl: rel.MagnetURL,
			TorrentHash: src.Hash, TorrentIds: src.IDs,
		})
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			continue // requested a moment ago
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return // removed meanwhile
		}
		if err != nil {
			s.log.ErrorContext(ctx, "found release", "request", r.ID, "err", err)
			return
		}
		s.log.InfoContext(ctx, "catalog request: release picked", "request", r.ID, "game", g.Name, "release", rel.Title, "indexer", rel.Indexer)
		// The submit step retries a request that stays queued.
		if _, err := s.Submit(ctx, queued, &src); err != nil {
			s.log.WarnContext(ctx, "submit request", "request", r.ID, "err", err)
		}
		return
	}
	s.log.InfoContext(ctx, "catalog request: no release", "request", r.ID, "game", g.Name, "platform", g.Folder)
	s.fail(ctx, claimed, failNoRelease)
}

// httpURL keeps a link only if it is http(s): the UI renders it as a link.
func httpURL(s string) string {
	if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
		return s
	}
	return ""
}
