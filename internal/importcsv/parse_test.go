package importcsv

import (
	"strings"
	"testing"
)

func TestParseSignedCSV(t *testing.T) {
	raw := "Date,Description,Amount\n2024-01-15,Coffee,-4.50\n01/16/2024,Paycheck,2000.00\n"
	headers, rows, err := DetectHeaders(strings.NewReader(raw), ',', true)
	if err != nil {
		t.Fatal(err)
	}
	p := Profile{
		DateColumn:   "Date",
		DateFormat:   "auto",
		AmountMode:   "signed",
		AmountColumn: "Amount",
		PayeeColumn:  "Description",
		HasHeader:    true,
	}
	parsed := ParseRows(headers, rows, p)
	if len(parsed) != 2 {
		t.Fatalf("got %d rows", len(parsed))
	}
	if parsed[0].Amount != -450 || parsed[0].Payee != "Coffee" {
		t.Fatalf("row0 %+v", parsed[0])
	}
	if parsed[1].Amount != 200000 || parsed[1].Date != "2024-01-16" {
		t.Fatalf("row1 %+v", parsed[1])
	}
}

func TestDebitCredit(t *testing.T) {
	raw := "Date,Payee,Debit,Credit\n2024-03-01,Store,12.00,\n2024-03-02,Refund,,5.00\n"
	headers, rows, err := DetectHeaders(strings.NewReader(raw), ',', true)
	if err != nil {
		t.Fatal(err)
	}
	p := Profile{
		DateColumn:   "Date",
		AmountMode:   "debit_credit",
		DebitColumn:  "Debit",
		CreditColumn: "Credit",
		PayeeColumn:  "Payee",
		HasHeader:    true,
	}
	parsed := ParseRows(headers, rows, p)
	if parsed[0].Amount != -1200 {
		t.Fatalf("debit want -1200 got %d", parsed[0].Amount)
	}
	if parsed[1].Amount != 500 {
		t.Fatalf("credit want 500 got %d", parsed[1].Amount)
	}
}

func TestFingerprintStable(t *testing.T) {
	a := Fingerprint(1, "2024-01-01", -100, "  Foo   Bar ")
	b := Fingerprint(1, "2024-01-01", -100, "foo bar")
	if a != b {
		t.Fatalf("not stable: %s vs %s", a, b)
	}
}

func TestDateWithin(t *testing.T) {
	if !DateWithin("2024-01-10", "2024-01-15", 5) {
		t.Fatal("expected within")
	}
	if DateWithin("2024-01-01", "2024-01-15", 5) {
		t.Fatal("expected outside")
	}
}

func TestGuessColumns(t *testing.T) {
	g := GuessColumns([]string{"Posted Date", "Description", "Debit", "Credit"})
	if g.AmountMode != "debit_credit" {
		t.Fatalf("mode %s", g.AmountMode)
	}
	if g.PayeeColumn != "Description" {
		t.Fatalf("payee %s", g.PayeeColumn)
	}
}
