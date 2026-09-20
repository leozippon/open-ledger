package store

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Password hashing parameters. The encoded form is self describing, so the cost
// can be raised later without invalidating existing hashes.
const (
	hashScheme     = "pbkdf2-sha256"
	hashIterations = 600000
	hashKeyLength  = 32
	hashSaltLength = 16

	minPasswordLength = 8
)

// User is one family member. The password hash never leaves this package.
type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	IsAdmin   bool   `json:"is_admin"`
	CreatedAt string `json:"created_at"`
	// Entries counts the transactions this member recorded; the member list
	// uses it to explain why a member cannot be deleted.
	Entries int `json:"entries"`

	tokenVersion int
}

// TokenVersion changes whenever the password changes, which retires old sessions.
func (u User) TokenVersion() int { return u.tokenVersion }

const userColumns = `id, username, is_admin, token_version, created_at`

// Users lists every family member, oldest first, with their entry counts.
func (s *Store) Users() ([]User, error) {
	rows, err := s.db.Query(`
		SELECT u.id, u.username, u.is_admin, u.token_version, u.created_at,
		       (SELECT COUNT(*) FROM transactions t WHERE t.user_id = u.id)
		FROM users u ORDER BY u.id`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.IsAdmin, &u.tokenVersion, &u.CreatedAt, &u.Entries); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// User reads one member by id.
func (s *Store) User(id int64) (User, error) {
	var u User
	err := s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Username, &u.IsAdmin, &u.tokenVersion, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// CreateUser adds a family member.
func (s *Store) CreateUser(username, password string, isAdmin bool) (User, error) {
	name, err := checkUsername(username)
	if err != nil {
		return User{}, err
	}
	if err := checkPassword(password); err != nil {
		return User{}, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return User{}, err
	}
	result, err := s.db.Exec(
		`INSERT INTO users (username, password_hash, is_admin, token_version, created_at) VALUES (?, ?, ?, 1, ?)`,
		name, hash, isAdmin, now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return User{}, UsernameTaken(name)
		}
		return User{}, fmt.Errorf("create user: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return User{}, err
	}
	return s.User(id)
}

// Authenticate checks a username and password pair. Both an unknown username
// and a wrong password report ErrBadCredentials so neither can be told apart.
func (s *Store) Authenticate(username, password string) (User, error) {
	var (
		u    User
		hash string
	)
	err := s.db.QueryRow(`SELECT `+userColumns+`, password_hash FROM users WHERE username = ?`, strings.TrimSpace(username)).
		Scan(&u.ID, &u.Username, &u.IsAdmin, &u.tokenVersion, &u.CreatedAt, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrBadCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("read user: %w", err)
	}
	ok, err := verifyPassword(hash, password)
	if err != nil {
		return User{}, err
	}
	if !ok {
		return User{}, ErrBadCredentials
	}
	return u, nil
}

// RenameUser changes a member's login name. Sessions stay valid because they
// are bound to the member id, not the name. Old entries keep the same user_id
// and pick up the new name when listed.
func (s *Store) RenameUser(id int64, username string) (User, error) {
	name, err := checkUsername(username)
	if err != nil {
		return User{}, err
	}
	user, err := s.User(id)
	if err != nil {
		return User{}, err
	}
	if user.Username == name {
		return user, nil
	}
	if _, err := s.db.Exec(`UPDATE users SET username = ? WHERE id = ?`, name, id); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return User{}, UsernameTaken(name)
		}
		return User{}, fmt.Errorf("rename user: %w", err)
	}
	return s.User(id)
}

// SetPassword replaces a member's password and retires their open sessions.
func (s *Store) SetPassword(id int64, password string) error {
	if err := checkPassword(password); err != nil {
		return err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(
		`UPDATE users SET password_hash = ?, token_version = token_version + 1 WHERE id = ?`, hash, id)
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ChangePassword verifies the current password before replacing it.
func (s *Store) ChangePassword(id int64, current, next string) error {
	var hash string
	err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, id).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	ok, err := verifyPassword(hash, current)
	if err != nil {
		return err
	}
	if !ok {
		return ErrBadCredentials
	}
	return s.SetPassword(id, next)
}

// DeleteUser removes a member who has recorded nothing. Members with entries
// are kept so the ledger stays attributable.
func (s *Store) DeleteUser(id int64) error {
	user, err := s.User(id)
	if err != nil {
		return err
	}
	used, err := s.exists(`SELECT 1 FROM transactions WHERE user_id = ? LIMIT 1`, id)
	if err != nil {
		return err
	}
	if used {
		return ErrInUse
	}
	if user.IsAdmin {
		var admins int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin = 1`).Scan(&admins); err != nil {
			return fmt.Errorf("count administrators: %w", err)
		}
		if admins <= 1 {
			return invalid("至少要保留一个管理员")
		}
	}
	if _, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

// ensureAdmin creates the bootstrap administrator when the ledger has no users.
func (s *Store) ensureAdmin(admin Admin) error {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&id)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read users: %w", err)
	}
	name, hash, err := prepareAdmin(admin)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(
		`INSERT INTO users (username, password_hash, is_admin, token_version, created_at) VALUES (?, ?, 1, 1, ?)`,
		name, hash, now())
	if err != nil {
		return fmt.Errorf("create administrator: %w", err)
	}
	s.createdAdmin = name
	_, err = result.LastInsertId()
	return err
}

func prepareAdmin(admin Admin) (username, hash string, err error) {
	if admin.Password == "" {
		return "", "", ErrAdminRequired
	}
	if username, err = checkUsername(admin.Username); err != nil {
		return "", "", fmt.Errorf("administrator username: %w", err)
	}
	if err = checkPassword(admin.Password); err != nil {
		return "", "", fmt.Errorf("administrator password: %w", err)
	}
	hash, err = hashPassword(admin.Password)
	return username, hash, err
}

// CheckUsername keeps names short, printable and free of whitespace or quotes,
// so they read unambiguously in the login log lines fail2ban watches.
func CheckUsername(name string) (string, error) {
	return checkUsername(name)
}

// CheckPassword is the shared length rule for a new password.
func CheckPassword(password string) error {
	return checkPassword(password)
}

// UsernameTaken is the user-facing error when a login name is already in use,
// either inside one book or across the whole directory.
func UsernameTaken(name string) error {
	return invalid("用户名「%s」已被使用", name)
}

func checkUsername(name string) (string, error) {
	name = strings.TrimSpace(name)
	length := utf8.RuneCountInString(name)
	if length < 2 || length > 20 {
		return "", invalid("用户名需要 2 到 20 个字符")
	}
	for _, r := range name {
		if unicode.IsSpace(r) || !unicode.IsPrint(r) || r == '"' {
			return "", invalid("用户名不能包含空格、引号或不可见字符")
		}
	}
	return name, nil
}

func checkPassword(password string) error {
	if utf8.RuneCountInString(password) < minPasswordLength {
		return invalid("密码至少需要 %d 个字符", minPasswordLength)
	}
	if len(password) > 256 {
		return invalid("密码过长")
	}
	return nil
}

var b64 = base64.RawStdEncoding

func hashPassword(password string) (string, error) {
	salt := make([]byte, hashSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, hashIterations, hashKeyLength)
	if err != nil {
		return "", fmt.Errorf("derive password hash: %w", err)
	}
	return fmt.Sprintf("%s$%d$%s$%s", hashScheme, hashIterations, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func verifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != hashScheme {
		return false, fmt.Errorf("stored password hash is not in %s form", hashScheme)
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 {
		return false, fmt.Errorf("stored password hash has an invalid iteration count %q", parts[1])
	}
	salt, err := b64.DecodeString(parts[2])
	if err != nil {
		return false, fmt.Errorf("stored password hash has an invalid salt: %w", err)
	}
	want, err := b64.DecodeString(parts[3])
	if err != nil {
		return false, fmt.Errorf("stored password hash is unreadable: %w", err)
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false, fmt.Errorf("derive password hash: %w", err)
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
