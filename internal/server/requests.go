package server

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oldsj/rom-request/internal/api"
	"github.com/oldsj/rom-request/internal/db/dbq"
	"github.com/oldsj/rom-request/internal/prowlarr"
	"github.com/oldsj/rom-request/internal/torrent"
)

// searchResultTTL is how long a search result can be requested.
const searchResultTTL = time.Hour

// Deadlines for the integration calls a request makes, so a response comes well inside the
// server's 30 s write timeout.
const (
	resolveTimeout = 15 * time.Second
	submitTimeout  = 10 * time.Second
)

var (
	errSearchOff    = apiError("not_configured", "Search isn't set up on this server: it needs Prowlarr.")
	errDownloadsOff = apiError("not_configured", "Requests aren't set up on this server: they need qBittorrent and the ROM library.")
	errLibraryOff   = apiError("not_configured", "The ROM library isn't set up on this server.")
	errLibraryRead  = apiError("library_unavailable", "The ROM library folder can't be read right now.")
	errProwlarr     = apiError("prowlarr_unavailable", "Prowlarr didn't answer the search. Try again shortly.")
	errExpired      = apiError("release_expired", "That search result has expired. Search again.")
	errPlatform     = apiError("unknown_platform", "Choose a platform folder from the library.")
	errUnreadable   = apiError("release_unreadable", "This release's torrent couldn't be read. Try another result.")
	errUnavailable  = apiError("release_unavailable", "The release's torrent couldn't be downloaded from the indexer. Try again shortly.")
	errDuplicate    = apiError("already_requested", "This release has already been requested.")
	errNotFailed    = apiError("not_failed", "Only a failed request can be retried.")
	errImporting    = apiError("importing", "This request is being copied into the library. Try again when it's done.")
)

func toRequest(r dbq.Request) api.Request {
	out := api.Request{
		Id: r.ID, Title: r.Title, Indexer: r.Indexer, SizeBytes: r.SizeBytes, InfoUrl: r.InfoUrl,
		Platform: r.Platform, License: r.License, SourceUrl: r.SourceUrl,
		Status: api.RequestStatus(r.Status), Progress: r.Progress,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.Status == dbq.RequestStatusImported {
		out.Progress = 1
	}
	if r.ErrorCode != "" {
		out.Error = &api.ErrorDetail{Code: r.ErrorCode, Message: r.ErrorMessage}
	}
	if r.ImportedPath != "" {
		files := int(r.ImportedFiles)
		out.ImportedPath, out.ImportedFiles = &r.ImportedPath, &files
	}
	if r.ImportedSha256 != "" {
		out.ImportedSha256 = &r.ImportedSha256
	}
	if r.ImportedAt.Valid {
		out.ImportedAt = &r.ImportedAt.Time
	}
	if r.GameID.Valid {
		out.GameId, out.GameName = &r.GameID.Int64, &r.GameName
		if r.CoverID != "" {
			out.CoverId = &r.CoverID
		}
	}
	return out
}

// GetStatus reports which integrations are configured.
func (s *Server) GetStatus(ctx context.Context, _ api.GetStatusRequestObject) (api.GetStatusResponseObject, error) {
	if _, ok := userFrom(ctx); !ok {
		return api.GetStatus401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse(errUnauthorized)}, nil
	}
	return api.GetStatus200JSONResponse{
		Prowlarr: s.search != nil, Qbittorrent: s.reqs != nil, Library: s.lib != nil,
		Catalog: s.catalog != nil && s.lib != nil,
	}, nil
}

// ListPlatforms lists the library's platform folders.
func (s *Server) ListPlatforms(ctx context.Context, _ api.ListPlatformsRequestObject) (api.ListPlatformsResponseObject, error) {
	if _, ok := userFrom(ctx); !ok {
		return api.ListPlatforms401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse(errUnauthorized)}, nil
	}
	if s.lib == nil {
		return api.ListPlatforms503JSONResponse{UnavailableJSONResponse: api.UnavailableJSONResponse(errLibraryOff)}, nil
	}
	names, err := s.lib.Platforms()
	if err != nil {
		s.log.ErrorContext(ctx, "list platforms", "err", err)
		return api.ListPlatforms503JSONResponse{UnavailableJSONResponse: api.UnavailableJSONResponse(errLibraryRead)}, nil
	}
	items := make([]api.Platform, len(names))
	for i, n := range names {
		items[i] = api.Platform{Name: n}
	}
	return api.ListPlatforms200JSONResponse{Items: items}, nil
}

// Search asks Prowlarr and keeps the results, with their download links, on the server.
func (s *Server) Search(ctx context.Context, req api.SearchRequestObject) (api.SearchResponseObject, error) {
	u, ok := userFrom(ctx)
	if !ok {
		return api.Search401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse(errUnauthorized)}, nil
	}
	if u.Limited {
		// A free-text release search can't tell what's mature; children request from the catalog.
		return api.Search403JSONResponse{ForbiddenJSONResponse: api.ForbiddenJSONResponse(errLimited)}, nil
	}
	if s.search == nil {
		return api.Search503JSONResponse{UnavailableJSONResponse: api.UnavailableJSONResponse(errSearchOff)}, nil
	}
	query := strings.TrimSpace(req.Params.Q)
	if len(query) < 2 {
		return api.Search400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse(apiError("invalid_request", "Enter at least two characters."))}, nil
	}
	found, err := s.search.Search(ctx, query, s.cfg.ProwlarrCategories)
	if err != nil {
		s.log.WarnContext(ctx, "prowlarr search", "err", err)
		return api.Search502JSONResponse{BadGatewayJSONResponse: api.BadGatewayJSONResponse(errProwlarr)}, nil
	}
	slices.SortStableFunc(found, func(a, b prowlarr.Release) int {
		return cmp.Compare(deref(b.Seeders), deref(a.Seeders))
	})

	items := make([]api.Release, 0, len(found))
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		expires := time.Now().Add(searchResultTTL)
		for _, r := range found {
			cats := make([]string, len(r.Categories))
			for i, c := range r.Categories {
				cats[i] = c.Name
			}
			p := dbq.InsertSearchResultParams{
				UserID: u.ID, Title: r.Title, Indexer: r.Indexer, SizeBytes: r.Size,
				InfoUrl: httpURL(r.InfoURL), DownloadUrl: r.DownloadURL, MagnetUrl: r.MagnetURL,
				InfoHash: strings.ToLower(r.InfoHash), Categories: cats, ExpiresAt: expires,
				Seeders: int4(r.Seeders), Leechers: int4(r.Leechers),
			}
			if r.PublishDate != nil {
				p.PublishedAt = pgtype.Timestamptz{Time: *r.PublishDate, Valid: true}
			}
			id, err := q.InsertSearchResult(ctx, p)
			if err != nil {
				return err
			}
			items = append(items, api.Release{
				Id: id, Title: r.Title, Indexer: r.Indexer, SizeBytes: r.Size, InfoUrl: p.InfoUrl,
				Categories: cats, Seeders: r.Seeders, Leechers: r.Leechers, PublishedAt: r.PublishDate,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return api.Search200JSONResponse{Items: items}, nil
}

// ListRequests lists the user's requests.
func (s *Server) ListRequests(ctx context.Context, _ api.ListRequestsRequestObject) (api.ListRequestsResponseObject, error) {
	u, ok := userFrom(ctx)
	if !ok {
		return api.ListRequests401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse(errUnauthorized)}, nil
	}
	rows, err := s.q.ListRequests(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	items := make([]api.Request, len(rows))
	for i, r := range rows {
		items[i] = toRequest(r)
	}
	return api.ListRequests200JSONResponse{Items: items}, nil
}

// CreateRequest requests a search result for a platform and hands it to qBittorrent.
func (s *Server) CreateRequest(ctx context.Context, req api.CreateRequestRequestObject) (api.CreateRequestResponseObject, error) {
	u, ok := userFrom(ctx)
	if !ok {
		return api.CreateRequest401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse(errUnauthorized)}, nil
	}
	if u.Limited {
		// A free-text release search can't tell what's mature; children request from the catalog.
		return api.CreateRequest403JSONResponse{ForbiddenJSONResponse: api.ForbiddenJSONResponse(errLimited)}, nil
	}
	if s.reqs == nil {
		return api.CreateRequest503JSONResponse{UnavailableJSONResponse: api.UnavailableJSONResponse(errDownloadsOff)}, nil
	}
	sr, err := s.q.GetSearchResult(ctx, dbq.GetSearchResultParams{ID: req.Body.ReleaseId, UserID: u.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.CreateRequest404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse(errExpired)}, nil
	}
	if err != nil {
		return nil, err
	}
	if ok, err := s.lib.HasPlatform(req.Body.Platform); err != nil {
		return nil, err
	} else if !ok {
		return api.CreateRequest400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse(errPlatform)}, nil
	}

	rctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	src, err := s.reqs.Resolve(rctx, sr.MagnetUrl, sr.DownloadUrl)
	if errors.Is(err, torrent.ErrInvalid) {
		return api.CreateRequest502JSONResponse{BadGatewayJSONResponse: api.BadGatewayJSONResponse(errUnreadable)}, nil
	}
	if err != nil {
		s.log.WarnContext(ctx, "fetch torrent", "err", err)
		return api.CreateRequest502JSONResponse{BadGatewayJSONResponse: api.BadGatewayJSONResponse(errUnavailable)}, nil
	}
	if _, err := s.q.RequestForTorrent(ctx, src.Hash); err == nil {
		return api.CreateRequest409JSONResponse{ConflictJSONResponse: api.ConflictJSONResponse(errDuplicate)}, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	r, err := s.q.CreateRequest(ctx, dbq.CreateRequestParams{
		UserID: u.ID, Title: sr.Title, Indexer: sr.Indexer, SizeBytes: sr.SizeBytes, InfoUrl: sr.InfoUrl,
		DownloadUrl: sr.DownloadUrl, MagnetUrl: sr.MagnetUrl, TorrentHash: src.Hash, TorrentIds: src.IDs,
		Platform: req.Body.Platform, License: strings.TrimSpace(deref(req.Body.License)),
		SourceUrl: strings.TrimSpace(deref(req.Body.SourceUrl)),
	})
	if isUniqueViolation(err) {
		return api.CreateRequest409JSONResponse{ConflictJSONResponse: api.ConflictJSONResponse(errDuplicate)}, nil
	}
	if err != nil {
		return nil, err
	}
	// The worker retries a request that stays queued (qBittorrent unreachable).
	sctx, cancel := context.WithTimeout(ctx, submitTimeout)
	defer cancel()
	if submitted, err := s.reqs.Submit(sctx, r, &src); err != nil {
		s.log.WarnContext(ctx, "submit request", "request", r.ID, "err", err)
	} else {
		r = submitted
	}
	return api.CreateRequest201JSONResponse(toRequest(r)), nil
}

// RetryRequest queues a failed request again and submits it.
func (s *Server) RetryRequest(ctx context.Context, req api.RetryRequestRequestObject) (api.RetryRequestResponseObject, error) {
	u, ok := userFrom(ctx)
	if !ok {
		return api.RetryRequest401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse(errUnauthorized)}, nil
	}
	r, err := s.q.RetryRequest(ctx, dbq.RetryRequestParams{ID: req.RequestId, UserID: u.ID})
	if isUniqueViolation(err) {
		return api.RetryRequest409JSONResponse{ConflictJSONResponse: api.ConflictJSONResponse(errDuplicate)}, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := s.q.GetRequest(ctx, dbq.GetRequestParams{ID: req.RequestId, UserID: u.ID}); errors.Is(err, pgx.ErrNoRows) {
			return api.RetryRequest404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse(errNotFound)}, nil
		} else if err != nil {
			return nil, err
		}
		return api.RetryRequest409JSONResponse{ConflictJSONResponse: api.ConflictJSONResponse(errNotFailed)}, nil
	}
	if err != nil {
		return nil, err
	}
	if s.reqs != nil && r.Status == dbq.RequestStatusQueued {
		sctx, cancel := context.WithTimeout(ctx, resolveTimeout+submitTimeout)
		defer cancel()
		if submitted, err := s.reqs.Submit(sctx, r, nil); err != nil {
			s.log.WarnContext(ctx, "resubmit request", "request", r.ID, "err", err)
		} else {
			r = submitted
		}
	}
	return api.RetryRequest200JSONResponse(toRequest(r)), nil
}

// DeleteRequest removes a request. Unless it was imported, its torrent and downloaded files
// are removed from qBittorrent: now if qBittorrent answers, otherwise by the worker later. The
// library is never touched.
func (s *Server) DeleteRequest(ctx context.Context, req api.DeleteRequestRequestObject) (api.DeleteRequestResponseObject, error) {
	u, ok := userFrom(ctx)
	if !ok {
		return api.DeleteRequest401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse(errUnauthorized)}, nil
	}
	var savePath string
	if s.reqs != nil {
		savePath = s.reqs.SavePath(req.RequestId)
	}
	removed, err := s.q.RemoveRequest(ctx, dbq.RemoveRequestParams{ID: req.RequestId, UserID: u.ID, SavePath: savePath})
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := s.q.GetRequest(ctx, dbq.GetRequestParams{ID: req.RequestId, UserID: u.ID}); errors.Is(err, pgx.ErrNoRows) {
			return api.DeleteRequest404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse(errNotFound)}, nil
		} else if err != nil {
			return nil, err
		}
		return api.DeleteRequest409JSONResponse{ConflictJSONResponse: api.ConflictJSONResponse(errImporting)}, nil
	}
	if err != nil {
		return nil, err
	}
	if removed.RemovalID != uuid.Nil && s.reqs != nil {
		rctx, cancel := context.WithTimeout(ctx, submitTimeout)
		defer cancel()
		if err := s.reqs.Remove(rctx, removed.RemovalID); err != nil {
			s.log.WarnContext(ctx, "remove torrent: worker will retry", "request", removed.ID, "err", err)
		}
	}
	return api.DeleteRequest204Response{}, nil
}

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func int4(p *int) pgtype.Int4 {
	if p == nil || *p < 0 || *p > 1<<31-1 {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*p), Valid: true} //nolint:gosec // range checked
}

// httpURL keeps a link only if it is http(s): the UI renders it as a link.
func httpURL(s string) string {
	if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
		return s
	}
	return ""
}
