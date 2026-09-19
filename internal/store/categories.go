package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Category groups expenses or incomes. Kind is fixed once created, because
// existing transactions rely on it.
type Category struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Icon      string `json:"icon"`
	Color     string `json:"color"`
	SortOrder int    `json:"sort_order"`
	Archived  bool   `json:"archived"`
}

// Categories lists every category, archived ones last within each kind.
func (s *Store) Categories() ([]Category, error) {
	rows, err := s.db.Query(`
		SELECT id, name, kind, icon, color, sort_order, archived
		FROM categories
		ORDER BY kind, archived, sort_order, id`)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &c.Icon, &c.Color, &c.SortOrder, &c.Archived); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateCategory inserts a category and returns it.
func (s *Store) CreateCategory(c Category) (Category, error) {
	if c.Kind != KindExpense && c.Kind != KindIncome {
		return Category{}, invalid("分类类型只能是支出或收入")
	}
	if err := normalizeCategory(&c); err != nil {
		return Category{}, err
	}
	res, err := s.db.Exec(
		`INSERT INTO categories (name, kind, icon, color, sort_order, archived)
		 VALUES (?, ?, ?, ?, COALESCE((SELECT MAX(sort_order) + 1 FROM categories WHERE kind = ?), 0), 0)`,
		c.Name, c.Kind, c.Icon, c.Color, c.Kind)
	if err != nil {
		return Category{}, uniqueName(err, "分类")
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Category{}, err
	}
	return s.category(id)
}

// UpdateCategory replaces the editable fields of a category; kind is untouched.
func (s *Store) UpdateCategory(id int64, c Category) (Category, error) {
	if err := normalizeCategory(&c); err != nil {
		return Category{}, err
	}
	res, err := s.db.Exec(
		`UPDATE categories SET name = ?, icon = ?, color = ?, sort_order = ?, archived = ? WHERE id = ?`,
		c.Name, c.Icon, c.Color, c.SortOrder, c.Archived, id)
	if err != nil {
		return Category{}, uniqueName(err, "分类")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Category{}, ErrNotFound
	}
	return s.category(id)
}

// ReorderCategories writes the display order of the active categories in one
// kind. The ids must be exactly that set, with no extras, gaps, or archived rows.
func (s *Store) ReorderCategories(kind string, ids []int64) error {
	if kind != KindExpense && kind != KindIncome {
		return invalid("分类类型只能是支出或收入")
	}
	current, err := s.Categories()
	if err != nil {
		return err
	}
	want := map[int64]struct{}{}
	for _, category := range current {
		if category.Kind == kind && !category.Archived {
			want[category.ID] = struct{}{}
		}
	}
	if err := exactIDs(ids, want, "分类顺序与现有分类不一致"); err != nil {
		return err
	}
	return s.inTx(func(tx *sql.Tx) error {
		for i, id := range ids {
			if _, err := tx.Exec(`UPDATE categories SET sort_order = ? WHERE id = ?`, i, id); err != nil {
				return fmt.Errorf("reorder categories: %w", err)
			}
		}
		return nil
	})
}

// DeleteCategory removes an unused category; it refuses categories with transactions.
func (s *Store) DeleteCategory(id int64) error {
	used, err := s.exists(`SELECT 1 FROM transactions WHERE category_id = ? LIMIT 1`, id)
	if err != nil {
		return err
	}
	if used {
		return ErrInUse
	}
	res, err := s.db.Exec(`DELETE FROM categories WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete category: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) category(id int64) (Category, error) {
	var c Category
	err := s.db.QueryRow(`
		SELECT id, name, kind, icon, color, sort_order, archived FROM categories WHERE id = ?`, id).
		Scan(&c.ID, &c.Name, &c.Kind, &c.Icon, &c.Color, &c.SortOrder, &c.Archived)
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, ErrNotFound
	}
	return c, err
}

func normalizeCategory(c *Category) error {
	c.Name = strings.TrimSpace(c.Name)
	c.Icon = strings.TrimSpace(c.Icon)
	c.Color = strings.TrimSpace(c.Color)
	if c.Name == "" {
		return invalid("请填写分类名称")
	}
	if utf8.RuneCountInString(c.Name) > 12 {
		return invalid("分类名称最多 12 个字")
	}
	if utf8.RuneCountInString(c.Icon) > 8 {
		return invalid("图标最多 8 个字符")
	}
	if c.Color == "" {
		c.Color = "#9aa0aa"
	}
	if !colorRE.MatchString(c.Color) {
		return invalid("颜色格式应为 #RRGGBB")
	}
	return nil
}
