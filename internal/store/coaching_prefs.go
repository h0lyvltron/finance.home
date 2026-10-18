package store

import (
	"database/sql"
	"time"
)

// CoachingPref is a per-person snooze/dismiss for an insight kind.
type CoachingPref struct {
	Kind         string
	SnoozedUntil string // YYYY-MM-DD
	Dismissed    bool
}

// ListCoachingPrefs returns prefs for a person.
func (s *Store) ListCoachingPrefs(personID int64) ([]CoachingPref, error) {
	rows, err := s.DB.Query(`
		SELECT kind, COALESCE(snoozed_until,''), dismissed
		FROM coaching_prefs WHERE person_id=?`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CoachingPref
	for rows.Next() {
		var p CoachingPref
		var dismissed int
		if err := rows.Scan(&p.Kind, &p.SnoozedUntil, &dismissed); err != nil {
			return nil, err
		}
		p.Dismissed = dismissed != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) coachingPrefMap(personID int64) (map[string]CoachingPref, error) {
	list, err := s.ListCoachingPrefs(personID)
	if err != nil {
		return nil, err
	}
	out := map[string]CoachingPref{}
	for _, p := range list {
		out[p.Kind] = p
	}
	return out, nil
}

// IsCoachingSuppressed returns true when the kind is dismissed or still snoozed.
func IsCoachingSuppressed(prefs map[string]CoachingPref, kind string, today string) bool {
	p, ok := prefs[kind]
	if !ok {
		return false
	}
	if p.Dismissed {
		return true
	}
	if p.SnoozedUntil != "" && p.SnoozedUntil >= today {
		return true
	}
	return false
}

// SnoozeCoaching hides an insight kind until endDate (YYYY-MM-DD inclusive).
func (s *Store) SnoozeCoaching(personID int64, kind string, endDate string) error {
	kind = normalizeInsightKind(kind)
	if kind == "" || endDate == "" {
		return sql.ErrNoRows
	}
	_, err := s.DB.Exec(`
		INSERT INTO coaching_prefs(person_id, kind, snoozed_until, dismissed, updated_at)
		VALUES (?,?,?,0,strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(person_id, kind) DO UPDATE SET
			snoozed_until=excluded.snoozed_until,
			dismissed=0,
			updated_at=excluded.updated_at`, personID, kind, endDate)
	return err
}

// DismissCoaching permanently hides an insight kind for this person (until cleared).
func (s *Store) DismissCoaching(personID int64, kind string) error {
	kind = normalizeInsightKind(kind)
	if kind == "" {
		return sql.ErrNoRows
	}
	_, err := s.DB.Exec(`
		INSERT INTO coaching_prefs(person_id, kind, snoozed_until, dismissed, updated_at)
		VALUES (?, ?, NULL, 1, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(person_id, kind) DO UPDATE SET
			snoozed_until=NULL,
			dismissed=1,
			updated_at=excluded.updated_at`, personID, kind)
	return err
}

// ClearCoachingPref restores a dismissed/snoozed kind.
func (s *Store) ClearCoachingPref(personID int64, kind string) error {
	_, err := s.DB.Exec(`DELETE FROM coaching_prefs WHERE person_id=? AND kind=?`, personID, kind)
	return err
}

func normalizeInsightKind(kind string) string {
	switch kind {
	case "pace", "review", "bills", "positive", "goal", "spike", "card", "split":
		return kind
	default:
		return ""
	}
}

// SnoozeUntilDays returns YYYY-MM-DD for today+days in household tz.
func SnoozeUntilDays(hTimezone string, days int, now time.Time) string {
	if hTimezone != "" {
		if loc, err := time.LoadLocation(hTimezone); err == nil {
			now = now.In(loc)
		}
	}
	return now.AddDate(0, 0, days).Format("2006-01-02")
}
