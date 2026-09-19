package store_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"ledger/internal/store"
)

func TestMemberLifecycle(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)

	member, err := st.CreateUser("papa", "another-secret", false)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	if member.IsAdmin || member.Username != "papa" || member.Entries != 0 {
		t.Errorf("created member = %+v", member)
	}

	rejected := map[string][2]string{
		"duplicate username": {"papa", "yet-another-secret"},
		"short username":     {"p", "yet-another-secret"},
		"spaced username":    {"pa pa", "yet-another-secret"},
		"quoted username":    {`pa"pa`, "yet-another-secret"},
		"short password":     {"didi", "short"},
	}
	for name, pair := range rejected {
		if _, err := st.CreateUser(pair[0], pair[1], false); err == nil {
			t.Errorf("%s: create succeeded, want rejection", name)
		} else {
			var invalid store.InvalidError
			if !errors.As(err, &invalid) {
				t.Errorf("%s: got %T (%v), want InvalidError", name, err, err)
			}
		}
	}

	signedIn, err := st.Authenticate("papa", "another-secret")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if signedIn.ID != member.ID {
		t.Errorf("authenticate returned %d, want %d", signedIn.ID, member.ID)
	}
	if _, err := st.Authenticate("papa", "wrong-secret"); !errors.Is(err, store.ErrBadCredentials) {
		t.Errorf("wrong password = %v, want ErrBadCredentials", err)
	}
	if _, err := st.Authenticate("nobody", "another-secret"); !errors.Is(err, store.ErrBadCredentials) {
		t.Errorf("unknown username = %v, want ErrBadCredentials", err)
	}

	// Changing a password retires the sessions signed with the old version.
	before := signedIn.TokenVersion()
	if err := st.ChangePassword(member.ID, "wrong-secret", "third-secret"); !errors.Is(err, store.ErrBadCredentials) {
		t.Errorf("change with a wrong current password = %v, want ErrBadCredentials", err)
	}
	if err := st.ChangePassword(member.ID, "another-secret", "third-secret"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	after, err := st.Authenticate("papa", "third-secret")
	if err != nil {
		t.Fatalf("authenticate with the new password: %v", err)
	}
	if after.TokenVersion() <= before {
		t.Errorf("token version stayed at %d after a password change", after.TokenVersion())
	}
	if err := st.ChangePassword(member.ID, "third-secret", "no"); err == nil {
		t.Error("a too short new password was accepted")
	}

	// An administrator reset works the same way, without the old password.
	if err := st.SetPassword(member.ID, "reset-secret"); err != nil {
		t.Fatalf("reset password: %v", err)
	}
	reset, err := st.Authenticate("papa", "reset-secret")
	if err != nil {
		t.Fatalf("authenticate after reset: %v", err)
	}
	if reset.TokenVersion() <= after.TokenVersion() {
		t.Errorf("token version stayed at %d after a reset", reset.TokenVersion())
	}
	if err := st.SetPassword(9999, "reset-secret"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("reset for an unknown member = %v, want ErrNotFound", err)
	}

	renamed, err := st.RenameUser(member.ID, "didi")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if renamed.Username != "didi" || renamed.TokenVersion() != reset.TokenVersion() {
		t.Errorf("rename = %+v, want didi at token version %d", renamed, reset.TokenVersion())
	}
	if _, err := st.Authenticate("papa", "reset-secret"); !errors.Is(err, store.ErrBadCredentials) {
		t.Errorf("old username still authenticates: %v", err)
	}
	if _, err := st.Authenticate("didi", "reset-secret"); err != nil {
		t.Fatalf("authenticate after rename: %v", err)
	}
	if _, err := st.RenameUser(member.ID, testAdmin.Username); err == nil {
		t.Error("rename to a taken username succeeded")
	} else {
		var invalid store.InvalidError
		if !errors.As(err, &invalid) {
			t.Errorf("taken username: got %T (%v), want InvalidError", err, err)
		}
	}
	if _, err := st.RenameUser(member.ID, "d"); err == nil {
		t.Error("a too short new username was accepted")
	}
	if _, err := st.RenameUser(9999, "ghost"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("rename unknown member = %v, want ErrNotFound", err)
	}
	same, err := st.RenameUser(member.ID, " didi ")
	if err != nil || same.Username != "didi" {
		t.Errorf("idempotent rename = %+v, %v", same, err)
	}

	// A member who recorded nothing can go; the only administrator cannot.
	if err := st.DeleteUser(admin); err == nil {
		t.Error("the last administrator was deleted")
	}
	if err := st.DeleteUser(member.ID); err != nil {
		t.Fatalf("delete unused member: %v", err)
	}
	if err := st.DeleteUser(member.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete twice = %v, want ErrNotFound", err)
	}
}

func TestMemberWithEntriesIsKept(t *testing.T) {
	st := newStore(t)
	food := categoryID(t, st, store.KindExpense, "餐饮")

	member, err := st.CreateUser("papa", "another-secret", false)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 100, CategoryID: food,
		Date: "2026-03-04", UserID: member.ID,
	}); err != nil {
		t.Fatalf("create entry: %v", err)
	}
	if err := st.DeleteUser(member.ID); !errors.Is(err, store.ErrInUse) {
		t.Errorf("delete a member with entries = %v, want ErrInUse", err)
	}
	users, err := st.Users()
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	for _, user := range users {
		want := 0
		if user.ID == member.ID {
			want = 1
		}
		if user.Entries != want {
			t.Errorf("member %q has %d entries, want %d", user.Username, user.Entries, want)
		}
	}
}

func TestPerMemberFigures(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")

	member, err := st.CreateUser("papa", "another-secret", false)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	entries := []struct {
		amount int64
		user   int64
	}{{1000, admin}, {2500, member.ID}, {500, member.ID}}
	for _, entry := range entries {
		if _, err := st.CreateTransaction(store.TxInput{
			Kind: store.KindExpense, Amount: entry.amount, CategoryID: food,
			Date: "2026-03-04", UserID: entry.user,
		}); err != nil {
			t.Fatalf("create entry: %v", err)
		}
	}

	whole, err := st.Summary("2026-03", 0, false)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if whole.Expense != 4000 {
		t.Errorf("family expense = %d, want 4000", whole.Expense)
	}
	mine, err := st.Summary("2026-03", member.ID, false)
	if err != nil {
		t.Fatalf("member summary: %v", err)
	}
	if mine.Expense != 3000 || mine.UserID != member.ID {
		t.Errorf("member summary = %+v, want 3000 for member %d", mine, member.ID)
	}
	if len(mine.ExpenseByCategory) != 1 || mine.ExpenseByCategory[0].Amount != 3000 {
		t.Errorf("member breakdown = %+v, want one row of 3000", mine.ExpenseByCategory)
	}
	if len(mine.Days) != 1 || mine.Days[0].Expense != 3000 {
		t.Errorf("member days = %+v, want one day of 3000", mine.Days)
	}

	listed, err := st.Transactions(store.TxFilter{Month: "2026-03", UserID: member.ID})
	if err != nil {
		t.Fatalf("list by member: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("member list has %d entries, want 2", len(listed))
	}
	for _, tx := range listed {
		if tx.UserID != member.ID || tx.Username != "papa" {
			t.Errorf("entry %d is attributed to %d/%q", tx.ID, tx.UserID, tx.Username)
		}
	}

	trend, err := st.Trend(1, member.ID, false)
	if err != nil {
		t.Fatalf("member trend: %v", err)
	}
	if len(trend) != 1 {
		t.Fatalf("trend has %d points, want 1", len(trend))
	}
}

// TestForeignSchemaVersionFails keeps the version marker meaningful: unknown
// versions are refused rather than half-read. Version 1 is the one older file
// this build still migrates.
func TestForeignSchemaVersionFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE schema_version (version INTEGER NOT NULL)`,
		`INSERT INTO schema_version (version) VALUES (8)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("write version marker: %v", err)
		}
	}
	db.Close()

	if _, err := store.Open(path, testAdmin); err == nil {
		t.Fatal("a database from an unknown schema version was accepted")
	}
}
