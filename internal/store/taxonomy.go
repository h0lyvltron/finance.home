package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"finance.home/internal/models"
)

func (s *Store) ListCategories(kind string, includeArchived bool) ([]models.Category, error) {
	q := `SELECT c.id, c.name, c.kind, c.parent_id, COALESCE(c.fixed_flexible,''), COALESCE(c.color,''),
		c.exclude_budget, c.protected, c.archived, c.display_order, COALESCE(p.name,'')
		FROM categories c LEFT JOIN categories p ON p.id = c.parent_id WHERE 1=1`
	args := []any{}
	if kind != "" {
		q += ` AND c.kind = ?`
		args = append(args, kind)
	}
	if !includeArchived {
		q += ` AND c.archived = 0`
	}
	q += ` ORDER BY c.display_order, c.name`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Category
	for rows.Next() {
		var c models.Category
		var parent sql.NullInt64
		var excl, prot, arch int
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &parent, &c.FixedFlexible, &c.Color,
			&excl, &prot, &arch, &c.DisplayOrder, &c.ParentName); err != nil {
			return nil, err
		}
		if parent.Valid {
			v := parent.Int64
			c.ParentID = &v
		}
		c.ExcludeBudget = excl == 1
		c.Protected = prot == 1
		c.Archived = arch == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetCategory(id int64) (*models.Category, error) {
	c := &models.Category{}
	var parent sql.NullInt64
	var excl, prot, arch int
	err := s.DB.QueryRow(`SELECT id, name, kind, parent_id, COALESCE(fixed_flexible,''), COALESCE(color,''),
		exclude_budget, protected, archived, display_order FROM categories WHERE id=?`, id).
		Scan(&c.ID, &c.Name, &c.Kind, &parent, &c.FixedFlexible, &c.Color, &excl, &prot, &arch, &c.DisplayOrder)
	if err != nil {
		return nil, err
	}
	if parent.Valid {
		v := parent.Int64
		c.ParentID = &v
	}
	c.ExcludeBudget = excl == 1
	c.Protected = prot == 1
	c.Archived = arch == 1
	return c, nil
}

func (s *Store) CategoryByKindName(kind, name string) (*models.Category, error) {
	c := &models.Category{}
	err := s.DB.QueryRow(`SELECT id, name, kind FROM categories WHERE kind=? AND name=? AND parent_id IS NULL`, kind, name).
		Scan(&c.ID, &c.Name, &c.Kind)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Store) CreateCategory(name, kind, fixed string, parentID *int64) (int64, error) {
	var ff any
	if fixed != "" {
		ff = fixed
	}
	var maxOrd int
	_ = s.DB.QueryRow(`SELECT COALESCE(MAX(display_order),0) FROM categories`).Scan(&maxOrd)
	res, err := s.DB.Exec(`INSERT INTO categories(name, kind, parent_id, fixed_flexible, display_order) VALUES (?,?,?,?,?)`,
		name, kind, parentID, ff, maxOrd+1)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListTags() ([]models.Tag, error) {
	rows, err := s.DB.Query(`SELECT id, name FROM tags WHERE archived=0 ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Tag
	for rows.Next() {
		var t models.Tag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) EnsureTag(name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("empty tag")
	}
	var id int64
	err := s.DB.QueryRow(`SELECT id FROM tags WHERE name = ? COLLATE NOCASE`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	res, err := s.DB.Exec(`INSERT INTO tags(name) VALUES (?)`, name)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListPayees(limit int) ([]models.Payee, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.Query(`SELECT id, name, default_category_id, default_account_id, COALESCE(last_used_at,'')
		FROM payees WHERE archived=0
		ORDER BY COALESCE(last_used_at,'') DESC, name LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Payee
	for rows.Next() {
		var p models.Payee
		var dc, da sql.NullInt64
		if err := rows.Scan(&p.ID, &p.Name, &dc, &da, &p.LastUsedAt); err != nil {
			return nil, err
		}
		if dc.Valid {
			v := dc.Int64
			p.DefaultCategoryID = &v
		}
		if da.Valid {
			v := da.Int64
			p.DefaultAccountID = &v
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) SearchPayees(q string, limit int) ([]models.Payee, error) {
	if limit <= 0 {
		limit = 10
	}
	q = strings.TrimSpace(q)
	rows, err := s.DB.Query(`SELECT id, name, default_category_id, default_account_id, COALESCE(last_used_at,'')
		FROM payees WHERE archived=0 AND name LIKE ? COLLATE NOCASE
		ORDER BY COALESCE(last_used_at,'') DESC, name LIMIT ?`, "%"+q+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Payee
	for rows.Next() {
		var p models.Payee
		var dc, da sql.NullInt64
		if err := rows.Scan(&p.ID, &p.Name, &dc, &da, &p.LastUsedAt); err != nil {
			return nil, err
		}
		if dc.Valid {
			v := dc.Int64
			p.DefaultCategoryID = &v
		}
		if da.Valid {
			v := da.Int64
			p.DefaultAccountID = &v
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) EnsurePayee(name string, categoryID, accountID *int64) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("empty payee")
	}
	var id int64
	err := s.DB.QueryRow(`SELECT id FROM payees WHERE name = ? COLLATE NOCASE`, name).Scan(&id)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err == nil {
		_, _ = s.DB.Exec(`UPDATE payees SET last_used_at=?,
			default_category_id=COALESCE(?, default_category_id),
			default_account_id=COALESCE(?, default_account_id) WHERE id=?`,
			now, categoryID, accountID, id)
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	res, err := s.DB.Exec(`INSERT INTO payees(name, default_category_id, default_account_id, last_used_at) VALUES (?,?,?,?)`,
		name, categoryID, accountID, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) CreatePayee(name string) (int64, error) {
	return s.EnsurePayee(name, nil, nil)
}

func (s *Store) CreateTag(name string) (int64, error) {
	return s.EnsureTag(name)
}
