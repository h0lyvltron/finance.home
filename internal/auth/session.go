package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/http"
	"time"
)

const (
	SessionCookie = "fh_session"
	SessionTTL    = 30 * 24 * time.Hour
)

type Session struct {
	ID        string
	PersonID  int64
	CSRFToken string
	ExpiresAt time.Time
	PersonName string
	PersonSlot string
}

func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func CreateSession(db *sql.DB, personID int64) (*Session, error) {
	id, err := NewToken()
	if err != nil {
		return nil, err
	}
	csrf, err := NewToken()
	if err != nil {
		return nil, err
	}
	exp := time.Now().UTC().Add(SessionTTL)
	_, err = db.Exec(
		`INSERT INTO sessions(id, person_id, csrf_token, expires_at) VALUES (?,?,?,?)`,
		id, personID, csrf, exp.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, err
	}
	return &Session{ID: id, PersonID: personID, CSRFToken: csrf, ExpiresAt: exp}, nil
}

func LoadSession(db *sql.DB, id string) (*Session, error) {
	s := &Session{}
	var exp string
	err := db.QueryRow(`
		SELECT s.id, s.person_id, s.csrf_token, s.expires_at, p.name, p.slot
		FROM sessions s JOIN people p ON p.id = s.person_id
		WHERE s.id = ?`, id).Scan(&s.ID, &s.PersonID, &s.CSRFToken, &exp, &s.PersonName, &s.PersonSlot)
	if err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339Nano, exp)
	if err != nil {
		t, err = time.Parse(time.RFC3339, exp)
		if err != nil {
			return nil, err
		}
	}
	if time.Now().UTC().After(t) {
		_, _ = db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
		return nil, sql.ErrNoRows
	}
	s.ExpiresAt = t
	return s, nil
}

func DestroySession(db *sql.DB, id string) error {
	_, err := db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func SetSessionCookie(w http.ResponseWriter, s *Session, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    s.ID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		Expires:  s.ExpiresAt,
	})
}

func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

func SessionFromRequest(r *http.Request, db *sql.DB) (*Session, error) {
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return nil, sql.ErrNoRows
	}
	return LoadSession(db, c.Value)
}
