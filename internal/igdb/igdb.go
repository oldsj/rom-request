// Package igdb reads the game catalog from IGDB (api.igdb.com), with a Twitch application's
// client credentials. It serves the catalog browser: platforms, the games on one, and covers.
package igdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Config is the Twitch application and, for tests, where the services are.
type Config struct {
	ClientID     string
	ClientSecret string
	TokenURL     string // default https://id.twitch.tv/oauth2/token
	APIURL       string // default https://api.igdb.com/v4
	ImageURL     string // default https://images.igdb.com/igdb/image/upload
}

// Client calls IGDB. Safe for concurrent use.
type Client struct {
	cfg Config
	hc  *http.Client
	// IGDB allows 4 requests a second.
	limit *rate.Limiter

	mu      sync.Mutex
	token   string
	expires time.Time
	slugs   map[string]cachedPlatform
}

type cachedPlatform struct {
	p     *Platform // nil: IGDB has no such slug
	until time.Time
}

// New makes a Client.
func New(cfg Config, hc *http.Client) *Client {
	if cfg.TokenURL == "" {
		cfg.TokenURL = "https://id.twitch.tv/oauth2/token"
	}
	if cfg.APIURL == "" {
		cfg.APIURL = "https://api.igdb.com/v4"
	}
	if cfg.ImageURL == "" {
		cfg.ImageURL = "https://images.igdb.com/igdb/image/upload"
	}
	return &Client{cfg: cfg, hc: hc, limit: rate.NewLimiter(4, 4), slugs: map[string]cachedPlatform{}}
}

// ErrUnavailable means IGDB or Twitch didn't answer as expected.
var ErrUnavailable = errors.New("igdb: unavailable")

func (c *Client) accessToken(ctx context.Context, renew bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !renew && c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}
	form := url.Values{"client_id": {c.cfg.ClientID}, "client_secret": {c.cfg.ClientSecret}, "grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: token: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: token: status %d", ErrUnavailable, resp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&tok); err != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("%w: token: unreadable answer", ErrUnavailable)
	}
	c.token = tok.AccessToken
	// Renew a minute early.
	c.expires = time.Now().Add(time.Duration(tok.ExpiresIn)*time.Second - time.Minute)
	return c.token, nil
}

// query posts an Apicalypse query to an endpoint and decodes the JSON answer into out.
func (c *Client) query(ctx context.Context, endpoint, body string, out any) error {
	for attempt := 0; ; attempt++ {
		token, err := c.accessToken(ctx, attempt > 0)
		if err != nil {
			return err
		}
		if err := c.limit.Wait(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.APIURL+"/"+endpoint, strings.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Client-ID", c.cfg.ClientID)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		resp, err := c.hc.Do(req)
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrUnavailable, endpoint, err)
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			_ = resp.Body.Close() // the token was revoked or expired early: get another, once
			continue
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%w: %s: status %d", ErrUnavailable, endpoint, resp.StatusCode)
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrUnavailable, endpoint, err)
		}
		return nil
	}
}

// Platform is an IGDB platform.
type Platform struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Platforms looks IGDB's platforms up by slug. Slugs IGDB doesn't have are missing from the
// answer. Answers are cached for a day.
func (c *Client) Platforms(ctx context.Context, slugs []string) (map[string]Platform, error) {
	out := map[string]Platform{}
	var ask []string
	now := time.Now()
	c.mu.Lock()
	for _, s := range slugs {
		if !slugRe.MatchString(s) {
			continue
		}
		if cp, ok := c.slugs[s]; ok && now.Before(cp.until) {
			if cp.p != nil {
				out[s] = *cp.p
			}
			continue
		}
		ask = append(ask, `"`+s+`"`)
	}
	c.mu.Unlock()
	if len(ask) == 0 {
		return out, nil
	}
	var found []Platform
	if err := c.query(ctx, "platforms", fmt.Sprintf("fields id,name,slug; where slug = (%s); limit 500;", strings.Join(ask, ",")), &found); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range found {
		out[p.Slug] = p
		c.slugs[p.Slug] = cachedPlatform{p: &p, until: now.Add(24 * time.Hour)}
	}
	for _, q := range ask {
		s := strings.Trim(q, `"`)
		if _, ok := out[s]; !ok {
			c.slugs[s] = cachedPlatform{until: now.Add(time.Hour)}
		}
	}
	return out, nil
}

// Game is one game in the catalog.
type Game struct {
	ID      int64
	Name    string
	Year    int // 0 when unknown
	Summary string
	CoverID string // IGDB image id, or empty
}

// Sort orders a browse without a search.
type Sort string

// The orders.
const (
	SortPopular Sort = "popular"
	SortName    Sort = "name"
)

// gameTypes are the kinds of game the catalog lists: main games, remakes, remasters, expanded
// games and ports; not DLC, bundles, mods or episodes.
const gameTypes = "(0,8,9,10,11)"

// Games lists the games on a platform, limit at a time from offset. A non-empty search
// ranks by IGDB's relevance instead of sort.
func (c *Client) Games(ctx context.Context, platformID int64, search string, sort Sort, offset, limit int) ([]Game, error) {
	var b strings.Builder
	if s := cleanSearch(search); s != "" {
		fmt.Fprintf(&b, "search \"%s\"; ", s)
	}
	b.WriteString("fields name,first_release_date,summary,cover.image_id; ")
	fmt.Fprintf(&b, "where platforms = (%d) & game_type = %s; ", platformID, gameTypes)
	if cleanSearch(search) == "" {
		if sort == SortName {
			b.WriteString("sort name asc; ")
		} else {
			// Unrated games sort last.
			b.WriteString("sort total_rating_count desc; ")
		}
	}
	fmt.Fprintf(&b, "limit %d; offset %d;", limit, offset)

	var raw []struct {
		ID               int64  `json:"id"`
		Name             string `json:"name"`
		FirstReleaseDate int64  `json:"first_release_date"`
		Summary          string `json:"summary"`
		Cover            *struct {
			ImageID string `json:"image_id"`
		} `json:"cover"`
	}
	if err := c.query(ctx, "games", b.String(), &raw); err != nil {
		return nil, err
	}
	out := make([]Game, 0, len(raw))
	for _, r := range raw {
		g := Game{ID: r.ID, Name: r.Name, Summary: r.Summary}
		if r.FirstReleaseDate != 0 {
			g.Year = time.Unix(r.FirstReleaseDate, 0).UTC().Year()
		}
		if r.Cover != nil && ValidImageID(r.Cover.ImageID) {
			g.CoverID = r.Cover.ImageID
		}
		out = append(out, g)
	}
	return out, nil
}

// ErrNotFound means IGDB has no such game on the platform.
var ErrNotFound = errors.New("igdb: no such game")

// Game looks one game up by id, on a platform.
func (c *Client) Game(ctx context.Context, id, platformID int64) (Game, error) {
	var raw []struct {
		ID        int64   `json:"id"`
		Name      string  `json:"name"`
		Platforms []int64 `json:"platforms"`
		Cover     *struct {
			ImageID string `json:"image_id"`
		} `json:"cover"`
	}
	if err := c.query(ctx, "games", fmt.Sprintf("fields name,platforms,cover.image_id; where id = %d; limit 1;", id), &raw); err != nil {
		return Game{}, err
	}
	if len(raw) == 0 || raw[0].Name == "" || !slices.Contains(raw[0].Platforms, platformID) {
		return Game{}, ErrNotFound
	}
	g := Game{ID: raw[0].ID, Name: raw[0].Name}
	if raw[0].Cover != nil && ValidImageID(raw[0].Cover.ImageID) {
		g.CoverID = raw[0].Cover.ImageID
	}
	return g, nil
}

// Details is one game's page.
type Details struct {
	Game
	Storyline   string
	Rating      int // 0 when unrated
	Genres      []string
	Developers  []string
	Screenshots []string // image ids
	Videos      []Video
}

// Video is a trailer or gameplay video on YouTube.
type Video struct {
	Name      string
	YouTubeID string
}

const (
	maxScreenshots = 12
	maxVideos      = 6
)

var youtubeIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// Details looks up one game's details by id, on a platform.
func (c *Client) Details(ctx context.Context, id, platformID int64) (Details, error) {
	var raw []struct {
		ID               int64   `json:"id"`
		Name             string  `json:"name"`
		Platforms        []int64 `json:"platforms"`
		FirstReleaseDate int64   `json:"first_release_date"`
		Summary          string  `json:"summary"`
		Storyline        string  `json:"storyline"`
		TotalRating      float64 `json:"total_rating"`
		Cover            *struct {
			ImageID string `json:"image_id"`
		} `json:"cover"`
		Genres []struct {
			Name string `json:"name"`
		} `json:"genres"`
		InvolvedCompanies []struct {
			Developer bool `json:"developer"`
			Company   struct {
				Name string `json:"name"`
			} `json:"company"`
		} `json:"involved_companies"`
		Screenshots []struct {
			ImageID string `json:"image_id"`
		} `json:"screenshots"`
		Videos []struct {
			Name    string `json:"name"`
			VideoID string `json:"video_id"`
		} `json:"videos"`
	}
	q := fmt.Sprintf("fields name,platforms,first_release_date,summary,storyline,total_rating,cover.image_id,"+
		"genres.name,involved_companies.developer,involved_companies.company.name,screenshots.image_id,"+
		"videos.name,videos.video_id; where id = %d; limit 1;", id)
	if err := c.query(ctx, "games", q, &raw); err != nil {
		return Details{}, err
	}
	if len(raw) == 0 || raw[0].Name == "" || !slices.Contains(raw[0].Platforms, platformID) {
		return Details{}, ErrNotFound
	}
	r := raw[0]
	d := Details{Game: Game{ID: r.ID, Name: r.Name, Summary: r.Summary}, Storyline: r.Storyline, Rating: int(r.TotalRating + 0.5)}
	if r.FirstReleaseDate != 0 {
		d.Year = time.Unix(r.FirstReleaseDate, 0).UTC().Year()
	}
	if r.Cover != nil && ValidImageID(r.Cover.ImageID) {
		d.CoverID = r.Cover.ImageID
	}
	d.Genres = []string{}
	for _, g := range r.Genres {
		if g.Name != "" {
			d.Genres = append(d.Genres, g.Name)
		}
	}
	d.Developers = []string{}
	for _, ic := range r.InvolvedCompanies {
		if ic.Developer && ic.Company.Name != "" && !slices.Contains(d.Developers, ic.Company.Name) {
			d.Developers = append(d.Developers, ic.Company.Name)
		}
	}
	d.Screenshots = []string{}
	for _, sc := range r.Screenshots {
		if ValidImageID(sc.ImageID) && len(d.Screenshots) < maxScreenshots {
			d.Screenshots = append(d.Screenshots, sc.ImageID)
		}
	}
	d.Videos = []Video{}
	for _, v := range r.Videos {
		if youtubeIDRe.MatchString(v.VideoID) && len(d.Videos) < maxVideos {
			d.Videos = append(d.Videos, Video{Name: v.Name, YouTubeID: v.VideoID})
		}
	}
	return d, nil
}

// cleanSearch makes user text safe inside an Apicalypse string: no quotes, backslashes or
// control characters, at most 100 bytes.
func cleanSearch(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	for len(s) > 100 {
		_, size := lastRune(s)
		s = s[:len(s)-size]
	}
	return s
}

func lastRune(s string) (rune, int) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i]&0xC0 != 0x80 {
			return rune(s[i]), len(s) - i
		}
	}
	return 0, len(s)
}

var imageIDRe = regexp.MustCompile(`^[a-z0-9]{1,40}$`)

// ValidImageID reports whether id looks like an IGDB image id.
func ValidImageID(id string) bool { return imageIDRe.MatchString(id) }

// MaxCoverBytes caps a cover or screenshot.
const MaxCoverBytes = 2 << 20

// Cover fetches a cover (264×374 JPEG). The caller closes the body.
func (c *Client) Cover(ctx context.Context, imageID string) (io.ReadCloser, int64, error) {
	return c.image(ctx, "t_cover_big", imageID)
}

// Screenshot fetches a screenshot (889×500 JPEG). The caller closes the body.
func (c *Client) Screenshot(ctx context.Context, imageID string) (io.ReadCloser, int64, error) {
	return c.image(ctx, "t_screenshot_big", imageID)
}

func (c *Client) image(ctx context.Context, size, imageID string) (io.ReadCloser, int64, error) {
	if !ValidImageID(imageID) {
		return nil, 0, errors.New("igdb: invalid image id")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.ImageURL+"/"+size+"/"+imageID+".jpg", nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: image: %w", ErrUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK || resp.ContentLength > MaxCoverBytes ||
		!strings.HasPrefix(resp.Header.Get("Content-Type"), "image/jpeg") {
		_ = resp.Body.Close()
		return nil, 0, fmt.Errorf("%w: image: status %d, %s", ErrUnavailable, resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	return resp.Body, resp.ContentLength, nil
}
