package server

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oldsj/rom-request/internal/api"
	"github.com/oldsj/rom-request/internal/db/dbq"
	"github.com/oldsj/rom-request/internal/igdb"
	"github.com/oldsj/rom-request/internal/library"
)

// catalogPageSize is how many games one catalog page holds.
const catalogPageSize = 48

var (
	errCatalogOff      = apiError("not_configured", "The game catalog isn't set up on this server: it needs IGDB credentials and the ROM library.")
	errCatalog         = apiError("catalog_unavailable", "The game catalog (IGDB) didn't answer. Try again shortly.")
	errNotInCatalog    = apiError("not_in_catalog", "IGDB doesn't know this platform folder. Map it with CATALOG_PLATFORMS.")
	errUnknownPlatform = apiError("unknown_platform", "No such platform folder in the library.")
)

// platformAliases are RomM's platform folder names that differ from IGDB's platform slugs.
var platformAliases = map[string]string{
	"psx": "ps", "ps1": "ps",
	"genesis": "genesis-slash-megadrive", "megadrive": "genesis-slash-megadrive", "md": "genesis-slash-megadrive",
	"segacd": "sega-cd", "mega-cd": "sega-cd",
	"32x": "sega32", "sega32x": "sega32",
	"gamecube": "ngc", "gc": "ngc",
	"n3ds": "3ds",
	"gg":   "gamegear", "ms": "sms", "mastersystem": "sms",
	"dreamcast": "dc",
	"tg16":      "turbografx16--1", "pce": "turbografx16--1", "pcengine": "turbografx16--1",
	"ngp": "neo-geo-pocket", "ngpc": "neo-geo-pocket-color",
	"ws": "wonderswan", "wsc": "wonderswan-color",
	"a2600": "atari2600",
}

// catalogSlug is the IGDB platform slug for a library folder.
func (s *Server) catalogSlug(folder string) string {
	f := strings.ToLower(folder)
	if slug, ok := s.cfg.CatalogPlatforms[folder]; ok {
		return slug
	}
	if slug, ok := s.cfg.CatalogPlatforms[f]; ok {
		return slug
	}
	if slug, ok := platformAliases[f]; ok {
		return slug
	}
	return f
}

// ListCatalogPlatforms lists the library's platform folders with IGDB's name for each it knows.
func (s *Server) ListCatalogPlatforms(ctx context.Context, _ api.ListCatalogPlatformsRequestObject) (api.ListCatalogPlatformsResponseObject, error) {
	if _, ok := userFrom(ctx); !ok {
		return api.ListCatalogPlatforms401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse(errUnauthorized)}, nil
	}
	if s.catalog == nil || s.lib == nil {
		return api.ListCatalogPlatforms503JSONResponse{UnavailableJSONResponse: api.UnavailableJSONResponse(errCatalogOff)}, nil
	}
	folders, err := s.lib.Platforms()
	if err != nil {
		s.log.ErrorContext(ctx, "list platforms", "err", err)
		return api.ListCatalogPlatforms503JSONResponse{UnavailableJSONResponse: api.UnavailableJSONResponse(errLibraryRead)}, nil
	}
	slugs := make([]string, len(folders))
	for i, f := range folders {
		slugs[i] = s.catalogSlug(f)
	}
	known, err := s.catalog.Platforms(ctx, slugs)
	if err != nil {
		s.log.WarnContext(ctx, "igdb platforms", "err", err)
		return api.ListCatalogPlatforms502JSONResponse{BadGatewayJSONResponse: api.BadGatewayJSONResponse(errCatalog)}, nil
	}
	items := make([]api.CatalogPlatform, len(folders))
	for i, f := range folders {
		items[i] = api.CatalogPlatform{Name: f}
		if p, ok := known[slugs[i]]; ok {
			items[i].CatalogName = &p.Name
		}
	}
	return api.ListCatalogPlatforms200JSONResponse{Items: items}, nil
}

// ListCatalogGames lists one page of a platform's games, marking those the library has.
func (s *Server) ListCatalogGames(ctx context.Context, req api.ListCatalogGamesRequestObject) (api.ListCatalogGamesResponseObject, error) {
	if _, ok := userFrom(ctx); !ok {
		return api.ListCatalogGames401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse(errUnauthorized)}, nil
	}
	if s.catalog == nil || s.lib == nil {
		return api.ListCatalogGames503JSONResponse{UnavailableJSONResponse: api.UnavailableJSONResponse(errCatalogOff)}, nil
	}
	p := req.Params
	names, err := s.lib.Games(p.Platform)
	if errors.Is(err, library.ErrUnknownPlatform) {
		return api.ListCatalogGames404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse(errUnknownPlatform)}, nil
	}
	if err != nil {
		s.log.ErrorContext(ctx, "list library games", "err", err)
		return api.ListCatalogGames503JSONResponse{UnavailableJSONResponse: api.UnavailableJSONResponse(errLibraryRead)}, nil
	}
	slug := s.catalogSlug(p.Platform)
	known, err := s.catalog.Platforms(ctx, []string{slug})
	if err != nil {
		s.log.WarnContext(ctx, "igdb platforms", "err", err)
		return api.ListCatalogGames502JSONResponse{BadGatewayJSONResponse: api.BadGatewayJSONResponse(errCatalog)}, nil
	}
	platform, ok := known[slug]
	if !ok {
		return api.ListCatalogGames404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse(errNotInCatalog)}, nil
	}
	sort := igdb.SortPopular
	if p.Sort != nil && *p.Sort == api.Name {
		sort = igdb.SortName
	}
	offset := deref(p.Offset)
	games, err := s.catalog.Games(ctx, platform.ID, deref(p.Q), sort, offset, catalogPageSize)
	if err != nil {
		s.log.WarnContext(ctx, "igdb games", "err", err)
		return api.ListCatalogGames502JSONResponse{BadGatewayJSONResponse: api.BadGatewayJSONResponse(errCatalog)}, nil
	}

	have := make(map[string]bool, len(names))
	for _, n := range names {
		if k := library.TitleKey(n); k != "" {
			have[k] = true
		}
	}
	items := make([]api.CatalogGame, len(games))
	for i, g := range games {
		items[i] = api.CatalogGame{Id: g.ID, Name: g.Name, InLibrary: have[library.TitleKey(g.Name)]}
		if g.Year != 0 {
			items[i].Year = &g.Year
		}
		if g.Summary != "" {
			items[i].Summary = &g.Summary
		}
		if g.CoverID != "" {
			items[i].CoverId = &g.CoverID
		}
	}
	out := api.ListCatalogGames200JSONResponse{Items: items}
	if next := offset + catalogPageSize; len(games) == catalogPageSize && next <= 10000 {
		out.NextOffset = &next
	}
	return out, nil
}

var errGameNotFound = apiError("not_in_catalog", "IGDB has no such game on this platform.")

// ListCatalogRequests lists the user's latest request for each catalog game on a platform.
func (s *Server) ListCatalogRequests(ctx context.Context, req api.ListCatalogRequestsRequestObject) (api.ListCatalogRequestsResponseObject, error) {
	u, ok := userFrom(ctx)
	if !ok {
		return api.ListCatalogRequests401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse(errUnauthorized)}, nil
	}
	rows, err := s.q.CatalogRequests(ctx, dbq.CatalogRequestsParams{UserID: u.ID, Platform: req.Params.Platform})
	if err != nil {
		return nil, err
	}
	items := make([]api.Request, len(rows))
	for i, r := range rows {
		items[i] = toRequest(r)
	}
	return api.ListCatalogRequests200JSONResponse{Items: items}, nil
}

// RequestCatalogGame saves a request for a catalog game; the worker finds and downloads a
// release of it.
func (s *Server) RequestCatalogGame(ctx context.Context, req api.RequestCatalogGameRequestObject) (api.RequestCatalogGameResponseObject, error) {
	u, ok := userFrom(ctx)
	if !ok {
		return api.RequestCatalogGame401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse(errUnauthorized)}, nil
	}
	if s.catalog == nil || s.lib == nil {
		return api.RequestCatalogGame503JSONResponse{UnavailableJSONResponse: api.UnavailableJSONResponse(errCatalogOff)}, nil
	}
	if s.reqs == nil || s.search == nil {
		return api.RequestCatalogGame503JSONResponse{UnavailableJSONResponse: api.UnavailableJSONResponse(errDownloadsOff)}, nil
	}
	b := req.Body
	if ok, err := s.lib.HasPlatform(b.Platform); err != nil {
		return nil, err
	} else if !ok {
		return api.RequestCatalogGame400JSONResponse{BadRequestJSONResponse: api.BadRequestJSONResponse(errPlatform)}, nil
	}
	slug := s.catalogSlug(b.Platform)
	known, err := s.catalog.Platforms(ctx, []string{slug})
	if err != nil {
		s.log.WarnContext(ctx, "igdb platforms", "err", err)
		return api.RequestCatalogGame502JSONResponse{BadGatewayJSONResponse: api.BadGatewayJSONResponse(errCatalog)}, nil
	}
	platform, ok := known[slug]
	if !ok {
		return api.RequestCatalogGame404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse(errNotInCatalog)}, nil
	}
	game, err := s.catalog.Game(ctx, b.GameId, platform.ID)
	if errors.Is(err, igdb.ErrNotFound) {
		return api.RequestCatalogGame404JSONResponse{NotFoundJSONResponse: api.NotFoundJSONResponse(errGameNotFound)}, nil
	}
	if err != nil {
		s.log.WarnContext(ctx, "igdb game", "err", err)
		return api.RequestCatalogGame502JSONResponse{BadGatewayJSONResponse: api.BadGatewayJSONResponse(errCatalog)}, nil
	}
	r, err := s.q.CreateCatalogRequest(ctx, dbq.CreateCatalogRequestParams{
		UserID: u.ID, GameName: game.Name, Platform: b.Platform, GameID: pgtype.Int8{Int64: game.ID, Valid: true}, CoverID: game.CoverID,
		CatalogSlug: platform.Slug, CatalogName: platform.Name,
	})
	if isUniqueViolation(err) {
		return api.RequestCatalogGame409JSONResponse{ConflictJSONResponse: api.ConflictJSONResponse(apiError("already_requested", "This game is already requested."))}, nil
	}
	if err != nil {
		return nil, err
	}
	return api.RequestCatalogGame201JSONResponse(toRequest(r)), nil
}

// GetCatalogCover relays a cover from IGDB's image server.
func (s *Server) GetCatalogCover(ctx context.Context, req api.GetCatalogCoverRequestObject) (api.GetCatalogCoverResponseObject, error) {
	if _, ok := userFrom(ctx); !ok {
		return api.GetCatalogCover401JSONResponse{UnauthorizedJSONResponse: api.UnauthorizedJSONResponse(errUnauthorized)}, nil
	}
	if s.catalog == nil {
		return api.GetCatalogCover503JSONResponse{UnavailableJSONResponse: api.UnavailableJSONResponse(errCatalogOff)}, nil
	}
	body, size, err := s.catalog.Cover(ctx, req.CoverId)
	if err != nil {
		s.log.WarnContext(ctx, "igdb cover", "err", err)
		return api.GetCatalogCover502JSONResponse{BadGatewayJSONResponse: api.BadGatewayJSONResponse(errCatalog)}, nil
	}
	if size < 0 {
		size = 0 // unknown: sent without Content-Length
	}
	return api.GetCatalogCover200ImagejpegResponse{
		Body:          limitedBody{Reader: io.LimitReader(body, igdb.MaxCoverBytes), Closer: body},
		ContentLength: size,
		// An IGDB image id names one image for good.
		Headers: api.GetCatalogCover200ResponseHeaders{CacheControl: "private, max-age=2592000, immutable"},
	}, nil
}

type limitedBody struct {
	io.Reader
	io.Closer
}
