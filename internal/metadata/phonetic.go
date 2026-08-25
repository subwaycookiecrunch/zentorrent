package metadata

// phonetic.go — Double Metaphone (compact form) plus a Devanagari→Latin
// transliteration pass so Hindi/Marathi titles land in the same phonetic
// space as their romanized aliases ("बाहुबली" ≈ "bahubali" ≈ "baahubali").
//
// The goal is not linguistic perfection; it is stable collision of the
// misspellings people actually type into a search box.

import (
	"strings"
)

// HindiConsonants maps Devanagari letters to their ITRANS-lite roman form.
// Vowel signs are handled in devanagariToLatin.
var hindiVowels = map[rune]string{
	'अ': "a", 'आ': "aa", 'इ': "i", 'ई': "ee", 'उ': "u", 'ऊ': "oo",
	'ए': "e", 'ऐ': "ai", 'ओ': "o", 'औ': "au",
}

var hindiMatras = map[rune]string{
	'ा': "aa", 'ि': "i", 'ी': "ee", 'ु': "u", 'ू': "oo",
	'े': "e", 'ै': "ai", 'ो': "o", 'ौ': "au", 'ं': "n", 'ँ': "n",
	'्': "", // virama: suppress inherent vowel
}

var hindiConsonants = map[rune]string{
	'क': "k", 'ख': "kh", 'ग': "g", 'घ': "gh", 'ङ': "n",
	'च': "ch", 'छ': "chh", 'ज': "j", 'झ': "jh", 'ञ': "ny",
	'ट': "t", 'ठ': "th", 'ड': "d", 'ढ': "dh", 'ण': "n",
	'त': "t", 'थ': "th", 'द': "d", 'ध': "dh", 'न': "n",
	'प': "p", 'फ': "ph", 'ब': "b", 'भ': "bh", 'म': "m",
	'य': "y", 'र': "r", 'ल': "l", 'व': "v",
	'श': "sh", 'ष': "sh", 'स': "s", 'ह': "h",
}

// DevanagariToLatin transliterates a Devanagari string to a rough Latin
// phonetic spelling. Non-Devanagari runes pass through unchanged.
func DevanagariToLatin(s string) string {
	var b strings.Builder
	pendingVirama := false
	for _, r := range s {
		if r == '्' {
			pendingVirama = true
			continue
		}
		if v, ok := hindiConsonants[r]; ok {
			b.WriteString(v)
			if !strings.HasSuffix(v, "h") && !pendingVirama {
				b.WriteString("a") // inherent vowel, stripped by metaphone anyway
			}
			pendingVirama = false
			continue
		}
		pendingVirama = false
		if m, ok := hindiMatras[r]; ok {
			clean := strings.Trim(m, "', ")
			b.WriteString(clean)
			continue
		}
		if v, ok := hindiVowels[r]; ok {
			b.WriteString(v)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// doubleMetaphone computes a consonant-skeleton phonetic key: vowels and
// semivowels are dropped (a leading vowel becomes a marker), common digraph
// variants fold together (PH->F, CH/SH->X, TH->T), and duplicate letters
// collapse. The result is that "drishiam"/"drishyam", "bahubali"/
// "baahubali" and "interstelar"/"interstellar" all reduce to one key.
func doubleMetaphone(s string) string {
	s = strings.ToUpper(s)
	var pre strings.Builder

	// Fold digraph variants before skeletonizing.
	r := strings.NewReplacer(
		"PH", "F", "SCH", "X", "CH", "X", "SH", "X", "TCH", "X",
		"TH", "T", "CK", "K", "DT", "T", "GH", "G", "KN", "N", "WR", "R",
		"QU", "KW",
	)
	s = r.Replace(s)

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case 'A', 'E', 'I', 'O', 'U', 'Y', 'W':
			if i == 0 {
				pre.WriteByte('A') // preserve the fact it started with a vowel
			}
			continue
		case 'B', 'C', 'D', 'F', 'G', 'H', 'J', 'K', 'L', 'M',
			'N', 'P', 'Q', 'R', 'S', 'T', 'V', 'X', 'Z':
			pre.WriteByte(c)
		}
	}

	// Collapse consecutive duplicates.
	var b strings.Builder
	prev := byte(0)
	for i := 0; i < len(pre.String()); i++ {
		c := pre.String()[i]
		if c != prev {
			b.WriteByte(c)
			prev = c
		}
	}
	return b.String()
}

// PhoneticKey reduces any title (English, romanized Indian, or Devanagari)
// to its phonetic matching key.
func PhoneticKey(s string) string {
	s = NormalizeQuery(s)
	if containsDevanagari(s) || containsDevanagari(strings.TrimSpace(s)) {
		s = DevanagariToLatin(s)
	}
	key := doubleMetaphone(s)
	if key == "" {
		key = strings.ReplaceAll(NormalizeQuery(s), " ", "")
	}
	return key
}

func containsDevanagari(s string) bool {
	for _, r := range s {
		if r >= 0x0900 && r <= 0x097F {
			return true
		}
	}
	return false
}

// PhoneticsMatch reports whether two spellings are phonetically equivalent.
func PhoneticsMatch(a, b string) bool {
	ka, kb := PhoneticKey(a), PhoneticKey(b)
	return ka != "" && ka == kb
}
