package metadata

// fuzzy.go — Damerau-Levenshtein (optimal string alignment) distance and a
// normalized similarity score for typo-tolerant ranking.

import (
	"strings"
)

// DamerauLevenshtein computes the OSA edit distance between a and b:
// insertions, deletions, substitutions, and adjacent transpositions.
func DamerauLevenshtein(a, b string) int {
	ra, rb := []rune(strings.ToLower(a)), []rune(strings.ToLower(b))
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	d := make([][]int, la+1)
	for i := range d {
		d[i] = make([]int, lb+1)
		d[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		d[0][j] = j
	}

	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			m := min3(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				if t := d[i-2][j-2] + 1; t < m {
					m = t
				}
			}
			d[i][j] = m
		}
	}
	return d[la][lb]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// FuzzyScore converts edit distance into a 0..1 similarity.
func FuzzyScore(a, b string) float64 {
	maxLen := len([]rune(a))
	if n := len([]rune(b)); n > maxLen {
		maxLen = n
	}
	if maxLen == 0 {
		return 1
	}
	return 1 - float64(DamerauLevenshtein(a, b))/float64(maxLen)
}
