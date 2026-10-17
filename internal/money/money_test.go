package money

import "testing"

func TestParseDecimal(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"12.34", 1234},
		{"$12.34", 1234},
		{"-12.34", -1234},
		{"12", 1200},
		{"12.3", 1230},
		{"0.05", 5},
		{"1,234.56", 123456},
		{"-$12.34", -1234},
		{"$-12.34", -1234},
		{"($1,234.56)", -123456},
	}
	for _, c := range cases {
		got, err := ParseDecimal(c.in)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("%q: got %d want %d", c.in, got, c.want)
		}
	}
}

func TestFormat(t *testing.T) {
	if Format(1234, "USD") != "$12.34" {
		t.Fatal(Format(1234, "USD"))
	}
	if Format(-4250, "USD") != "-$42.50" {
		t.Fatal(Format(-4250, "USD"))
	}
}
