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

func TestMatches(t *testing.T) {
	if !Matches("Purchase VENMO *ADD FUNDS , 7700", "purchase  venmo") {
		t.Error("expected case/space-insensitive match")
	}
	if Matches("anything", "  ") {
		t.Error("blank match must not match")
	}
}
