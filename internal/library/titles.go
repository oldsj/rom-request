package library

import (
	"path"
	"strings"
	"unicode"
)

// TitleKey reduces a game's name, or a ROM's file or folder name, to a key that matches across
// naming styles: "Legend of Zelda, The - A Link to the Past (USA) [!].sfc" and IGDB's
// "The Legend of Zelda: A Link to the Past" both become "legendofzeldaalinktothepast".
// It drops a file extension, tags in brackets, a leading or trailing ", The", accents,
// punctuation and spaces, and spells "&" as "and".
func TitleKey(name string) string {
	if ext := path.Ext(name); len(ext) >= 2 && len(ext) <= 5 && !strings.ContainsAny(ext, " ") && !isDigits(ext[1:]) {
		name = strings.TrimSuffix(name, ext)
	}
	name = strings.ToLower(stripBrackets(name))
	name = strings.ReplaceAll(name, "&", " and ")
	words := strings.FieldsFunc(name, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	if len(words) > 0 && words[0] == "the" {
		words = words[1:]
	}
	var b strings.Builder
	for i, w := range words {
		// "Zelda, The - A Link…": an article moved behind a comma.
		if w == "the" && i > 0 && strings.Contains(name, ", the") && (i == len(words)-1 || words[i+1] == "-" || words[i+1] == ":") {
			continue
		}
		for _, r := range fold(w) {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// stripBrackets removes (…), […] and {…} groups.
func stripBrackets(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

var accents = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ä", "a", "ã", "a", "å", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "ö", "o", "õ", "o", "ø", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ñ", "n", "ç", "c", "ß", "ss",
)

func fold(s string) string { return accents.Replace(s) }
