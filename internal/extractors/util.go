package extractors

import "strings"

func strings_unescape(s string) string {
	return strings.NewReplacer(`&`, "&", `\/`, "/").Replace(s)
}
