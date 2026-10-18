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

// Words that don't identify a merchant on their own.
var skipTokens = map[string]bool{
	"AND": true, "THE": true, "OF": true, "FOR": true,
	"COM": true, "WWW": true, "INC": true, "LLC": true,
}

// Generic words. They can match, but they don't make two payees the same merchant.
var weakTokens = map[string]bool{
	"STORE": true, "SHOP": true, "MARKET": true, "PURCHASE": true,
	"PAYMENT": true, "MOBILE": true, "ONLINE": true, "TRANSFER": true,
	"BILL": true, "ORDER": true, "POS": true, "RECEIVED": true,
}

// Tokens returns the merchant words in s, after Key's cleanup.
func Tokens(s string) []string {
	raw := strings.FieldsFunc(Key(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var out []string
	for _, t := range raw {
		if len(t) < 3 || skipTokens[t] {
			continue
		}
		out = append(out, t)
	}
	return out
}

// Score reports how confidently a and b are the same merchant, from 0 to 1.
// 1 is the same key. A truncated name ("HUBMAN AND CHUBGI") and a shared
// distinctive word ("STEAM" inside "STEAMGAMES") still score above 0.
func Score(a, b string) float64 {
	ka, kb := Key(a), Key(b)
	if ka == "" || kb == "" {
		return 0
	}
	if ka == kb {
		return 1
	}
	if prefixKey(ka, kb) {
		return 0.92
	}
	if tokenCovered(ka, kb) {
		return 0.8
	}
	return 0
}

// Similar reports whether Score(a, b) is high enough to treat them as one merchant.
func Similar(a, b string) bool {
	return Score(a, b) > 0
}

// TitleName is a short category label for a merchant, e.g. "APPLE.COM/BILL" → "Apple".
func TitleName(s string) string {
	toks := Tokens(s)
	word := ""
	if len(toks) > 0 {
		word = toks[0]
	} else {
		word = Key(s)
	}
	if word == "" {
		return ""
	}
	lower := strings.ToLower(word)
	return strings.ToUpper(lower[:1]) + lower[1:]
}

func prefixKey(a, b string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	return len(a) >= 8 && strings.HasPrefix(b, a)
}

func tokenCovered(ka, kb string) bool {
	ta, tb := Tokens(ka), Tokens(kb)
	if len(ta) == 0 || len(tb) == 0 {
		return false
	}
	short, long := ta, tb
	if len(tb) < len(ta) {
		short, long = tb, ta
	}
	strong := false
	for _, s := range short {
		if !tokenHits(s, long) {
			return false
		}
		if len(s) >= 5 && !weakTokens[s] {
			strong = true
		}
	}
	return strong
}

func tokenHits(s string, list []string) bool {
	for _, o := range list {
		if s == o || tokenPrefix(s, o) {
			return true
		}
	}
	return false
}

func tokenPrefix(a, b string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	return len(a) >= 5 && strings.HasPrefix(b, a)
}
