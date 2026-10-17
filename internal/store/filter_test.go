package store

import (
	"net/url"
	"testing"
	"time"

	"finance.home/internal/models"
)

func TestResolveDatesThisMonth(t *testing.T) {
	f := QueryFilter{DatePreset: "this_month"}
	h := &models.Household{Timezone: "UTC", WeekStarts: 1}
	now := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	f.ResolveDates(h, now)
	if f.DateFrom != "2024-06-01" || f.DateTo != "2024-06-15" {
		t.Fatalf("got %s..%s", f.DateFrom, f.DateTo)
	}
}

func TestResolveDatesRolling(t *testing.T) {
	f := QueryFilter{DatePreset: "rolling", RollingDays: 7}
	h := &models.Household{Timezone: "UTC"}
	now := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	f.ResolveDates(h, now)
	if f.DateFrom != "2024-06-09" || f.DateTo != "2024-06-15" {
		t.Fatalf("got %s..%s", f.DateFrom, f.DateTo)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	amin := int64(-5000)
	review := true
	f := QueryFilter{
		DatePreset:  "custom",
		DateFrom:    "2024-01-01",
		DateTo:      "2024-01-31",
		AccountIDs:  []int64{1, 2},
		CategoryIDs: []int64{3},
		TagIDs:      []int64{4},
		People:      []string{"a", "shared"},
		Payee:       "coffee",
		AmountMin:   &amin,
		Statuses:    []string{"pending"},
		NeedsReview: &review,
		Text:        "notes",
	}
	v := f.EncodeQuery()
	got := ParseQueryFilter(v)
	if got.DatePreset != "custom" || got.DateFrom != "2024-01-01" || got.Payee != "coffee" {
		t.Fatalf("decode mismatch: %+v", got)
	}
	if len(got.AccountIDs) != 2 || len(got.People) != 2 {
		t.Fatalf("lists: %+v", got)
	}
	if got.NeedsReview == nil || !*got.NeedsReview {
		t.Fatal("needs review")
	}
	if got.AmountMin == nil || *got.AmountMin != -5000 {
		t.Fatalf("amin %v", got.AmountMin)
	}
}

func TestParseQueryFilterAccountIDCompat(t *testing.T) {
	v := url.Values{}
	v.Set("account_id", "9")
	f := ParseQueryFilter(v)
	if f.AccountID != 9 || len(f.AccountIDs) != 1 {
		t.Fatalf("%+v", f)
	}
}

func TestExpandCategories(t *testing.T) {
	// Unit-level: ExpandCategories needs DB — covered in integration if present.
	// Smoke the placeholder logic via empty.
	s := &Store{}
	out, err := s.ExpandCategories(nil)
	if err != nil || out != nil {
		t.Fatalf("%v %v", out, err)
	}
}
