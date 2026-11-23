package library

import "testing"

func TestTitleKey(t *testing.T) {
	same := [][2]string{
		{"The Legend of Zelda: A Link to the Past", "Legend of Zelda, The - A Link to the Past (USA) [!].sfc"},
		{"Pokémon FireRed Version", "Pokemon - FireRed Version (USA, Europe) (Rev 1).gba"},
		{"Super Mario Bros. 3", "Super Mario Bros. 3 (USA).nes"},
		{"Ratchet & Clank", "Ratchet and Clank (USA)"},
		{"Final Fantasy VII", "Final Fantasy VII (USA) (Disc 1).chd"},
		{"Metroid Fusion", "Metroid Fusion.zip"},
		{"F-Zero", "F-Zero (USA).sfc"},
	}
	for _, p := range same {
		if a, b := TitleKey(p[0]), TitleKey(p[1]); a != b {
			t.Errorf("%q → %q, %q → %q", p[0], a, p[1], b)
		}
	}
	different := [][2]string{
		{"Super Mario Bros.", "Super Mario Bros. 3 (USA).nes"},
		{"Final Fantasy VII", "Final Fantasy VIII (USA).chd"},
	}
	for _, p := range different {
		if TitleKey(p[0]) == TitleKey(p[1]) {
			t.Errorf("%q matched %q", p[0], p[1])
		}
	}
}
