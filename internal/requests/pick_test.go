package requests

import (
	"testing"

	"github.com/oldsj/rom-request/internal/prowlarr"
)

func rel(title string, seeders int, size int64) prowlarr.Release {
	return prowlarr.Release{Title: title, Seeders: &seeders, Size: size}
}

func best(g Game, rs ...prowlarr.Release) string {
	r := Rank(g, rs)
	if len(r) == 0 {
		return ""
	}
	return r[0].Title
}

// Release names as Prowlarr returned them (The Pirate Bay, LimeTorrents, BlueRoms), 2026-11-23.
func TestRank(t *testing.T) {
	gba := Game{Name: "Pokémon Sapphire Version", Folder: "GBA", Slug: "gba", PlatformName: "Game Boy Advance"}
	if got := best(gba,
		rel("1324  Pokemon Alpha Sapphire (Europe) (En Ja Fr De Es It Ko) Decrypted", 101, 1932735283),
		rel("[GameCube] Pokemon Box  Ruby   Sapphire (Europe) (En Fr De Es It) (Rev 1) Y2K12", 7, 1460288880),
		rel("Pokemon Alpha Sapphire Version Keygen (3DS)", 1, 3355443),
		rel("Pokémon Sapphire Version - Game Boy Advance", 1, 4928307),
	); got != "Pokémon Sapphire Version - Game Boy Advance" {
		t.Errorf("sapphire: %q", got)
	}

	psx := Game{Name: "Castlevania: Symphony of the Night", Folder: "PS1", Slug: "ps", PlatformName: "PlayStation"}
	if got := best(psx,
		rel("Castlevania Symphony of the Night (PT BR) (VCD)", 4, 359609139),
		rel("Castlevania - Symphony of the Night - OST [Mp3 128 Kbps] [TNT Vi", 3, 65286456),
		rel("PS1 - Castlevania symphony of the night [1 CD - PAL - ENG] [TNT", 4, 395699723),
		rel("[PSX] Castlevania.Symphony.Of.The.Night.NTSC.US", 18, 391753817),
		rel("Castlevania - Symphony of the Night + emulador", 2, 325813528),
		rel("(PSX-PSP) Castlevania:Symphony of the Night converted properly (", 8, 410093541),
		rel("Castlevania Symphony of the Night PS4", 50, 391753817),
	); got != "[PSX] Castlevania.Symphony.Of.The.Night.NTSC.US" {
		t.Errorf("castlevania: %q", got)
	}

	// Region beats seeders; nothing without a seeder; no packs of every game.
	zelda := Game{Name: "The Legend of Zelda: The Minish Cap", Folder: "GBA", Slug: "gba", PlatformName: "Game Boy Advance"}
	if got := best(zelda,
		rel("Legend of Zelda, The - The Minish Cap (Europe) GBA", 40, 16<<20),
		rel("Legend of Zelda, The - The Minish Cap (USA) GBA", 12, 16<<20),
		rel("Legend of Zelda The Minish Cap USA GBA", 0, 16<<20),
		rel("GBA Legend of Zelda Minish Cap + 2000 games", 90, 20<<30),
	); got != "Legend of Zelda, The - The Minish Cap (USA) GBA" {
		t.Errorf("zelda: %q", got)
	}
	if got := best(zelda, rel("Legend of Zelda Four Swords GBA", 10, 8<<20)); got != "" {
		t.Errorf("another game: %q", got)
	}

	// Roman numerals and digits are the same.
	ff := Game{Name: "Final Fantasy VI Advance", Folder: "gba", Slug: "gba", PlatformName: "Game Boy Advance"}
	if got := best(ff, rel("Final Fantasy 6 Advance (U) [GBA]", 3, 8<<20)); got == "" {
		t.Error("final fantasy: no match")
	}
}

func TestQueries(t *testing.T) {
	got := Queries(Game{Name: "Pokémon Sapphire Version"})
	if len(got) != 2 || got[0] != "pokemon sapphire version" || got[1] != "pokemon sapphire" {
		t.Errorf("queries: %q", got)
	}
	if got := Queries(Game{Name: "Metroid Fusion"}); len(got) != 1 || got[0] != "metroid fusion" {
		t.Errorf("queries: %q", got)
	}
}
