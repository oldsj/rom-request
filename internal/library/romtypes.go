package library

import (
	"path"
	"slices"
	"strings"
)

// archives are what RomM accepts for any platform: it reads (and EmulatorJS plays) a game
// inside a zip or 7z.
var archives = []string{".zip", ".7z"}

// romTypes are each platform's game file extensions, keyed by RomM's platform slug. A download
// for a known platform must hold at least one of them (or an archive), so a release that is only
// a web page and its images never lands in the library as a game.
var romTypes = map[string][]string{
	"gb":       {".gb", ".sgb"},
	"gbc":      {".gbc", ".gb"},
	"gba":      {".gba"},
	"nds":      {".nds", ".dsi"},
	"nes":      {".nes", ".fds", ".unf", ".unif"},
	"snes":     {".sfc", ".smc", ".fig", ".swc", ".bs"},
	"n64":      {".z64", ".n64", ".v64"},
	"genesis":  {".md", ".gen", ".smd", ".bin", ".68k", ".sgd"},
	"sms":      {".sms"},
	"gamegear": {".gg"},
	"psx":      {".cue", ".bin", ".chd", ".iso", ".img", ".pbp", ".m3u", ".ecm"},
	"xbox":     {".iso", ".xiso"},
}

// platformSlugs maps common library folder names to RomM's slugs where they differ.
var platformSlugs = map[string]string{
	"ps1":          "psx",
	"playstation":  "psx",
	"megadrive":    "genesis",
	"md":           "genesis",
	"gg":           "gamegear",
	"mastersystem": "sms",
}

// junkTypes are never imported: indexer landing pages, scraper metadata and artwork that some
// releases carry beside (or instead of) the game. License and readme text files are kept.
var junkTypes = []string{
	".html", ".htm", ".mht", ".json", ".xml", ".url", ".lnk", ".webloc",
	".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".ico", ".svg",
	".exe", ".bat", ".cmd", ".scr", ".js",
}

func ext(name string) string { return strings.ToLower(path.Ext(name)) }

// isJunk reports whether a downloaded file is skipped on import.
func isJunk(name string) bool { return slices.Contains(junkTypes, ext(name)) }

// isROM reports whether name is a game file for the platform folder. A folder RomM's slug isn't
// known for accepts any file that isn't junk.
func isROM(platform, name string) bool {
	e := ext(name)
	if slices.Contains(archives, e) {
		return true
	}
	slug := strings.ToLower(platform)
	if s, ok := platformSlugs[slug]; ok {
		slug = s
	}
	types, known := romTypes[slug]
	if !known {
		return !isJunk(name)
	}
	return slices.Contains(types, e)
}
