package requests

import (
	"math"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/oldsj/rom-request/internal/prowlarr"
)

// A catalog request names a game, not a release. The picker turns Prowlarr's results for the
// game into one release: a candidate must name every word of the game's title and the platform,
// and must not look like something other than the game (a soundtrack, a crack, a pack of many
// games). Among the candidates, fewer extra words, a preferred region, and more seeders win.

// Game is what the picker knows about a requested game.
type Game struct {
	Name string
	// Folder is the library folder; Slug and PlatformName are IGDB's for the platform.
	Folder, Slug, PlatformName string
}

// platformWords are names releases use for a platform, by IGDB slug, beyond its IGDB name, its
// slug and the folder's name. not lists names of neighbouring platforms that contain these.
var platformWords = map[string]struct{ also, not []string }{
	"gba":                     {also: []string{"gba", "game boy advance", "gameboy advance"}},
	"gb":                      {also: []string{"gb", "game boy", "gameboy", "dmg"}, not: []string{"advance", "color", "colour", "gbc", "gba"}},
	"gbc":                     {also: []string{"gbc", "game boy color", "gameboy color", "game boy colour"}},
	"nes":                     {also: []string{"nes", "famicom"}, not: []string{"snes", "super nintendo", "super famicom", "disk system"}},
	"snes":                    {also: []string{"snes", "super nintendo", "super famicom", "sfc"}},
	"n64":                     {also: []string{"n64", "nintendo 64"}},
	"nds":                     {also: []string{"nds", "nintendo ds"}, not: []string{"3ds", "dsi"}},
	"3ds":                     {also: []string{"3ds", "nintendo 3ds"}},
	"ngc":                     {also: []string{"gamecube", "gcn", "ngc", "game cube"}},
	"wii":                     {also: []string{"wii"}, not: []string{"wii u", "wiiu"}},
	"ps":                      {also: []string{"psx", "ps1", "psone", "ps one", "playstation"}, not: []string{"ps2", "ps3", "ps4", "ps5", "psp", "psvita", "playstation 2", "playstation 3", "playstation 4", "playstation 5", "playstation portable", "playstation vita"}},
	"ps2":                     {also: []string{"ps2", "playstation 2"}},
	"psp":                     {also: []string{"psp", "playstation portable"}},
	"genesis-slash-megadrive": {also: []string{"genesis", "mega drive", "megadrive", "md", "smd"}, not: []string{"32x", "mega cd", "sega cd"}},
	"sega-cd":                 {also: []string{"sega cd", "mega cd", "segacd", "megacd"}},
	"sega32":                  {also: []string{"32x", "sega 32x"}},
	"sms":                     {also: []string{"sms", "master system", "mastersystem"}},
	"gamegear":                {also: []string{"game gear", "gamegear", "gg"}},
	"saturn":                  {also: []string{"saturn", "sega saturn"}},
	"dc":                      {also: []string{"dreamcast", "dc"}},
	"turbografx16--1":         {also: []string{"turbografx", "turbografx 16", "tg16", "pc engine", "pce"}, not: []string{"cd"}},
	"xbox":                    {also: []string{"xbox"}, not: []string{"xbox 360", "x360", "xbox one", "xbox series"}},
	"atari2600":               {also: []string{"atari 2600", "a2600", "vcs"}},
}

// unwanted are words of releases that aren't the game itself.
var unwanted = words("ost soundtrack soundtracks mp3 flac music keygen crack cracked trainer cheat cheats " +
	"artbook guide guides manual manuals wallpaper wallpapers theme themes dlc update patch emulator " +
	"emulador bios collection compilation pack romset roms anthology bundle mod hack hacked " +
	"translation translated demo trailer video")

// noise words don't count as extra: release tags, regions, languages, formats.
var noise = words("the a an of and version rom roms iso bin cue chd cso rvz wbfs zip 7z rar " +
	"usa us u ntsc europe eur e pal world w en fr de es it ja jp jpn japan multi multi5 rev " +
	"v1 disc discs cd")

// regionScore prefers releases for the USA, then Europe and the world, then the rest.
func regionScore(t map[string]bool) int {
	switch {
	case t["usa"] || t["us"] || t["ntsc"] && t["u"] || t["ntscu"]:
		return 2
	case t["world"] || t["europe"] || t["eur"] || t["en"] || t["pal"]:
		return 1
	case t["japan"] || t["jpn"] || t["jp"] || t["ja"]:
		return -1
	}
	return 0
}

// maxBytes caps a release's size for platforms whose games are small, so a pack of every game
// for one isn't taken for one game.
var maxBytes = map[string]int64{
	"gb": 64 << 20, "gbc": 64 << 20, "gba": 256 << 20, "nes": 64 << 20, "snes": 64 << 20,
	"n64": 512 << 20, "sms": 64 << 20, "gamegear": 64 << 20, "genesis-slash-megadrive": 64 << 20,
	"sega32": 64 << 20, "atari2600": 16 << 20, "nds": 1 << 30, "3ds": 8 << 30,
}

// Rank returns the releases in found that are g, best first; none when nothing fits.
func Rank(g Game, found []prowlarr.Release) []prowlarr.Release {
	want := significant(tokens(g.Name))
	if len(want) == 0 {
		return nil
	}
	hints, nots := platformHints(g)
	type cand struct {
		r                      prowlarr.Release
		extra, region, seeders int
	}
	var cands []cand
	for _, r := range found {
		ts := tokens(r.Title)
		set := map[string]bool{}
		for _, t := range ts {
			set[t] = true
		}
		if !containsAll(set, want) || !hasPhrase(ts, hints) || hasPhrase(ts, nots) {
			continue
		}
		if slices.ContainsFunc(ts, func(t string) bool { return unwanted[t] && !slices.Contains(want, t) }) {
			continue
		}
		seeders := 0
		if r.Seeders != nil {
			seeders = *r.Seeders
		}
		if seeders < 1 || r.Size <= 0 {
			continue
		}
		if limit, ok := maxBytes[g.Slug]; ok && r.Size > limit {
			continue
		}
		extra := 0
		for _, t := range ts {
			if !noise[t] && !slices.Contains(want, t) && !hasPhrase([]string{t}, hints) {
				extra++
			}
		}
		cands = append(cands, cand{r: r, extra: extra, region: regionScore(set), seeders: seeders})
	}
	// Few extra words first (in steps, so a word or two doesn't outweigh the region), then the
	// region, then seeders on a log scale, then fewer extra words exactly.
	slices.SortStableFunc(cands, func(a, b cand) int {
		if d := min(a.extra, 6)/3 - min(b.extra, 6)/3; d != 0 {
			return d
		}
		if d := b.region - a.region; d != 0 {
			return d
		}
		if d := int(math.Log2(float64(b.seeders))) - int(math.Log2(float64(a.seeders))); d != 0 {
			return d
		}
		return a.extra - b.extra
	})
	out := make([]prowlarr.Release, len(cands))
	for i, c := range cands {
		out[i] = c.r
	}
	return out
}

// Queries are the Prowlarr searches to try for g, in order: the cleaned name, then without
// words releases often leave out ("Version", a subtitle's article).
func Queries(g Game) []string {
	full := strings.Join(tokens(g.Name), " ")
	short := strings.Join(significant(tokens(g.Name)), " ")
	out := []string{full}
	if short != full && short != "" {
		out = append(out, short)
	}
	return out
}

func platformHints(g Game) (hints, nots [][]string) {
	add := func(s string) {
		if t := tokens(s); len(t) > 0 {
			hints = append(hints, t)
		}
	}
	add(g.Folder)
	add(g.Slug)
	add(g.PlatformName)
	if w, ok := platformWords[g.Slug]; ok {
		for _, s := range w.also {
			add(s)
		}
		for _, s := range w.not {
			nots = append(nots, tokens(s))
		}
	}
	return hints, nots
}

// hasPhrase reports whether ts contains any of the phrases as consecutive tokens.
func hasPhrase(ts []string, phrases [][]string) bool {
	for _, p := range phrases {
		for i := 0; i+len(p) <= len(ts); i++ {
			if slices.Equal(ts[i:i+len(p)], p) {
				return true
			}
		}
		// A phrase written as one word: "gameboy", "megadrive".
		if len(p) > 1 && slices.Contains(ts, strings.Join(p, "")) {
			return true
		}
	}
	return false
}

func containsAll(set map[string]bool, want []string) bool {
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

// significant drops the words a release name often leaves out.
func significant(ts []string) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		if t != "the" && t != "a" && t != "an" && t != "version" && t != "of" && t != "and" {
			out = append(out, t)
		}
	}
	return out
}

var romans = map[string]string{"ii": "2", "iii": "3", "iv": "4", "v": "5", "vi": "6", "vii": "7", "viii": "8", "ix": "9", "x": "10", "xi": "11", "xii": "12"}

// tokens is a name as lower-case ASCII words: accents removed, "&" as "and", Roman numerals
// (II to XII) as digits, every other character a separator.
func tokens(s string) []string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case r == '&':
			b.WriteString(" and ")
		case r == '\'' || r == '’':
			// "Link's" is "links", not "link s".
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteByte(' ')
		}
	}
	ts := strings.Fields(b.String())
	for i, t := range ts {
		if d, ok := romans[t]; ok {
			ts[i] = d
		}
	}
	return ts
}

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}
