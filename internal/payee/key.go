// Package payee turns noisy bank descriptions into stable merchant keys.
package payee

import (
	"strings"
	"unicode"
)

// Card processors and aggregators that prefix the real merchant name.
var processorPrefixes = []string{
	"PAYPAL *", "SQ *", "SQ*", "TST* ", "TST*", "WL *", "WL*", "PY *", "DD *", "PP*",
	"CHECKCARD ", "POS ", "SP ",
}

const maxKeyWords = 4

// Normalize upper-cases s and collapses runs of whitespace.
func Normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToUpper(s)), " ")
}

// Matches reports whether payee contains match, ignoring case and spacing.
func Matches(payee, match string) bool {
	m := Normalize(match)
	return m != "" && strings.Contains(Normalize(payee), m)
}

// Key reduces a bank description to the part that identifies the merchant,
// e.g. "AMAZON MKTPL*5151O6EQ0" → "AMAZON MKTPL" and
// "SP HUBMAN AND CHUBGIRL" → "HUBMAN AND CHUBGIRL". The key is always a
// substring of Normalize(s), so it works as a "contains" rule.
func Key(s string) string {
	full := Normalize(s)
	k := full
	if i := strings.IndexByte(k, ','); i >= 0 {
		k = k[:i]
	}
	for _, p := range processorPrefixes {
		if strings.HasPrefix(k, p) && len(k) > len(p) {
			k = k[len(p):]
			break
		}
	}
	if i := strings.IndexByte(k, '*'); i > 0 {
		k = k[:i]
	}
	toks := strings.Fields(k)
	start := 0
	for start < len(toks) && !hasLetter(toks[start]) {
		start++
	}
	end, words := start, 0
	for end < len(toks) && !hasDigit(toks[end]) {
		if hasLetter(toks[end]) {
			if words == maxKeyWords {
				break
			}
			words++
		}
		end++
	}
	k = strings.Trim(strings.Join(toks[start:end], " "), " -#.&/")
	if k == "" {
		return full
	}
	return k
}

func hasLetter(s string) bool {
	return strings.IndexFunc(s, unicode.IsLetter) >= 0
}

func hasDigit(s string) bool {
	return strings.IndexFunc(s, unicode.IsDigit) >= 0
}
