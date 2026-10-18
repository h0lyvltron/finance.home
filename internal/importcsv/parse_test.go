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

func TestPreambleSkipped(t *testing.T) {
	cases := map[string]string{
		"barclays": "Barclays Bank Delaware\n" +
			"Account Number: XXXXXXXXXXXX7091\n" +
			"Account Balance as of October 17 2026:    $2234.72\n" +
			" \n" +
			"Transaction Date,Description,Category,Amount,Card Last 4 Digits,Purchased By\n" +
			"10/15/2026,\"AMAZON MKTPL, SEATTLE WA\",\"DEBIT\",\"-23.45\",=\"7091\",JANE\n" +
			"10/16/2026,\"Payment Received\",\"CREDIT\",\"500.00\",=\"7091\",\n" +
			" \n",
		"excel resave": "Barclays Bank Delaware,,,,,\n" +
			"Account Number: XXXXXXXXXXXX7091,,,,,\n" +
			"Account Balance as of October 17 2026:,$2234.72,,,,\n" +
			",,,,,\n" +
			"Transaction Date,Description,Category,Amount,Card Last 4 Digits,Purchased By\n" +
			"10/15/2026,\"AMAZON MKTPL, SEATTLE WA\",DEBIT,-23.45,7091,JANE\n" +
			"10/16/2026,Payment Received,CREDIT,500.00,7091,\n",
	}
	for name, raw := range cases {
		delim := DetectDelimiter(raw)
		if delim != ',' {
			t.Fatalf("%s: delimiter %q", name, delim)
		}
		headers, rows, err := DetectHeaders(strings.NewReader(raw), delim, true)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if headers[0] != "Transaction Date" {
			t.Fatalf("%s: headers %q", name, headers)
		}
		g := GuessColumns(headers)
		if g.DateColumn != "Transaction Date" || g.PayeeColumn != "Description" || g.AmountColumn != "Amount" {
			t.Fatalf("%s: guess %+v", name, g)
		}
		parsed := ParseRows(headers, rows, g)
		if len(parsed) != 2 {
			t.Fatalf("%s: got %d rows", name, len(parsed))
		}
		if parsed[0].Raw["Card Last 4 Digits"] != "7091" {
			t.Fatalf("%s: card cell %q", name, parsed[0].Raw["Card Last 4 Digits"])
		}
		if parsed[0].Error != "" || parsed[0].Amount != -2345 || parsed[0].Payee != "AMAZON MKTPL, SEATTLE WA" {
			t.Fatalf("%s: row0 %+v", name, parsed[0])
		}
		if parsed[1].Error != "" || parsed[1].Amount != 50000 || parsed[1].Date != "2026-10-16" {
			t.Fatalf("%s: row1 %+v", name, parsed[1])
		}
	}
}

func TestMissingColumns(t *testing.T) {
	headers := []string{"Transaction Date", "Description", "Amount"}
	stale := Profile{DateColumn: "Barclays Bank Delaware", AmountMode: "signed", AmountColumn: "Barclays Bank Delaware"}
	if m := MissingColumns(headers, stale); len(m) != 2 {
		t.Fatalf("want 2 missing, got %q", m)
	}
	ok := Profile{DateColumn: "Transaction Date", AmountMode: "signed", AmountColumn: "Amount", PayeeColumn: "Description"}
	if m := MissingColumns(headers, ok); len(m) != 0 {
		t.Fatalf("want none missing, got %q", m)
	}
}

func TestDetectDelimiterSemicolon(t *testing.T) {
	if d := DetectDelimiter("Some Bank\nDate;Payee;Amount\n2026-01-01;X;-1,00\n"); d != ';' {
		t.Fatalf("got %q", d)
	}
}

const capitalOneCSV = "Transaction Date,Posted Date,Card No.,Description,Category,Debit,Credit\n" +
	"2026-10-12,2026-10-13,1234,AMAZON MKTPL*5151O6EQ0,Merchandise,25.99,\n" +
	"2026-10-10,2026-10-11,1234,CAPITAL ONE MOBILE PYMT,Payment/Credit,,150.00\n" +
	"10/09/2026,10/10/2026,1234,BEST BUY 00012345,Merchandise,\"$1,234.56\",\n" +
	"10/08/2026,10/09/2026,1234,AMAZON MKTPL*REFUND,Merchandise,,$12.34\n" +
	"10/7/26,10/8/26,1234,SQ *BLUE BOTTLE, SEATTLE WA,Dining,4.50,\n"

func TestCapitalOneExport(t *testing.T) {
	delim := DetectDelimiter(capitalOneCSV)
	headers, rows, err := DetectHeaders(strings.NewReader(capitalOneCSV), delim, true)
	if err != nil {
		t.Fatal(err)
	}
	g := GuessColumns(headers)
	if g.DateColumn != "Transaction Date" || g.PayeeColumn != "Description" || g.NotesColumn != "" ||
		g.AmountMode != "debit_credit" || g.DebitColumn != "Debit" || g.CreditColumn != "Credit" {
		t.Fatalf("guess %+v", g)
	}
	parsed := ParseRows(headers, rows, g)
	want := []struct {
		date   string
		amount int64
		payee  string
	}{
		{"2026-10-12", -2599, "AMAZON MKTPL*5151O6EQ0"},
		{"2026-10-10", 15000, "CAPITAL ONE MOBILE PYMT"},
		{"2026-10-09", -123456, "BEST BUY 00012345"},
		{"2026-10-08", 1234, "AMAZON MKTPL*REFUND"},
		{"2026-10-07", -450, "SQ *BLUE BOTTLE, SEATTLE WA"},
	}
	if len(parsed) != len(want) {
		t.Fatalf("got %d rows", len(parsed))
	}
	for i, w := range want {
		pr := parsed[i]
		if pr.Error != "" || pr.Date != w.date || pr.Amount != w.amount || pr.Payee != w.payee || pr.Notes != "" {
			t.Fatalf("row%d %+v", i, pr)
		}
	}
	if parsed[4].Raw["Category"] != "Dining" || parsed[4].Raw["Debit"] != "4.50" {
		t.Fatalf("overflow not folded into payee: %+v", parsed[4].Raw)
	}
}

func TestTrailingDelimiterRows(t *testing.T) {
	raw := "Date,Description,Amount\n2026-10-01,Coffee,-4.50,\n2026-10-02,\"Lunch, Deli\",-12.00,\n"
	headers, rows, err := DetectHeaders(strings.NewReader(raw), ',', true)
	if err != nil {
		t.Fatal(err)
	}
	parsed := ParseRows(headers, rows, GuessColumns(headers))
	if len(parsed) != 2 || parsed[0].Error != "" || parsed[0].Payee != "Coffee" || parsed[0].Amount != -450 ||
		parsed[1].Payee != "Lunch, Deli" || parsed[1].Amount != -1200 {
		t.Fatalf("parsed %+v", parsed)
	}
}

func TestGuessColumnsDebitCreditNames(t *testing.T) {
	cases := []struct {
		headers       []string
		date          string
		debit, credit string
	}{
		{[]string{"Posted Date", "Transaction Date", "Card No.", "Description", "Debit", "Credit"}, "Transaction Date", "Debit", "Credit"},
		{[]string{"Date", "Description", "Debit Amount", "Credit Amount", "Balance"}, "Date", "Debit Amount", "Credit Amount"},
		{[]string{"Posting Date", "Payee", "Withdrawals", "Deposits", "Balance"}, "Posting Date", "Withdrawals", "Deposits"},
		{[]string{"Trans. Date", "Post Date", "Description", "Money Out", "Money In"}, "Trans. Date", "Money Out", "Money In"},
		{[]string{"Status", "Date", "Description", "Debit", "Credit"}, "Date", "Debit", "Credit"},
	}
	for _, c := range cases {
		g := GuessColumns(c.headers)
		if g.DateColumn != c.date || g.AmountMode != "debit_credit" || g.DebitColumn != c.debit || g.CreditColumn != c.credit {
			t.Fatalf("%q: guess %+v", c.headers, g)
		}
	}
	signed := [][]string{
		{"Date", "Description", "Amount", "Debit/Credit"},
		{"Date", "Description", "Amount", "Credit Card No."},
		{"Date", "Description", "Transaction Type", "Amount"},
	}
	for _, h := range signed {
		g := GuessColumns(h)
		if g.AmountMode != "signed" || g.AmountColumn != "Amount" {
			t.Fatalf("%q: guess %+v", h, g)
		}
	}
}

func TestParseDateVariants(t *testing.T) {
	cases := map[string]string{
		"2026-10-12":          "2026-10-12",
		"10/12/2026":          "2026-10-12",
		"1/2/2026":            "2026-01-02",
		"10/12/26":            "2026-10-12",
		"2026-10-12 00:00:00": "2026-10-12",
		"2026-10-12T08:30:00": "2026-10-12",
		"Oct 12, 2026":        "2026-10-12",
	}
	for in, want := range cases {
		got, err := ParseDate(in, "auto")
		if err != nil || got != want {
			t.Fatalf("%q: got %q err %v", in, got, err)
		}
	}
}

func TestGuessColumnsBankExport(t *testing.T) {
	raw := "\ufeffTransaction ID,Posting Date,Effective Date,Transaction Type,Posting Status,Amount,Check Number,Reference Number,Description,Transaction Category,Type,Balance,Memo,Extended Description\n" +
		"123,10/01/2026,09/30/2026,Debit,Posted,-$42.17,,REF1,GROCERY MART,Groceries,POS,1000.00,,GROCERY MART #12 SEATTLE WA\n"
	headers, rows, err := DetectHeaders(strings.NewReader(raw), ',', true)
	if err != nil {
		t.Fatal(err)
	}
	if headers[0] != "Transaction ID" {
		t.Fatalf("BOM not stripped: %q", headers[0])
	}
	for i := 0; i < 50; i++ {
		g := GuessColumns(headers)
		if g.DateColumn != "Posting Date" || g.PayeeColumn != "Description" ||
			g.AmountMode != "signed" || g.AmountColumn != "Amount" || g.NotesColumn != "Memo" {
			t.Fatalf("guess %+v", g)
		}
	}
	parsed := ParseRows(headers, rows, GuessColumns(headers))
	if parsed[0].Error != "" || parsed[0].Date != "2026-10-01" || parsed[0].Amount != -4217 {
		t.Fatalf("row0 %+v", parsed[0])
	}
}

func TestHeaderlessCreditUnion(t *testing.T) {
	auto := "10/03/2026,\"Finance Charge/Fee: $99.24, Loan Payment\",,637.76\n" +
		"09/03/2026,\"Finance Charge/Fee: $10.00, Loan Payment\",,100.00\n"
	headers, rows, hasHeader, err := Read(strings.NewReader(auto), ',')
	if err != nil {
		t.Fatal(err)
	}
	if hasHeader {
		t.Fatal("loan export should have no header")
	}
	if len(rows) != 2 {
		t.Fatalf("rows %d", len(rows))
	}
	if headers[0] != "Date" || headers[1] != "Description" || headers[3] != "Amount" {
		t.Fatalf("headers %q", headers)
	}
	g := GuessColumns(headers)
	g.HasHeader = false
	parsed := ParseRows(headers, rows, g)
	if len(parsed) != 2 || parsed[0].Error != "" {
		t.Fatalf("parsed %+v", parsed)
	}
	if parsed[0].Date != "2026-10-03" || parsed[0].Amount != 63776 || !strings.Contains(parsed[0].Payee, "Loan Payment") {
		t.Fatalf("first row dropped or misread: %+v", parsed[0])
	}

	checking := "10/16/2026,WITHDRAWAL GROCERY,,-12.34\n" +
		"09/29/2026,CHECK # 1386 ,1386,-300.00\n" +
		"09/30/2026,Deposit PAYROLL,,2423.27\n"
	headers, rows, hasHeader, err = Read(strings.NewReader(checking), ',')
	if err != nil || hasHeader {
		t.Fatalf("has=%v err=%v", hasHeader, err)
	}
	if headers[2] != "Check" {
		t.Fatalf("headers %q", headers)
	}
	g = GuessColumns(headers)
	g.HasHeader = false
	parsed = ParseRows(headers, rows, g)
	if len(parsed) != 3 {
		t.Fatalf("len %d", len(parsed))
	}
	if parsed[0].Amount != -1234 || parsed[0].Payee != "WITHDRAWAL GROCERY" {
		t.Fatalf("spend %+v", parsed[0])
	}
	if parsed[1].Amount != -30000 || parsed[1].Notes != "1386" {
		t.Fatalf("check %+v", parsed[1])
	}
	if parsed[2].Amount != 242327 {
		t.Fatalf("deposit %+v", parsed[2])
	}

	headed := "Transaction Date,Posted Date,Card No.,Description,Category,Debit,Credit\n" +
		"2026-10-11,2026-10-13,6897,STARLINK,Phone,140.00,\n"
	headers, _, hasHeader, err = Read(strings.NewReader(headed), ',')
	if err != nil || !hasHeader || headers[0] != "Transaction Date" || headers[5] != "Debit" {
		t.Fatalf("headed sniff headers=%q has=%v err=%v", headers, hasHeader, err)
	}
}
