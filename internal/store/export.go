package store

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"finance.home/internal/money"
)

// ExportTransactionsCSV writes the current register filter set as CSV.
func (s *Store) ExportTransactionsCSV(w io.Writer, f RegisterFilter) error {
	if f.Limit <= 0 {
		f.Limit = 10000
	}
	txs, err := s.ListTransactions(f)
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"date", "account", "payee", "category", "person", "status", "amount", "notes", "needs_review", "tags"})
	for _, t := range txs {
		cat := t.CategoryName
		if t.IsSplit {
			cat = "Split"
		}
		var tags []string
		for _, tg := range t.Tags {
			tags = append(tags, tg.Name)
		}
		needs := "0"
		if t.NeedsReview {
			needs = "1"
		}
		_ = cw.Write([]string{
			t.TransactionDate,
			t.AccountName,
			t.PayeeName,
			cat,
			t.Person,
			t.Status,
			money.Format(t.Amount, ""),
			t.Notes,
			needs,
			strings.Join(tags, ";"),
		})
	}
	cw.Flush()
	return cw.Error()
}

func (s *Store) ExportFilterSummary(f RegisterFilter) string {
	if f.AccountID > 0 {
		return fmt.Sprintf("account_%d", f.AccountID)
	}
	return "all"
}
