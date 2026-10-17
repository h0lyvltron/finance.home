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
		headers = normalizeHeaders(all[0])
		rows = all[1:]
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

func normalizeHeaders(h []string) []string {
	out := make([]string, len(h))
	seen := map[string]int{}
	for i, s := range h {
		s = strings.TrimSpace(s)
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
	for i, cols := range rows {
		raw := map[string]string{}
		for j, h := range headers {
			if j < len(cols) {
				raw[h] = strings.TrimSpace(cols[j])
			} else {
				raw[h] = ""
			}
		}
		pr := ParsedRow{Index: i, Raw: raw}
		if isBlankRow(raw) {
			pr.Error = "blank row"
			out = append(out, pr)
			continue
		}
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
	// Excel serial? skip
	formats := dateFormats(format)
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t.Format("2006-01-02"), nil
		}
	}
	// Try common variants with single-digit month/day already covered by Go layouts.
	return "", fmt.Errorf("unrecognized %q", s)
}

func dateFormats(format string) []string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "yyyy-mm-dd":
		return []string{"2006-01-02"}
	case "mm/dd/yyyy", "mdy_slash":
		return []string{"01/02/2006", "1/2/2006", "01/2/2006", "1/02/2006"}
	case "dd/mm/yyyy", "dmy_slash":
		return []string{"02/01/2006", "2/1/2006", "02/1/2006", "2/01/2006"}
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
	lower := map[string]string{}
	for _, h := range headers {
		lower[strings.ToLower(h)] = h
	}
	pick := func(cands ...string) string {
		for _, c := range cands {
			if h, ok := lower[c]; ok {
				return h
			}
			for k, h := range lower {
				if strings.Contains(k, c) {
					return h
				}
			}
		}
		return ""
	}
	p.DateColumn = pick("date", "posted", "transaction date", "trans date", "posting date")
	p.PayeeColumn = pick("payee", "description", "memo", "name", "merchant")
	p.NotesColumn = pick("notes", "memo", "reference", "check")
	debit := pick("debit", "withdrawal", "outflow", "amount debit")
	credit := pick("credit", "deposit", "inflow", "amount credit")
	amount := pick("amount", "sum", "value", "transaction amount")
	if debit != "" && credit != "" {
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

// DetectDelimiter peeks at the first line.
func DetectDelimiter(sample string) rune {
	line := sample
	if i := strings.IndexByte(sample, '\n'); i >= 0 {
		line = sample[:i]
	}
	counts := map[rune]int{',': 0, ';': 0, '\t': 0, '|': 0}
	for _, r := range line {
		if _, ok := counts[r]; ok {
			counts[r]++
		}
	}
	best := ','
	bestN := -1
	for r, n := range counts {
		if n > bestN {
			best, bestN = r, n
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
