// Package books is the process-wide directory of isolated household ledgers.
//
// accounts.db maps a globally unique username to one book file and the member
// id inside that file. Each book is an ordinary store.Store. Passwords stay in
// the book; this package never copies them.
package books

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"ledger/internal/store"

	_ "modernc.org/sqlite"
)

// Books is the registry of household ledgers for one process.
type Books struct {
	dir  string
	db   *sql.DB
	mu   sync.Mutex
	open map[string]*store.Store
}

// Account is a login name and the book it opens.
type Account struct {
	Username string
	BookID   string
	UserID   int64
}

// Open creates or opens the directory at dir, including accounts.db.
func Open(dir string) (*Books, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("data directory is empty")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("data directory: %w", err)
	}
	dir = abs
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(dir, "accounts.db")
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
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS books (
			id   TEXT PRIMARY KEY,
			path TEXT NOT NULL UNIQUE
		);
		CREATE TABLE IF NOT EXISTS accounts (
			username TEXT PRIMARY KEY,
			book_id  TEXT NOT NULL REFERENCES books(id),
			user_id  INTEGER NOT NULL,
			UNIQUE (book_id, user_id)
		);
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create accounts schema: %w", err)
	}
	return &Books{dir: dir, db: db, open: map[string]*store.Store{}}, nil
}

// Close releases the directory and every cached book.
func (b *Books) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	var first error
	for id, st := range b.open {
		if err := st.Close(); err != nil && first == nil {
			first = err
		}
		delete(b.open, id)
	}
	if err := b.db.Close(); err != nil && first == nil {
		first = err
	}
	return first
}

// Empty reports whether anyone can log in yet.
func (b *Books) Empty() (bool, error) {
	var n int
	if err := b.db.QueryRow(`SELECT COUNT(*) FROM accounts`).Scan(&n); err != nil {
		return false, fmt.Errorf("count accounts: %w", err)
	}
	return n == 0, nil
}

// Lookup finds the book for a login name.
func (b *Books) Lookup(username string) (Account, error) {
	var a Account
	err := b.db.QueryRow(
		`SELECT username, book_id, user_id FROM accounts WHERE username = ?`,
		strings.TrimSpace(username),
	).Scan(&a.Username, &a.BookID, &a.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, store.ErrNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("lookup account: %w", err)
	}
	return a, nil
}

// OpenBook returns the cached store for a book, opening the file on first use.
func (b *Books) OpenBook(id string) (*store.Store, error) {
	b.mu.Lock()
	if st, ok := b.open[id]; ok {
		b.mu.Unlock()
		return st, nil
	}
	b.mu.Unlock()

	var path string
	err := b.db.QueryRow(`SELECT path FROM books WHERE id = ?`, id).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read book %s: %w", id, err)
	}

	st, err := store.Open(path, store.Admin{})
	if err != nil {
		return nil, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if cached, ok := b.open[id]; ok {
		st.Close()
		return cached, nil
	}
	b.open[id] = st
	return st, nil
}

// Authenticate resolves a login name to a book and checks the password there.
func (b *Books) Authenticate(username, password string) (Account, *store.Store, store.User, error) {
	acct, err := b.Lookup(username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Account{}, nil, store.User{}, store.ErrBadCredentials
		}
		return Account{}, nil, store.User{}, err
	}
	book, err := b.OpenBook(acct.BookID)
	if err != nil {
		return Account{}, nil, store.User{}, err
	}
	user, err := book.Authenticate(username, password)
	if err != nil {
		return Account{}, nil, store.User{}, err
	}
	if user.ID != acct.UserID {
		return Account{}, nil, store.User{}, fmt.Errorf("account %q is user %d in book %s, found %d", acct.Username, acct.UserID, acct.BookID, user.ID)
	}
	return acct, book, user, nil
}

// Create opens a new book whose first member is the given administrator.
func (b *Books) Create(admin store.Admin) (Account, error) {
	name, err := store.CheckUsername(admin.Username)
	if err != nil {
		return Account{}, err
	}
	if err := store.CheckPassword(admin.Password); err != nil {
		return Account{}, err
	}
	if _, err := b.Lookup(name); err == nil {
		return Account{}, store.UsernameTaken(name)
	} else if !errors.Is(err, store.ErrNotFound) {
		return Account{}, err
	}

	id, err := newID()
	if err != nil {
		return Account{}, err
	}
	if err := os.MkdirAll(filepath.Join(b.dir, "houses"), 0o750); err != nil {
		return Account{}, fmt.Errorf("create houses directory: %w", err)
	}
	path, err := filepath.Abs(filepath.Join(b.dir, "houses", id+".db"))
	if err != nil {
		return Account{}, err
	}
	st, err := store.Open(path, admin)
	if err != nil {
		return Account{}, err
	}
	users, err := st.Users()
	if err != nil {
		st.Close()
		removeSQLite(path)
		return Account{}, err
	}
	var founder store.User
	for _, u := range users {
		if u.Username == name {
			founder = u
			break
		}
	}
	if founder.ID == 0 {
		st.Close()
		removeSQLite(path)
		return Account{}, errors.New("new book has no administrator")
	}
	acct := Account{Username: founder.Username, BookID: id, UserID: founder.ID}
	if err := b.insertBook(id, path, []Account{acct}); err != nil {
		st.Close()
		removeSQLite(path)
		return Account{}, err
	}
	b.mu.Lock()
	b.open[id] = st
	b.mu.Unlock()
	return acct, nil
}

// Import registers every member of an existing ledger file as one book.
// A path that is already registered is left alone. A username that already
// belongs to another book fails the whole import.
func (b *Books) Import(path string) (id string, added bool, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false, fmt.Errorf("import %s: %w", path, err)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", false, fmt.Errorf("import %s: %w", path, err)
	}

	var existing string
	err = b.db.QueryRow(`SELECT id FROM books WHERE path = ?`, abs).Scan(&existing)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, fmt.Errorf("import %s: %w", path, err)
	}

	st, err := store.Open(abs, store.Admin{})
	if err != nil {
		return "", false, fmt.Errorf("import %s: %w", path, err)
	}
	users, err := st.Users()
	if err != nil {
		st.Close()
		return "", false, fmt.Errorf("import %s: %w", path, err)
	}
	if len(users) == 0 {
		st.Close()
		return "", false, fmt.Errorf("import %s: the ledger has no members", path)
	}

	id, err = newID()
	if err != nil {
		st.Close()
		return "", false, err
	}
	accts := make([]Account, 0, len(users))
	for _, u := range users {
		accts = append(accts, Account{Username: u.Username, BookID: id, UserID: u.ID})
	}
	if err := b.insertBook(id, abs, accts); err != nil {
		st.Close()
		return "", false, err
	}
	b.mu.Lock()
	b.open[id] = st
	b.mu.Unlock()
	return id, true, nil
}

// Attach records a member who was just added inside a book.
func (b *Books) Attach(bookID, username string, userID int64) error {
	name, err := store.CheckUsername(username)
	if err != nil {
		return err
	}
	_, err = b.db.Exec(
		`INSERT INTO accounts (username, book_id, user_id) VALUES (?, ?, ?)`,
		name, bookID, userID,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return store.UsernameTaken(name)
		}
		return fmt.Errorf("attach account: %w", err)
	}
	return nil
}

// Rename changes the global login name for one member.
func (b *Books) Rename(bookID string, userID int64, username string) error {
	name, err := store.CheckUsername(username)
	if err != nil {
		return err
	}
	var current string
	err = b.db.QueryRow(
		`SELECT username FROM accounts WHERE book_id = ? AND user_id = ?`,
		bookID, userID,
	).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read account: %w", err)
	}
	if current == name {
		return nil
	}
	_, err = b.db.Exec(
		`UPDATE accounts SET username = ? WHERE book_id = ? AND user_id = ?`,
		name, bookID, userID,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return store.UsernameTaken(name)
		}
		return fmt.Errorf("rename account: %w", err)
	}
	return nil
}

// Detach drops a member from the directory after they have left a book.
func (b *Books) Detach(bookID string, userID int64) error {
	_, err := b.db.Exec(`DELETE FROM accounts WHERE book_id = ? AND user_id = ?`, bookID, userID)
	if err != nil {
		return fmt.Errorf("detach account: %w", err)
	}
	return nil
}

func (b *Books) insertBook(id, path string, accts []Account) error {
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO books (id, path) VALUES (?, ?)`, id, path); err != nil {
		return fmt.Errorf("register book: %w", err)
	}
	for _, a := range accts {
		if _, err := tx.Exec(
			`INSERT INTO accounts (username, book_id, user_id) VALUES (?, ?, ?)`,
			a.Username, a.BookID, a.UserID,
		); err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return store.UsernameTaken(a.Username)
			}
			return fmt.Errorf("register account %s: %w", a.Username, err)
		}
	}
	return tx.Commit()
}

func newID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate book id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func removeSQLite(path string) {
	os.Remove(path)
	os.Remove(path + "-wal")
	os.Remove(path + "-shm")
}
