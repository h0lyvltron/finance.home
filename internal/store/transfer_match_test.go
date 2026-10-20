package store

import "testing"

func testMatcher() *transferMatcher {
	m := &transferMatcher{accounts: map[int64]*matchAccount{}, categories: map[int64]matchCategory{}}
	add := func(id int64, name, typ, owner, aliases string) {
		m.accounts[id] = &matchAccount{ID: id, Name: name, Type: typ, Owner: owner, tokens: accountTokens(name, aliases)}
	}
	add(1, "STCU", "checking", "joint", "")
	add(3, "Capital One (Mom)", "credit", "a", "")
	add(5, "Global CU (CHK)", "checking", "b", "")
	add(9, "Apple (Goldman Sachs)", "credit", "b", "")
	add(14, "Capital One (Dad)", "credit", "b", "")
	add(15, "Venmo", "checking", "a", "")
	add(2, "Old Navy Encore Credit Card", "credit", "joint", "BARCLAYCARD")
	m.categories[39] = matchCategory{kind: "transfer"}
	m.categories[29] = matchCategory{kind: "expense", debt: true}
	m.categories[7] = matchCategory{kind: "expense"}
	return m
}

func cat(id int64) *int64 { return &id }

func TestScoreFiftyDollarCollision(t *testing.T) {
	m := testMatcher()
	gcu := transferSide{TxID: 923, AccountID: 5, Date: "2026-10-05", Amount: -5000,
		Payee: "Withdrawal ACH CAPITAL ONE TYPE: CRCARDPMT CO: CAPITAL ONE NAME: Timot", CategoryID: cat(39)}
	dad := transferSide{TxID: 1261, AccountID: 14, Date: "2026-10-04", Amount: 5000,
		Payee: "CAPITAL ONE AUTOPAY PYMT", CategoryID: cat(29)}
	deposit := transferSide{TxID: 9, AccountID: 1, Date: "2026-10-09", Amount: 5000,
		Payee: "Remote Deposit: 392523470", CategoryID: cat(39)}

	good, ok := m.score(gcu, dad)
	if !ok || good < TransferAutoScore {
		t.Fatalf("card autopay score %d ok=%v", good, ok)
	}
	bad, ok := m.score(gcu, deposit)
	if !ok || bad >= TransferPossibleScore {
		t.Fatalf("unrelated deposit score %d should be below %d", bad, TransferPossibleScore)
	}
	picked := pickPairs([]pairCandidate{
		{Left: gcu.TxID, Right: deposit.TxID, Score: bad, Days: 4},
		{Left: gcu.TxID, Right: dad.TxID, Score: good, Days: 1},
	}, TransferAutoScore, false)
	if len(picked) != 1 || picked[0].Right != dad.TxID || picked[0].Ambiguous {
		t.Fatalf("picked %+v", picked)
	}
}

func TestScoreNamedAccountWins(t *testing.T) {
	m := testMatcher()
	gcu := transferSide{AccountID: 5, Date: "2026-04-01", Amount: -106729,
		Payee: "Withdrawal ACH APPLECARD GSBANK TYPE: PAYMENT CO: APPLECARD GSBANK", CategoryID: cat(29)}
	apple := transferSide{AccountID: 9, Date: "2026-03-31", Amount: 106729,
		Payee: "ACH DEPOSIT INTERNET TRANSFER FROM ACCOUNT ENDING IN 3212", CategoryID: cat(39)}
	s, ok := m.score(gcu, apple)
	if !ok || s < TransferAutoScore+40 {
		t.Fatalf("apple payment score %d", s)
	}

	stcu := transferSide{AccountID: 1, Date: "2026-10-13", Amount: -11000, Payee: "CREDITCARD BARCLAYCARD US ID2510407970"}
	oldNavy := transferSide{AccountID: 2, Date: "2026-10-12", Amount: 11000, Payee: "Payment Received"}
	if s, ok := m.score(stcu, oldNavy); !ok || s < TransferAutoScore {
		t.Fatalf("alias match score %d", s)
	}

	// Own-issuer text on a card doesn't point at the sibling card.
	stcuPmt := transferSide{AccountID: 1, Date: "2026-10-16", Amount: -20000, Payee: "MOBILE PMT CAPITAL ONE ID9279744380"}
	mom := transferSide{AccountID: 3, Date: "2026-10-15", Amount: 20000, Payee: "CAPITAL ONE MOBILE PYMT"}
	if s, ok := m.score(stcuPmt, mom); !ok || s < TransferAutoScore {
		t.Fatalf("sibling issuer score %d", s)
	}
}

func TestScoreMerchantAndChargePenalties(t *testing.T) {
	m := testMatcher()
	groceries := transferSide{AccountID: 2, Date: "2026-08-24", Amount: -27237, Payee: "ANDERSONS", CategoryID: cat(7)}
	paypal := transferSide{AccountID: 1, Date: "2026-08-26", Amount: 27237, Payee: "TRANSFER PAYPAL IDPAYPALSD11", CategoryID: cat(39)}
	if s, ok := m.score(groceries, paypal); !ok || s >= TransferPossibleScore {
		t.Fatalf("grocery purchase scored %d", s)
	}

	charge := transferSide{AccountID: 3, Date: "2026-05-16", Amount: -5000, Payee: "SP ONCEMOREWITHLOVE"}
	inflow := transferSide{AccountID: 1, Date: "2026-05-19", Amount: 5000, Payee: "Online Banking Transfer", CategoryID: cat(39)}
	if s, ok := m.score(charge, inflow); !ok || s >= TransferPossibleScore {
		t.Fatalf("card charge into checking scored %d", s)
	}

	venmo := transferSide{AccountID: 1, Date: "2026-07-07", Amount: -5000, Payee: "Purchase VENMO *ADD FUNDS , 7700 EASTPOR", CategoryID: cat(39)}
	dad := transferSide{AccountID: 14, Date: "2026-07-04", Amount: 5000, Payee: "CAPITAL ONE AUTOPAY PYMT", CategoryID: cat(39)}
	gcu := transferSide{AccountID: 5, Date: "2026-07-06", Amount: -5000, Payee: "Withdrawal ACH CAPITAL ONE TYPE: CRCARDPMT", CategoryID: cat(39)}
	wrong, _ := m.score(venmo, dad)
	right, _ := m.score(gcu, dad)
	if right < wrong+transferAmbiguity {
		t.Fatalf("venmo top-up %d vs card payment %d", wrong, right)
	}
	if got := m.mentionedAccount(venmo); got != 15 {
		t.Fatalf("venmo top-up should name the Venmo account, got %d", got)
	}
}

func TestScoreRejectsMismatches(t *testing.T) {
	m := testMatcher()
	a := transferSide{AccountID: 1, Date: "2026-01-01", Amount: -100}
	for _, b := range []transferSide{
		{AccountID: 1, Date: "2026-01-01", Amount: 100},
		{AccountID: 5, Date: "2026-01-01", Amount: 99},
		{AccountID: 5, Date: "2026-01-09", Amount: 100},
	} {
		if _, ok := m.score(a, b); ok {
			t.Fatalf("should not match %+v", b)
		}
	}
}

func TestPickPairsMarksAmbiguous(t *testing.T) {
	picked := pickPairs([]pairCandidate{
		{Left: 1, Right: 10, Score: 60, Days: 1},
		{Left: 1, Right: 11, Score: 55, Days: 2},
	}, TransferAutoScore, false)
	if len(picked) != 1 || !picked[0].Ambiguous {
		t.Fatalf("picked %+v", picked)
	}
}
