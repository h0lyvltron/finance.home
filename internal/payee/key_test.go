package payee

import "testing"

func TestKey(t *testing.T) {
	cases := map[string]string{
		"AMAZON MKTPL*5151O6EQ0":                  "AMAZON MKTPL",
		"AMAZON MARK* 577PR44G3":                  "AMAZON MARK",
		"MOBILE PMT CAPITAL ONE ID9279744380":     "MOBILE PMT CAPITAL ONE",
		"SP HUBMAN AND CHUBGIRL":                  "HUBMAN AND CHUBGIRL",
		"TST*STEVE-OS REPUBLIC":                   "STEVE-OS REPUBLIC",
		"WL *STEAM PURCHASE":                      "STEAM PURCHASE",
		"CR Pmt/Tfr VENMO*TERAOKA, San Jose, CA":  "CR PMT/TFR VENMO",
		"Withdrawal COLUMBIA BANK, MONROE, WA":    "WITHDRAWAL COLUMBIA BANK",
		"TARGET        00011189":                  "TARGET",
		"76 - R & B SUPER STOP":                   "R & B SUPER STOP",
		"CHEVRON 0207253":                         "CHEVRON",
		"Payment Received":                        "PAYMENT RECEIVED",
		"OLDNAVY.COM":                             "OLDNAVY.COM",
		"12345":                                   "12345",
		"":                                        "",
	}
	for in, want := range cases {
		got := Key(in)
		if got != want {
			t.Errorf("Key(%q) = %q, want %q", in, got, want)
		}
		if in != "" && !Matches(in, got) {
			t.Errorf("Key(%q) = %q is not contained in the input", in, got)
		}
	}
}

func TestSimilar(t *testing.T) {
	if !Similar("SP HUBMAN AND CHUBGI", "SP HUBMAN AND CHUBGIRL") {
		t.Fatal("truncated hubman should match the full name")
	}
	if !Similar("WL *STEAM PURCHASE", "STEAMGAMES.COM 4259522") {
		t.Fatal("steam purchase and steamgames should match")
	}
	if Similar("AMAZON MKTPL*5151", "AMAZON MARK* 577") {
		t.Fatal("amazon marketplace and amazon mark are different merchants")
	}
	if Similar("COSTCO WHSE #0123", "COSTCO GAS") {
		t.Fatal("costco warehouse and costco gas should stay apart")
	}
	if Similar("ANDERSONS", "TRANSFER PAYPAL") {
		t.Fatal("unrelated payees")
	}
	if TitleName("APPLE.COM/BILL") != "Apple" {
		t.Fatalf("title=%q", TitleName("APPLE.COM/BILL"))
	}
	if TitleName("WHATNOT") != "Whatnot" || TitleName("SP HUBMAN AND CHUBGIRL") != "Hubman" {
		t.Fatalf("titles %q %q", TitleName("WHATNOT"), TitleName("SP HUBMAN AND CHUBGIRL"))
	}
}

func TestMatches(t *testing.T) {
	if !Matches("Purchase VENMO *ADD FUNDS , 7700", "purchase  venmo") {
		t.Error("expected case/space-insensitive match")
	}
	if Matches("anything", "  ") {
		t.Error("blank match must not match")
	}
}
