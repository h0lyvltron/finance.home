package importcsv

import (
	"crypto/sha1"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"finance.home/internal/money"
)

// Profile describes how to map CSV columns into ledger fields.
type Profile struct {
	DateColumn   string
	DateFormat   string // auto|YYYY-MM-DD|MM/DD/YYYY|DD/MM/YYYY|MDY_slash|DMY_slash
	AmountMode   string // signed|debit_credit
	AmountColumn string
	DebitColumn  string
	CreditColumn string
	PayeeColumn  string
	NotesColumn  string
	InvertSign   bool
	HasHeader    bool
	Delimiter    rune
}

type ParsedRow struct {
	Index   int
	Raw     map[string]string
	Date    string
	Amount  int64
	Payee   string
	Notes   string
	Error   string
}

// DetectHeaders reads the first record as headers (or synthesizes col_N).
func DetectHeaders(r io.Reader, delim rune, hasHeader bool) ([]string, [][]string, error) {
	if delim == 0 {
		delim = ','
	}
	cr := csv.NewReader(r)
	cr.Comma = delim
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = true

	all, err := cr.ReadAll()
	if err != nil {
		return nil, nil, err
	}
	if len(all) == 0 {
		return nil, nil, fmt.Errorf("empty CSV")
	}
	var headers []string
	var rows [][]string
	if hasHeader {
		start := headerRowIndex(all)
		headers = normalizeHeaders(all[start])
		rows = all[start+1:]
	} else {
		n := len(all[0])
		headers = make([]string, n)
		for i := 0; i < n; i++ {
			headers[i] = fmt.Sprintf("col_%d", i+1)
		}
		rows = all
	}
	return headers, rows, nil
}

// headerRowIndex skips bank preamble lines (account number, balance, ...) that
// some exports put above the real header row.
func headerRowIndex(all [][]string) int {
	widest := 0
	for _, r := range all {
		if n := nonEmptyCount(r); n > widest {
			widest = n
		}
	}
	if widest <= 1 {
		return 0
	}
	need := widest
	if widest > 3 {
		need-- // tolerate one unnamed header column
	}
	for i, r := range all {
		if nonEmptyCount(r) >= need {
			return i
		}
	}
	return 0
}

func nonEmptyCount(r []string) int {
	n := 0
	for _, s := range r {
		if strings.TrimSpace(s) != "" {
			n++
		}
	}
	return n
}

func normalizeHeaders(h []string) []string {
	out := make([]string, len(h))
	seen := map[string]int{}
	for i, s := range h {
		s = strings.TrimSpace(strings.TrimPrefix(s, "\ufeff"))
		if s == "" {
			s = fmt.Sprintf("col_%d", i+1)
		}
		base := s
		if n, ok := seen[strings.ToLower(base)]; ok {
			s = fmt.Sprintf("%s_%d", base, n+1)
			seen[strings.ToLower(base)] = n + 1
		} else {
			seen[strings.ToLower(base)] = 1
		}
		out[i] = s
	}
	return out
}

func ParseRows(headers []string, rows [][]string, p Profile) []ParsedRow {
	out := make([]ParsedRow, 0, len(rows))
	payeeIdx := -1
	for j, h := range headers {
		if p.PayeeColumn != "" && h == p.PayeeColumn {
			payeeIdx = j
		}
	}
	trailing := trailingDelimiter(rows, len(headers))
	for i, cols := range rows {
		cols = fitColumns(cols, len(headers), payeeIdx, p.Delimiter, trailing)
		raw := map[string]string{}
		for j, h := range headers {
			if j < len(cols) {
				raw[h] = cleanCell(cols[j])
			} else {
				raw[h] = ""
			}
		}
		if isBlankRow(raw) {
			continue
		}
		pr := ParsedRow{Index: i, Raw: raw}
		dateRaw := raw[p.DateColumn]
		date, err := ParseDate(dateRaw, p.DateFormat)
		if err != nil {
			pr.Error = "date: " + err.Error()
			out = append(out, pr)
			continue
		}
		pr.Date = date

		amt, err := parseAmount(raw, p)
		if err != nil {
			pr.Error = "amount: " + err.Error()
			out = append(out, pr)
			continue
		}
		if p.InvertSign {
			amt = -amt
		}
		pr.Amount = amt
		if p.PayeeColumn != "" {
			pr.Payee = raw[p.PayeeColumn]
		}
		if p.NotesColumn != "" {
			pr.Notes = raw[p.NotesColumn]
		}
		out = append(out, pr)
	}
	return out
}

// trailingDelimiter reports files whose data rows (unlike the header) end in a
// stray delimiter: most rows are one cell too long and that cell is empty.
func trailingDelimiter(rows [][]string, n int) bool {
	total, over := 0, 0
	for _, cols := range rows {
		if nonEmptyCount(cols) == 0 {
			continue
		}
		total++
		if len(cols) > n {
			if strings.TrimSpace(cols[len(cols)-1]) != "" {
				return false
			}
			over++
		}
	}
	return over > 0 && over*2 >= total
}

// fitColumns handles rows with more cells than headers: a stray trailing
// delimiter is dropped, otherwise (unquoted delimiter inside a description)
// the overflow is folded back into the payee column.
func fitColumns(cols []string, n, payeeIdx int, delim rune, trailing bool) []string {
	if trailing && len(cols) > n && strings.TrimSpace(cols[len(cols)-1]) == "" {
		cols = cols[:len(cols)-1]
	}
	extra := len(cols) - n
	if extra <= 0 || payeeIdx < 0 {
		return cols
	}
	sep := string(delim)
	if delim == 0 || delim == ',' {
		sep = ", "
	}
	parts := make([]string, 0, extra+1)
	for _, c := range cols[payeeIdx : payeeIdx+extra+1] {
		parts = append(parts, strings.TrimSpace(c))
	}
	out := make([]string, 0, n)
	out = append(out, cols[:payeeIdx]...)
	out = append(out, strings.Join(parts, sep))
	return append(out, cols[payeeIdx+extra+1:]...)
}

// cleanCell unwraps Excel text-forcing cells like ="7091".
func cleanCell(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 3 && strings.HasPrefix(s, `="`) && strings.HasSuffix(s, `"`) {
		return s[2 : len(s)-1]
	}
	return s
}

// MissingColumns lists mapped columns that don't exist in headers.
func MissingColumns(headers []string, p Profile) []string {
	have := map[string]bool{}
	for _, h := range headers {
		have[h] = true
	}
	var missing []string
	check := func(c string) {
		if c != "" && !have[c] {
			missing = append(missing, c)
		}
	}
	check(p.DateColumn)
	if p.AmountMode == "debit_credit" {
		check(p.DebitColumn)
		check(p.CreditColumn)
	} else {
		check(p.AmountColumn)
	}
	check(p.PayeeColumn)
	check(p.NotesColumn)
	return missing
}

func isBlankRow(raw map[string]string) bool {
	for _, v := range raw {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

func parseAmount(raw map[string]string, p Profile) (int64, error) {
	switch p.AmountMode {
	case "debit_credit":
		debitS := strings.TrimSpace(raw[p.DebitColumn])
		creditS := strings.TrimSpace(raw[p.CreditColumn])
		var debit, credit int64
		var err error
		if debitS != "" {
			debit, err = money.ParseDecimal(debitS)
			if err != nil {
				return 0, err
			}
			if debit < 0 {
				debit = -debit
			}
		}
		if creditS != "" {
			credit, err = money.ParseDecimal(creditS)
			if err != nil {
				return 0, err
			}
			if credit < 0 {
				credit = -credit
			}
		}
		if debit == 0 && credit == 0 {
			return 0, fmt.Errorf("empty debit/credit")
		}
		// Net-worth: debit (money out / charge) negative; credit (money in / payment) positive.
		return credit - debit, nil
	default:
		s := strings.TrimSpace(raw[p.AmountColumn])
		if s == "" {
			return 0, fmt.Errorf("empty amount")
		}
		return money.ParseDecimal(s)
	}
}

// ParseDate normalizes to YYYY-MM-DD.
func ParseDate(s, format string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("empty")
	}
	formats := dateFormats(format)
	cands := []string{s}
	if i := strings.IndexAny(s, " T"); i > 0 {
		cands = append(cands, s[:i]) // trailing time of day, e.g. "2026-10-12 00:00:00"
	}
	for _, c := range cands {
		for _, f := range formats {
			if t, err := time.Parse(f, c); err == nil {
				return t.Format("2006-01-02"), nil
			}
		}
	}
	return "", fmt.Errorf("unrecognized %q", s)
}

func dateFormats(format string) []string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "yyyy-mm-dd":
		return []string{"2006-01-02"}
	case "mm/dd/yyyy", "mdy_slash":
		return []string{"01/02/2006", "1/2/2006", "01/2/2006", "1/02/2006", "01/02/06", "1/2/06"}
	case "dd/mm/yyyy", "dmy_slash":
		return []string{"02/01/2006", "2/1/2006", "02/1/2006", "2/01/2006", "02/01/06", "2/1/06"}
	case "mm-dd-yyyy":
		return []string{"01-02-2006", "1-2-2006"}
	case "dd-mm-yyyy":
		return []string{"02-01-2006", "2-1-2006"}
	default: // auto
		return []string{
			"2006-01-02",
			"01/02/2006", "1/2/2006", "01/2/2006", "1/02/2006",
			"02/01/2006", "2/1/2006",
			"01-02-2006", "02-01-2006",
			"01/02/06", "1/2/06",
			"Jan 2, 2006", "January 2, 2006",
			"2 Jan 2006", "02 Jan 2006",
			"2006/01/02",
		}
	}
}

// Fingerprint is an advisory duplicate key (account + date + amount + normalized payee).
func Fingerprint(accountID int64, date string, amount int64, payee string) string {
	payee = strings.ToLower(strings.TrimSpace(payee))
	payee = strings.Join(strings.Fields(payee), " ")
	h := sha1.Sum([]byte(fmt.Sprintf("%d|%s|%d|%s", accountID, date, amount, payee)))
	return hex.EncodeToString(h[:])
}

// GuessColumns suggests mapping from header names.
func GuessColumns(headers []string) Profile {
	p := Profile{
		AmountMode: "signed",
		DateFormat: "auto",
		HasHeader:  true,
		Delimiter:  ',',
	}
	pickWhere := func(skip func(string) bool, cands ...string) string {
		for _, c := range cands {
			for _, h := range headers {
				if l := strings.ToLower(h); l == c && !skip(l) {
					return h
				}
			}
			for _, h := range headers {
				if l := strings.ToLower(h); strings.Contains(l, c) && !skip(l) {
					return h
				}
			}
		}
		return ""
	}
	pick := func(cands ...string) string {
		return pickWhere(func(string) bool { return false }, cands...)
	}
	// Card numbers, debit/credit indicator and balance columns aren't amounts.
	notMoney := func(l string) bool {
		for _, s := range []string{"card", "type", "indicator", "balance", "/"} {
			if strings.Contains(l, s) {
				return true
			}
		}
		return false
	}
	p.DateColumn = pick("transaction date", "trans. date", "trans date", "posting date", "posted date", "post date", "date", "posted")
	p.PayeeColumn = pick("payee", "description", "memo", "name", "merchant")
	p.NotesColumn = pick("notes", "memo", "reference", "check")
	if p.NotesColumn == p.PayeeColumn {
		p.NotesColumn = ""
	}
	debit := pickWhere(notMoney, "debit", "withdrawal", "outflow", "money out", "paid out")
	credit := pickWhere(notMoney, "credit", "deposit", "inflow", "money in", "paid in")
	amount := pickWhere(notMoney, "amount", "transaction amount", "sum", "value")
	if debit != "" && credit != "" && debit != credit {
		p.AmountMode = "debit_credit"
		p.DebitColumn = debit
		p.CreditColumn = credit
	} else {
		p.AmountColumn = amount
		if p.AmountColumn == "" && len(headers) > 0 {
			// last numeric-looking column fallback left to user
			p.AmountColumn = headers[len(headers)-1]
		}
	}
	if p.DateColumn == "" && len(headers) > 0 {
		p.DateColumn = headers[0]
	}
	if p.PayeeColumn == "" && len(headers) > 1 {
		p.PayeeColumn = headers[1]
	}
	return p
}

// DetectDelimiter picks the delimiter with the most occurrences on any of the
// first lines (preamble lines may contain none); ties and no-hits fall back to comma.
func DetectDelimiter(sample string) rune {
	lines := strings.SplitN(sample, "\n", 21)
	if len(lines) > 20 {
		lines = lines[:20]
	}
	best, bestN := ',', 0
	for _, d := range []string{",", ";", "\t", "|"} {
		n := 0
		for _, l := range lines {
			if c := strings.Count(l, d); c > n {
				n = c
			}
		}
		if n > bestN {
			best, bestN = []rune(d)[0], n
		}
	}
	return best
}

// AbsInt64 returns absolute value.
func AbsInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// DateWithin returns true if a and b are within ±days (inclusive).
func DateWithin(a, b string, days int) bool {
	ta, err1 := time.Parse("2006-01-02", a)
	tb, err2 := time.Parse("2006-01-02", b)
	if err1 != nil || err2 != nil {
		return false
	}
	d := ta.Sub(tb)
	if d < 0 {
		d = -d
	}
	return d <= time.Duration(days)*24*time.Hour
}

// ParseInt64 soft helper for form values.
func ParseInt64(s string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v
}
