package money

import (
	"fmt"
	"strconv"
	"strings"
)

// Cents are integer minor units. Sign convention (net-worth oriented):
//   - Asset accounts: positive = inflow, negative = outflow.
//   - Liability accounts (credit/loan): charges are negative (more owed);
//     payments are positive (less owed). Display of liabilities uses Abs.
// Transfers: from side negative, to side positive (equal magnitude).

func Format(cents int64, currency string) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	maj := cents / 100
	min := cents % 100
	s := fmt.Sprintf("%d.%02d", maj, min)
	if currency == "" || currency == "USD" {
		s = "$" + s
	} else {
		s = currency + " " + s
	}
	if neg {
		return "-" + s
	}
	return s
}

func FormatAbs(cents int64, currency string) string {
	if cents < 0 {
		cents = -cents
	}
	return Format(cents, currency)
}

// ParseDecimal parses "12.34", "-12.34", "$12.34", "-$12.34", "(12.34)", "12" into cents.
func ParseDecimal(s string) (int64, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, "$", "")
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty amount")
	}
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg = true
		s = strings.TrimSpace(s[1 : len(s)-1])
	} else if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	parts := strings.SplitN(s, ".", 2)
	maj, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount")
	}
	var min int64
	if len(parts) == 2 {
		frac := parts[1]
		if len(frac) == 0 {
			min = 0
		} else if len(frac) == 1 {
			d, err := strconv.ParseInt(frac, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid amount")
			}
			min = d * 10
		} else {
			if len(frac) > 2 {
				frac = frac[:2]
			}
			d, err := strconv.ParseInt(frac, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid amount")
			}
			min = d
		}
	}
	cents := maj*100 + min
	if neg {
		cents = -cents
	}
	return cents, nil
}

// ExpenseAmount returns a negative cents value from a positive user-entered spend.
func ExpenseAmount(positiveCents int64) int64 {
	if positiveCents < 0 {
		return positiveCents
	}
	return -positiveCents
}
