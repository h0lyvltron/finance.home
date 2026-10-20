package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"finance.home/internal/models"
)

// QueryFilter is the shared filter model for registers, export, and reports.
type QueryFilter struct {
	DatePreset  string // this_month|last_month|last_30|last_90|ytd|this_year|custom|rolling|all
	DateFrom    string // YYYY-MM-DD inclusive (resolved)
	DateTo      string // YYYY-MM-DD inclusive (resolved)
	RollingDays int

	AccountIDs  []int64
	CategoryIDs []int64 // caller may pass parents; ExpandCategories before query
	TagIDs      []int64 // any-of
	People      []string
	Payee       string
	AmountMin   *int64
	AmountMax   *int64
	Statuses    []string
	NeedsReview *bool
	Text        string
	// Uncategorized keeps non-transfer transactions with no category or splits,
	// whether or not they are still in the review queue.
	Uncategorized bool

	ExcludeTransfers bool
	ExcludeStarting  bool

	Limit      int
	BeforeDate string
	BeforeID   int64

	// Convenience: single account (register / legacy)
	AccountID int64
}

// RegisterFilter is kept as an alias shape for older call sites.
type RegisterFilter = QueryFilter

func (f QueryFilter) Clone() QueryFilter {
	out := f
	out.AccountIDs = append([]int64(nil), f.AccountIDs...)
	out.CategoryIDs = append([]int64(nil), f.CategoryIDs...)
	out.TagIDs = append([]int64(nil), f.TagIDs...)
	out.People = append([]string(nil), f.People...)
	out.Statuses = append([]string(nil), f.Statuses...)
	if f.AmountMin != nil {
		v := *f.AmountMin
		out.AmountMin = &v
	}
	if f.AmountMax != nil {
		v := *f.AmountMax
		out.AmountMax = &v
	}
	if f.NeedsReview != nil {
		v := *f.NeedsReview
		out.NeedsReview = &v
	}
	return out
}

func (f *QueryFilter) NormalizeAccounts() {
	if f.AccountID > 0 && len(f.AccountIDs) == 0 {
		f.AccountIDs = []int64{f.AccountID}
	}
	if len(f.AccountIDs) == 1 {
		f.AccountID = f.AccountIDs[0]
	}
}

// ResolveDates fills DateFrom/DateTo from preset using household timezone + week start.
func (f *QueryFilter) ResolveDates(h *models.Household, now time.Time) {
	if h != nil && h.Timezone != "" {
		if loc, err := time.LoadLocation(h.Timezone); err == nil {
			now = now.In(loc)
		}
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	preset := f.DatePreset
	if preset == "" {
		if f.DateFrom != "" || f.DateTo != "" {
			preset = "custom"
		} else if f.RollingDays > 0 {
			preset = "rolling"
		} else {
			preset = "this_month"
		}
		f.DatePreset = preset
	}

	switch preset {
	case "all":
		f.DateFrom, f.DateTo = "", ""
	case "custom":
		// keep DateFrom/DateTo as provided
	case "rolling":
		days := f.RollingDays
		if days <= 0 {
			days = 30
			f.RollingDays = days
		}
		f.DateTo = today.Format("2006-01-02")
		f.DateFrom = today.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	case "last_30":
		f.RollingDays = 30
		f.DateTo = today.Format("2006-01-02")
		f.DateFrom = today.AddDate(0, 0, -29).Format("2006-01-02")
	case "last_90":
		f.RollingDays = 90
		f.DateTo = today.Format("2006-01-02")
		f.DateFrom = today.AddDate(0, 0, -89).Format("2006-01-02")
	case "last_month":
		ym := YearMonth(h, today)
		t, err := time.Parse("2006-01", ym)
		if err != nil {
			t = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
		}
		prevYM := t.AddDate(0, -1, 0).Format("2006-01")
		from, to, err := FiscalMonthBounds(h, prevYM)
		if err != nil {
			firstThis := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
			lastPrev := firstThis.AddDate(0, 0, -1)
			firstPrev := time.Date(lastPrev.Year(), lastPrev.Month(), 1, 0, 0, 0, 0, today.Location())
			f.DateFrom = firstPrev.Format("2006-01-02")
			f.DateTo = lastPrev.Format("2006-01-02")
		} else {
			f.DateFrom, f.DateTo = from, to
		}
	case "ytd", "this_year":
		f.DateFrom = time.Date(today.Year(), 1, 1, 0, 0, 0, 0, today.Location()).Format("2006-01-02")
		f.DateTo = today.Format("2006-01-02")
	case "last_12", "last_12mo":
		f.DatePreset = "last_12"
		f.DateTo = today.Format("2006-01-02")
		f.DateFrom = today.AddDate(-1, 0, 1).Format("2006-01-02")
	case "this_month", "":
		f.DatePreset = "this_month"
		ym := YearMonth(h, today)
		from, to, err := FiscalMonthBounds(h, ym)
		if err != nil {
			first := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
			f.DateFrom = first.Format("2006-01-02")
			f.DateTo = today.Format("2006-01-02")
		} else {
			f.DateFrom = from
			todayStr := today.Format("2006-01-02")
			if to > todayStr {
				f.DateTo = todayStr
			} else {
				f.DateTo = to
			}
		}
	default:
		ym := YearMonth(h, today)
		from, to, err := FiscalMonthBounds(h, ym)
		if err != nil {
			first := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
			f.DateFrom = first.Format("2006-01-02")
			f.DateTo = today.Format("2006-01-02")
		} else {
			f.DateFrom = from
			todayStr := today.Format("2006-01-02")
			if to > todayStr {
				f.DateTo = todayStr
			} else {
				f.DateTo = to
			}
		}
		f.DatePreset = "this_month"
	}
}

// ShiftFilterLastYear returns a clone with DateFrom/DateTo moved back one calendar year.
func ShiftFilterLastYear(f QueryFilter) QueryFilter {
	out := f.Clone()
	out.DatePreset = "custom"
	out.DateFrom = shiftDateYear(f.DateFrom, -1)
	out.DateTo = shiftDateYear(f.DateTo, -1)
	return out
}

func shiftDateYear(iso string, years int) string {
	if iso == "" {
		return ""
	}
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return t.AddDate(years, 0, 0).Format("2006-01-02")
}

// EncodeQuery writes filter fields into url.Values (for URL state / links).
func (f QueryFilter) EncodeQuery() url.Values {
	v := url.Values{}
	if f.DatePreset != "" {
		v.Set("preset", f.DatePreset)
	}
	if f.DatePreset == "custom" {
		if f.DateFrom != "" {
			v.Set("from", f.DateFrom)
		}
		if f.DateTo != "" {
			v.Set("to", f.DateTo)
		}
	}
	if f.DatePreset == "rolling" && f.RollingDays > 0 {
		v.Set("rolling", strconv.Itoa(f.RollingDays))
	}
	for _, id := range f.AccountIDs {
		v.Add("account", strconv.FormatInt(id, 10))
	}
	for _, id := range f.CategoryIDs {
		v.Add("category", strconv.FormatInt(id, 10))
	}
	for _, id := range f.TagIDs {
		v.Add("tag", strconv.FormatInt(id, 10))
	}
	for _, p := range f.People {
		v.Add("person", p)
	}
	if f.Payee != "" {
		v.Set("payee", f.Payee)
	}
	if f.AmountMin != nil {
		v.Set("amin", formatCentsParam(*f.AmountMin))
	}
	if f.AmountMax != nil {
		v.Set("amax", formatCentsParam(*f.AmountMax))
	}
	for _, st := range f.Statuses {
		v.Add("status", st)
	}
	if f.NeedsReview != nil {
		if *f.NeedsReview {
			v.Set("review", "1")
		} else {
			v.Set("review", "0")
		}
	}
	if f.Text != "" {
		v.Set("q", f.Text)
	}
	if f.Uncategorized {
		v.Set("uncat", "1")
	}
	return v
}

func formatCentsParam(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	s := fmt.Sprintf("%d.%02d", cents/100, cents%100)
	if neg {
		return "-" + s
	}
	return s
}

// ParseQueryFilter reads shared filter fields from a request query/form.
func ParseQueryFilter(v url.Values) QueryFilter {
	f := QueryFilter{
		DatePreset:  strings.TrimSpace(v.Get("preset")),
		DateFrom:    strings.TrimSpace(v.Get("from")),
		DateTo:      strings.TrimSpace(v.Get("to")),
		Payee:       strings.TrimSpace(v.Get("payee")),
		Text:        strings.TrimSpace(v.Get("q")),
		People:      compactStrings(v["person"]),
		Statuses:    compactStrings(v["status"]),
		AccountIDs:  parseInt64List(v["account"]),
		CategoryIDs: parseInt64List(v["category"]),
		TagIDs:      parseInt64List(v["tag"]),
	}
	if r := strings.TrimSpace(v.Get("rolling")); r != "" {
		if n, err := strconv.Atoi(r); err == nil && n > 0 {
			f.RollingDays = n
			if f.DatePreset == "" {
				f.DatePreset = "rolling"
			}
		}
	}
	if amin := strings.TrimSpace(v.Get("amin")); amin != "" {
		if c, err := parseMoneyParam(amin); err == nil {
			f.AmountMin = &c
		}
	}
	if amax := strings.TrimSpace(v.Get("amax")); amax != "" {
		if c, err := parseMoneyParam(amax); err == nil {
			f.AmountMax = &c
		}
	}
	switch strings.TrimSpace(v.Get("review")) {
	case "1", "true", "yes":
		t := true
		f.NeedsReview = &t
	case "0", "false", "no":
		t := false
		f.NeedsReview = &t
	}
	switch strings.TrimSpace(v.Get("uncat")) {
	case "1", "true", "yes":
		f.Uncategorized = true
	}
	if aid := strings.TrimSpace(v.Get("account_id")); aid != "" && len(f.AccountIDs) == 0 {
		if id, err := strconv.ParseInt(aid, 10, 64); err == nil && id > 0 {
			f.AccountID = id
			f.AccountIDs = []int64{id}
		}
	}
	f.NormalizeAccounts()
	return f
}

func parseMoneyParam(s string) (int64, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.TrimPrefix(s, "$")
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	parts := strings.SplitN(s, ".", 2)
	maj, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, err
	}
	var min int64
	if len(parts) == 2 {
		frac := parts[1]
		if len(frac) == 1 {
			frac += "0"
		}
		if len(frac) > 2 {
			frac = frac[:2]
		}
		min, err = strconv.ParseInt(frac, 10, 64)
		if err != nil {
			return 0, err
		}
	}
	c := maj*100 + min
	if neg {
		c = -c
	}
	return c, nil
}

func parseInt64List(vals []string) []int64 {
	var out []int64
	seen := map[int64]bool{}
	for _, raw := range vals {
		for _, p := range strings.Split(raw, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			id, err := strconv.ParseInt(p, 10, 64)
			if err != nil || id <= 0 || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func compactStrings(vals []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range vals {
		for _, p := range strings.Split(raw, ",") {
			p = strings.TrimSpace(strings.ToLower(p))
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// ExpandCategories adds all descendant category IDs for selected parents.
func (s *Store) ExpandCategories(ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	cats, err := s.ListCategories("", true)
	if err != nil {
		return nil, err
	}
	children := map[int64][]int64{}
	for _, c := range cats {
		if c.ParentID != nil {
			children[*c.ParentID] = append(children[*c.ParentID], c.ID)
		}
	}
	seen := map[int64]bool{}
	var out []int64
	var walk func(int64)
	walk = func(id int64) {
		if seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
		for _, ch := range children[id] {
			walk(ch)
		}
	}
	for _, id := range ids {
		walk(id)
	}
	return out, nil
}

type sqlFrag struct {
	where string
	args  []any
	joins string
}

// buildTxFilter builds WHERE/JOIN fragments against alias t (transactions).
// When useSplits is true, category/person constraints use EXISTS on transaction_splits.
func (s *Store) buildTxFilter(f QueryFilter, useSplits bool) (sqlFrag, error) {
	f.NormalizeAccounts()
	var b strings.Builder
	args := []any{}
	joins := ""

	b.WriteString(` t.deleted_at IS NULL`)

	if f.DateFrom != "" {
		b.WriteString(` AND t.transaction_date >= ?`)
		args = append(args, f.DateFrom)
	}
	if f.DateTo != "" {
		b.WriteString(` AND t.transaction_date <= ?`)
		args = append(args, f.DateTo)
	}
	if len(f.AccountIDs) > 0 {
		b.WriteString(` AND t.account_id IN (` + placeholders(len(f.AccountIDs)) + `)`)
		for _, id := range f.AccountIDs {
			args = append(args, id)
		}
	}
	if f.Payee != "" {
		b.WriteString(` AND COALESCE(t.payee_name,'') LIKE ? COLLATE NOCASE`)
		args = append(args, "%"+f.Payee+"%")
	}
	if f.AmountMin != nil {
		b.WriteString(` AND t.amount >= ?`)
		args = append(args, *f.AmountMin)
	}
	if f.AmountMax != nil {
		b.WriteString(` AND t.amount <= ?`)
		args = append(args, *f.AmountMax)
	}
	if len(f.Statuses) > 0 {
		b.WriteString(` AND t.status IN (` + placeholders(len(f.Statuses)) + `)`)
		for _, st := range f.Statuses {
			args = append(args, st)
		}
	}
	if f.NeedsReview != nil {
		if *f.NeedsReview {
			b.WriteString(` AND t.needs_review = 1`)
		} else {
			b.WriteString(` AND t.needs_review = 0`)
		}
	}
	if f.Uncategorized {
		b.WriteString(` AND t.category_id IS NULL AND t.transfer_id IS NULL
			AND NOT EXISTS (SELECT 1 FROM transaction_splits ux WHERE ux.transaction_id = t.id)`)
	}
	if f.Text != "" {
		b.WriteString(` AND (COALESCE(t.payee_name,'') LIKE ? COLLATE NOCASE OR COALESCE(t.notes,'') LIKE ? COLLATE NOCASE)`)
		like := "%" + f.Text + "%"
		args = append(args, like, like)
	}
	if len(f.TagIDs) > 0 {
		b.WriteString(` AND EXISTS (SELECT 1 FROM transaction_tags tt WHERE tt.transaction_id = t.id AND tt.tag_id IN (` +
			placeholders(len(f.TagIDs)) + `))`)
		for _, id := range f.TagIDs {
			args = append(args, id)
		}
	}

	catIDs := f.CategoryIDs
	if len(catIDs) > 0 {
		expanded, err := s.ExpandCategories(catIDs)
		if err != nil {
			return sqlFrag{}, err
		}
		catIDs = expanded
	}

	if useSplits {
		if len(catIDs) > 0 || len(f.People) > 0 {
			// Match on splits, or on the transaction itself when it has none
			// (uncategorized imports).
			var cond string
			var condArgs []any
			if len(catIDs) > 0 {
				cond += ` AND x.category_id IN (` + placeholders(len(catIDs)) + `)`
				for _, id := range catIDs {
					condArgs = append(condArgs, id)
				}
			}
			if len(f.People) > 0 {
				cond += ` AND x.person IN (` + placeholders(len(f.People)) + `)`
				for _, p := range f.People {
					condArgs = append(condArgs, p)
				}
			}
			b.WriteString(` AND (EXISTS (SELECT 1 FROM transaction_splits x WHERE x.transaction_id = t.id` + cond + `)
				OR (NOT EXISTS (SELECT 1 FROM transaction_splits x0 WHERE x0.transaction_id = t.id)` +
				strings.ReplaceAll(cond, "x.", "t.") + `))`)
			args = append(args, condArgs...)
			args = append(args, condArgs...)
		}
	} else {
		if len(catIDs) > 0 {
			b.WriteString(` AND t.category_id IN (` + placeholders(len(catIDs)) + `)`)
			for _, id := range catIDs {
				args = append(args, id)
			}
		}
		if len(f.People) > 0 {
			b.WriteString(` AND t.person IN (` + placeholders(len(f.People)) + `)`)
			for _, p := range f.People {
				args = append(args, p)
			}
		}
	}

	if f.ExcludeTransfers || f.ExcludeStarting {
		joins += ` LEFT JOIN categories xc ON xc.id = t.category_id`
		if f.ExcludeTransfers {
			b.WriteString(` AND COALESCE(xc.kind,'') != 'transfer' AND t.transfer_id IS NULL`)
		}
		if f.ExcludeStarting {
			b.WriteString(` AND COALESCE(xc.kind,'') != 'starting_balance'`)
		}
	}

	return sqlFrag{where: b.String(), args: args, joins: joins}, nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

func (s *Store) ListTransactions(f QueryFilter) ([]models.Transaction, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	frag, err := s.buildTxFilter(f, true)
	if err != nil {
		return nil, err
	}
	q := `SELECT t.id, t.account_id, a.name, t.payee_id, COALESCE(t.payee_name,''), COALESCE(t.notes,''),
		t.amount, t.transaction_date, t.person, t.status, t.needs_review, t.category_id,
		COALESCE(c.name,''), t.is_split, t.transfer_id
		FROM transactions t
		JOIN accounts a ON a.id = t.account_id
		LEFT JOIN categories c ON c.id = t.category_id` + frag.joins + `
		WHERE` + frag.where
	args := append([]any{}, frag.args...)
	if f.BeforeDate != "" && f.BeforeID > 0 {
		q += ` AND (t.transaction_date < ? OR (t.transaction_date = ? AND t.id < ?))`
		args = append(args, f.BeforeDate, f.BeforeDate, f.BeforeID)
	}
	q += ` ORDER BY t.transaction_date DESC, t.id DESC LIMIT ?`
	args = append(args, f.Limit)

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Transaction
	for rows.Next() {
		var t models.Transaction
		var payeeID, catID, xferID sql.NullInt64
		var needs, split int
		if err := rows.Scan(&t.ID, &t.AccountID, &t.AccountName, &payeeID, &t.PayeeName, &t.Notes,
			&t.Amount, &t.TransactionDate, &t.Person, &t.Status, &needs, &catID, &t.CategoryName, &split, &xferID); err != nil {
			return nil, err
		}
		if payeeID.Valid {
			v := payeeID.Int64
			t.PayeeID = &v
		}
		if catID.Valid {
			v := catID.Int64
			t.CategoryID = &v
		}
		if xferID.Valid {
			v := xferID.Int64
			t.TransferID = &v
		}
		t.NeedsReview = needs == 1
		t.IsSplit = split == 1
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range out {
		tags, err := s.txTags(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Tags = tags
		if out[i].IsSplit {
			splits, err := s.txSplits(out[i].ID)
			if err != nil {
				return nil, err
			}
			out[i].Splits = splits
		}
	}
	return out, nil
}

// FilterChip is a display label for an active filter dimension.
type FilterChip struct {
	Key   string
	Label string
	RemoveQuery string // relative query string without this chip
}
