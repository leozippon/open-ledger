// Package store owns the SQLite schema and every query the ledger needs.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	// ErrNotFound means the addressed row does not exist.
	ErrNotFound = errors.New("record not found")
	// ErrInUse means a row is still referenced by transactions.
	ErrInUse = errors.New("record is in use")
	// ErrBadCredentials means the username or password did not match.
	ErrBadCredentials = errors.New("invalid credentials")
	// ErrAdminRequired means the database has no users and none can be created
	// because no bootstrap administrator was supplied.
	ErrAdminRequired = errors.New("an administrator password is required to initialise the ledger")
)

// InvalidError carries a message that is safe to show to the user.
type InvalidError struct{ Msg string }

func (e InvalidError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return InvalidError{Msg: fmt.Sprintf(format, args...)}
}

// Admin is the first administrator, created when the database has no users yet.
type Admin struct {
	Username string
	Password string
}

// Store is a handle on the ledger database.
type Store struct {
	db *sql.DB
	// createdAdmin names the administrator Open had to bootstrap, if any.
	createdAdmin string
}

// CreatedAdmin reports the administrator created while opening an empty ledger,
// or an empty string when the ledger already had members.
func (s *Store) CreatedAdmin() string { return s.createdAdmin }

// Open connects to the SQLite file, applies pragmas, creates the schema if the
// file is new and makes sure at least one administrator exists.
func Open(path string, admin Admin) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is empty")
	}
	dsn := "file:" + url.PathEscape(path) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.setUp(admin); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

const schemaVersion = 8

const schemaSQL = `
CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  username      TEXT    NOT NULL UNIQUE,
  password_hash TEXT    NOT NULL,
  is_admin      INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
  token_version INTEGER NOT NULL DEFAULT 1,
  created_at    TEXT    NOT NULL
);

CREATE TABLE categories (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT    NOT NULL,
  kind       TEXT    NOT NULL CHECK (kind IN ('expense', 'income')),
  icon       TEXT    NOT NULL DEFAULT '',
  color      TEXT    NOT NULL DEFAULT '',
  sort_order INTEGER NOT NULL DEFAULT 0,
  archived   INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
  UNIQUE (kind, name)
);

CREATE TABLE cards (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  kind           TEXT    NOT NULL CHECK (kind IN ('debit', 'credit')),
  bank           TEXT    NOT NULL,
  name           TEXT    NOT NULL DEFAULT '',
  last4          TEXT    NOT NULL,
  network        TEXT    NOT NULL DEFAULT '',
  balance_offset INTEGER NOT NULL DEFAULT 0,
  archived       INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
  sort_order     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE card_funds (
  card_id        INTEGER NOT NULL REFERENCES cards(id) ON DELETE CASCADE,
  currency       TEXT    NOT NULL,
  balance_offset INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (card_id, currency)
);

CREATE TABLE activities (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT    NOT NULL UNIQUE,
  start_date TEXT    NOT NULL,
  end_date   TEXT    NOT NULL DEFAULT '',
  budget       INTEGER NOT NULL DEFAULT 0 CHECK (budget >= 0),
  total_budget INTEGER NOT NULL DEFAULT 0 CHECK (total_budget >= 0),
  is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
  sort_order INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE transactions (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  kind        TEXT    NOT NULL CHECK (kind IN ('expense', 'income', 'transfer', 'exchange')),
  amount      INTEGER NOT NULL CHECK (amount > 0),
  category_id INTEGER REFERENCES categories(id),
  activity_id INTEGER REFERENCES activities(id),
  card_id     INTEGER REFERENCES cards(id),
  to_card_id  INTEGER REFERENCES cards(id),
  user_id     INTEGER NOT NULL REFERENCES users(id),
  date        TEXT    NOT NULL,
  note        TEXT    NOT NULL DEFAULT '',
  shared      INTEGER NOT NULL DEFAULT 0 CHECK (shared IN (0, 1)),
  currency    TEXT    NOT NULL DEFAULT 'CNY',
  to_amount   INTEGER,
  to_currency TEXT,
  created_at  TEXT    NOT NULL,
  updated_at  TEXT    NOT NULL
);

CREATE INDEX transactions_date ON transactions(date);
CREATE INDEX transactions_activity ON transactions(activity_id);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

// setUp creates the schema in a new file and refuses a file written by another
// build. Creating the first administrator is part of it, because every
// transaction references the member who recorded it.
func (s *Store) setUp(admin Admin) error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}
	var current int
	err := s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read schema version: %w", err)
	}

	if current == 0 {
		if err := s.createSchema(); err != nil {
			return err
		}
	} else {
		steps := []func() error{
			s.migrate1to2, s.migrate2to3, s.migrate3to4, s.migrate4to5, s.migrate5to6, s.migrate6to7, s.migrate7to8,
		}
		for current < schemaVersion {
			if current < 1 || current > len(steps) {
				return fmt.Errorf("database schema version %d is not supported by this build (%d)", current, schemaVersion)
			}
			if err := steps[current-1](); err != nil {
				return err
			}
			current++
		}
		if current != schemaVersion {
			return fmt.Errorf("database schema version %d is not supported by this build (%d)", current, schemaVersion)
		}
	}
	return s.ensureAdmin(admin)
}

func (s *Store) inTx(fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func bumpSchema(tx *sql.Tx, version int) error {
	if _, err := tx.Exec(`UPDATE schema_version SET version = ?`, version); err != nil {
		return fmt.Errorf("bump schema version: %w", err)
	}
	return nil
}

func (s *Store) createSchema() error {
	return s.inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(schemaSQL); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
		if err := seed(tx); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (?)`, schemaVersion); err != nil {
			return fmt.Errorf("write schema version: %w", err)
		}
		return nil
	})
}

func (s *Store) migrate1to2() error {
	return s.inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`ALTER TABLE transactions ADD COLUMN shared INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add shared: %w", err)
		}
		return bumpSchema(tx, 2)
	})
}

func (s *Store) migrate2to3() error {
	return s.inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
		CREATE TABLE cards (
		  id             INTEGER PRIMARY KEY AUTOINCREMENT,
		  kind           TEXT    NOT NULL CHECK (kind IN ('debit', 'credit')),
		  bank           TEXT    NOT NULL,
		  last4          TEXT    NOT NULL,
		  balance_offset INTEGER NOT NULL DEFAULT 0,
		  archived       INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
		  sort_order     INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
			return fmt.Errorf("create cards: %w", err)
		}
		if _, err := tx.Exec(`
		CREATE TABLE transactions_v3 (
		  id          INTEGER PRIMARY KEY AUTOINCREMENT,
		  kind        TEXT    NOT NULL CHECK (kind IN ('expense', 'income', 'transfer')),
		  amount      INTEGER NOT NULL CHECK (amount > 0),
		  category_id INTEGER REFERENCES categories(id),
		  card_id     INTEGER REFERENCES cards(id),
		  to_card_id  INTEGER REFERENCES cards(id),
		  user_id     INTEGER NOT NULL REFERENCES users(id),
		  date        TEXT    NOT NULL,
		  note        TEXT    NOT NULL DEFAULT '',
		  shared      INTEGER NOT NULL DEFAULT 0 CHECK (shared IN (0, 1)),
		  created_at  TEXT    NOT NULL,
		  updated_at  TEXT    NOT NULL
		)`); err != nil {
			return fmt.Errorf("create transactions_v3: %w", err)
		}
		if _, err := tx.Exec(`
		INSERT INTO transactions_v3
		  (id, kind, amount, category_id, user_id, date, note, shared, created_at, updated_at)
		SELECT id, kind, amount, category_id, user_id, date, note, shared, created_at, updated_at
		FROM transactions`); err != nil {
			return fmt.Errorf("copy transactions: %w", err)
		}
		if _, err := tx.Exec(`DROP TABLE transactions`); err != nil {
			return fmt.Errorf("drop old transactions: %w", err)
		}
		if _, err := tx.Exec(`ALTER TABLE transactions_v3 RENAME TO transactions`); err != nil {
			return fmt.Errorf("rename transactions: %w", err)
		}
		if _, err := tx.Exec(`CREATE INDEX transactions_date ON transactions(date)`); err != nil {
			return fmt.Errorf("index transactions: %w", err)
		}
		return bumpSchema(tx, 3)
	})
}

func (s *Store) migrate3to4() error {
	return s.inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`ALTER TABLE cards ADD COLUMN name TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add card name: %w", err)
		}
		return bumpSchema(tx, 4)
	})
}

func (s *Store) migrate4to5() error {
	return s.inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
		CREATE TABLE card_funds (
		  card_id        INTEGER NOT NULL REFERENCES cards(id) ON DELETE CASCADE,
		  currency       TEXT    NOT NULL,
		  balance_offset INTEGER NOT NULL DEFAULT 0,
		  PRIMARY KEY (card_id, currency)
		)`); err != nil {
			return fmt.Errorf("create card_funds: %w", err)
		}
		if _, err := tx.Exec(`
		INSERT INTO card_funds (card_id, currency, balance_offset)
		SELECT id, 'CNY', balance_offset FROM cards`); err != nil {
			return fmt.Errorf("copy card funds: %w", err)
		}
		if _, err := tx.Exec(`
		CREATE TABLE transactions_v5 (
		  id          INTEGER PRIMARY KEY AUTOINCREMENT,
		  kind        TEXT    NOT NULL CHECK (kind IN ('expense', 'income', 'transfer', 'exchange')),
		  amount      INTEGER NOT NULL CHECK (amount > 0),
		  category_id INTEGER REFERENCES categories(id),
		  card_id     INTEGER REFERENCES cards(id),
		  to_card_id  INTEGER REFERENCES cards(id),
		  user_id     INTEGER NOT NULL REFERENCES users(id),
		  date        TEXT    NOT NULL,
		  note        TEXT    NOT NULL DEFAULT '',
		  shared      INTEGER NOT NULL DEFAULT 0 CHECK (shared IN (0, 1)),
		  currency    TEXT    NOT NULL DEFAULT 'CNY',
		  to_amount   INTEGER,
		  to_currency TEXT,
		  created_at  TEXT    NOT NULL,
		  updated_at  TEXT    NOT NULL
		)`); err != nil {
			return fmt.Errorf("create transactions_v5: %w", err)
		}
		if _, err := tx.Exec(`
		INSERT INTO transactions_v5
		  (id, kind, amount, category_id, card_id, to_card_id, user_id, date, note, shared, currency, created_at, updated_at)
		SELECT id, kind, amount, category_id, card_id, to_card_id, user_id, date, note, shared, 'CNY', created_at, updated_at
		FROM transactions`); err != nil {
			return fmt.Errorf("copy transactions: %w", err)
		}
		if _, err := tx.Exec(`DROP TABLE transactions`); err != nil {
			return fmt.Errorf("drop old transactions: %w", err)
		}
		if _, err := tx.Exec(`ALTER TABLE transactions_v5 RENAME TO transactions`); err != nil {
			return fmt.Errorf("rename transactions: %w", err)
		}
		if _, err := tx.Exec(`CREATE INDEX transactions_date ON transactions(date)`); err != nil {
			return fmt.Errorf("index transactions: %w", err)
		}
		return bumpSchema(tx, 5)
	})
}

func (s *Store) migrate5to6() error {
	return s.inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`ALTER TABLE cards ADD COLUMN network TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add card network: %w", err)
		}
		return bumpSchema(tx, 6)
	})
}

func (s *Store) migrate6to7() error {
	return s.inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
		CREATE TABLE activities (
		  id         INTEGER PRIMARY KEY AUTOINCREMENT,
		  name       TEXT    NOT NULL UNIQUE,
		  start_date TEXT    NOT NULL,
		  end_date   TEXT    NOT NULL DEFAULT '',
		  budget     INTEGER NOT NULL DEFAULT 0 CHECK (budget >= 0),
		  is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
		  sort_order INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
			return fmt.Errorf("create activities: %w", err)
		}
		start := time.Now().Format("2006-01-02")
		var earliest sql.NullString
		if err := tx.QueryRow(`SELECT MIN(date) FROM transactions WHERE kind IN ('expense', 'income')`).Scan(&earliest); err != nil {
			return fmt.Errorf("earliest transaction: %w", err)
		}
		if earliest.Valid {
			start = earliest.String
		}
		var raw sql.NullString
		if err := tx.QueryRow(`SELECT value FROM settings WHERE key = 'monthly_budget'`).Scan(&raw); err != nil && !errors.Is(err, sql.ErrNoRows) && !missingTable(err) {
			return fmt.Errorf("read monthly budget: %w", err)
		}
		budget := int64(0)
		if raw.Valid {
			if n, err := parseSettingInt(raw.String); err == nil {
				budget = n
			}
		}
		if _, err := tx.Exec(
			`INSERT INTO activities (name, start_date, end_date, budget, is_default, sort_order) VALUES (?, ?, '', ?, 1, 0)`,
			defaultActivityName, start, budget,
		); err != nil {
			return fmt.Errorf("seed daily activity: %w", err)
		}
		if _, err := tx.Exec(`ALTER TABLE transactions ADD COLUMN activity_id INTEGER REFERENCES activities(id)`); err != nil {
			return fmt.Errorf("add activity_id: %w", err)
		}
		if _, err := tx.Exec(`
			UPDATE transactions SET activity_id = (SELECT id FROM activities WHERE is_default = 1)
			WHERE kind IN ('expense', 'income')`); err != nil {
			return fmt.Errorf("assign daily activity: %w", err)
		}
		if _, err := tx.Exec(`CREATE INDEX transactions_activity ON transactions(activity_id)`); err != nil {
			return fmt.Errorf("index activity: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM settings WHERE key = 'monthly_budget'`); err != nil && !missingTable(err) {
			return fmt.Errorf("drop monthly budget: %w", err)
		}
		return bumpSchema(tx, 7)
	})
}

func (s *Store) migrate7to8() error {
	return s.inTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`ALTER TABLE activities ADD COLUMN total_budget INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add activity total budget: %w", err)
		}
		return bumpSchema(tx, 8)
	})
}

func parseSettingInt(raw string) (int64, error) {
	var n int64
	_, err := fmt.Sscan(raw, &n)
	return n, err
}

func missingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

func seed(tx *sql.Tx) error {
	categories := []struct {
		name, kind, icon, color string
	}{
		{"餐饮", "expense", "🍜", "#ff8a5c"},
		{"交通", "expense", "🚇", "#4a9ef7"},
		{"购物", "expense", "🛍️", "#f77ba8"},
		{"居住", "expense", "🏠", "#7c86f5"},
		{"娱乐", "expense", "🎮", "#a06ef0"},
		{"医疗", "expense", "💊", "#4fc4b0"},
		{"教育", "expense", "📚", "#f2b13c"},
		{"通讯", "expense", "📱", "#5ac8e8"},
		{"人情", "expense", "🎁", "#ef6f6f"},
		{"其他", "expense", "📦", "#9aa0aa"},
		{"工资", "income", "💰", "#34c07a"},
		{"奖金", "income", "🎉", "#f2a33c"},
		{"理财", "income", "📈", "#4a9ef7"},
		{"兼职", "income", "🧑‍💻", "#7c86f5"},
		{"其他", "income", "🪙", "#9aa0aa"},
	}
	for i, c := range categories {
		if _, err := tx.Exec(
			`INSERT INTO categories (name, kind, icon, color, sort_order) VALUES (?, ?, ?, ?, ?)`,
			c.name, c.kind, c.icon, c.color, i,
		); err != nil {
			return fmt.Errorf("seed categories: %w", err)
		}
	}
	if _, err := tx.Exec(
		`INSERT INTO activities (name, start_date, end_date, budget, is_default, sort_order) VALUES (?, ?, '', 0, 1, 0)`,
		defaultActivityName, time.Now().Format("2006-01-02"),
	); err != nil {
		return fmt.Errorf("seed daily activity: %w", err)
	}
	return nil
}

var (
	monthRE = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)
	yearRE  = regexp.MustCompile(`^\d{4}$`)
	colorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// exists reports whether a query that selects a literal returns a row.
func (s *Store) exists(query string, args ...any) (bool, error) {
	var one int
	err := s.db.QueryRow(query, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query: %w", err)
	}
	return true, nil
}

func checkMonth(month string) error {
	if !monthRE.MatchString(month) {
		return invalid("月份格式应为 YYYY-MM")
	}
	return nil
}

func checkYear(year string) error {
	if !yearRE.MatchString(year) {
		return invalid("年份格式应为 YYYY")
	}
	return nil
}

// exactIDs reports whether ids is a permutation of want.
func exactIDs(ids []int64, want map[int64]struct{}, mismatch string) error {
	if len(ids) == 0 || len(ids) != len(want) {
		return InvalidError{Msg: mismatch}
	}
	seen := map[int64]struct{}{}
	for _, id := range ids {
		if _, ok := want[id]; !ok {
			return InvalidError{Msg: mismatch}
		}
		if _, dup := seen[id]; dup {
			return InvalidError{Msg: mismatch}
		}
		seen[id] = struct{}{}
	}
	return nil
}

func checkDate(date string) error {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return invalid("日期格式应为 YYYY-MM-DD")
	}
	return nil
}

// monthRange returns the inclusive string bounds covering one month.
func monthRange(month string) (string, string) {
	return month + "-01", month + "-31"
}

// yearRange returns the inclusive string bounds covering one calendar year.
func yearRange(year string) (string, string) {
	return year + "-01-01", year + "-12-31"
}

func now() string { return time.Now().Format(time.RFC3339) }

// uniqueName turns a UNIQUE constraint violation into a user-facing message.
func uniqueName(err error, what string) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return invalid("%s名称已存在", what)
	}
	return err
}

// likePattern escapes LIKE wildcards so note search treats input literally.
func likePattern(q string) string {
	var b strings.Builder
	b.WriteByte('%')
	for _, r := range q {
		if r == '%' || r == '_' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('%')
	return b.String()
}
