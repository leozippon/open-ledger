package books_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"ledger/internal/books"
	"ledger/internal/store"
)

func TestCreateAndLookup(t *testing.T) {
	reg := openReg(t)
	acct, err := reg.Create(store.Admin{Username: "alice", Password: "alice-secret"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if acct.Username != "alice" || acct.BookID == "" || acct.UserID == 0 {
		t.Fatalf("account = %+v", acct)
	}
	got, err := reg.Lookup("alice")
	if err != nil || got != acct {
		t.Fatalf("lookup = %+v, %v; want %+v", got, err, acct)
	}
	if _, err := reg.Create(store.Admin{Username: "alice", Password: "other-secret"}); !isTaken(err) {
		t.Fatalf("duplicate create = %v, want username taken", err)
	}
}

func TestBooksStayIsolated(t *testing.T) {
	reg := openReg(t)
	a, err := reg.Create(store.Admin{Username: "alice", Password: "alice-secret"})
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	b, err := reg.Create(store.Admin{Username: "bob", Password: "bobby-secret"})
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}
	if a.BookID == b.BookID {
		t.Fatal("two signups shared a book id")
	}

	aliceBook, err := reg.OpenBook(a.BookID)
	if err != nil {
		t.Fatalf("open alice: %v", err)
	}
	bobBook, err := reg.OpenBook(b.BookID)
	if err != nil {
		t.Fatalf("open bob: %v", err)
	}
	if _, err := aliceBook.CreateTransaction(expense(t, aliceBook, a.UserID, "alice coffee")); err != nil {
		t.Fatalf("alice entry: %v", err)
	}

	aliceTx, err := aliceBook.Transactions(store.TxFilter{Month: "2026-03"})
	if err != nil || len(aliceTx) != 1 {
		t.Fatalf("alice transactions = %d, %v", len(aliceTx), err)
	}
	bobTx, err := bobBook.Transactions(store.TxFilter{Month: "2026-03"})
	if err != nil || len(bobTx) != 0 {
		t.Fatalf("bob saw %d of alice's entries", len(bobTx))
	}

	_, _, _, err = reg.Authenticate("alice", "bobby-secret")
	if !errors.Is(err, store.ErrBadCredentials) {
		t.Fatalf("alice with bob's password = %v", err)
	}
	acct, _, user, err := reg.Authenticate("bob", "bobby-secret")
	if err != nil || acct.BookID != b.BookID || user.ID != b.UserID {
		t.Fatalf("authenticate bob = %+v %+v %v", acct, user, err)
	}
}

func TestInviteAndRename(t *testing.T) {
	reg := openReg(t)
	host, err := reg.Create(store.Admin{Username: "host", Password: "host-secret"})
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	other, err := reg.Create(store.Admin{Username: "other", Password: "other-secret"})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	book, err := reg.OpenBook(host.BookID)
	if err != nil {
		t.Fatalf("open host: %v", err)
	}
	guest, err := book.CreateUser("guest", "guest-secret", false)
	if err != nil {
		t.Fatalf("create guest in book: %v", err)
	}
	if err := reg.Attach(host.BookID, guest.Username, guest.ID); err != nil {
		t.Fatalf("attach guest: %v", err)
	}
	if err := reg.Attach(host.BookID, "other", 99); !isTaken(err) {
		t.Fatalf("attach taken name = %v, want username taken", err)
	}

	acct, _, user, err := reg.Authenticate("guest", "guest-secret")
	if err != nil || acct.BookID != host.BookID || user.ID != guest.ID {
		t.Fatalf("guest login = %+v %+v %v", acct, user, err)
	}

	if err := reg.Rename(host.BookID, guest.ID, other.Username); !isTaken(err) {
		t.Fatalf("rename onto other = %v, want username taken", err)
	}
	if err := reg.Rename(host.BookID, guest.ID, "visitor"); err != nil {
		t.Fatalf("rename guest: %v", err)
	}
	if _, err := book.RenameUser(guest.ID, "visitor"); err != nil {
		t.Fatalf("rename in book: %v", err)
	}
	if _, err := reg.Lookup("guest"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("old guest name still resolves")
	}
	if got, err := reg.Lookup("visitor"); err != nil || got.UserID != guest.ID {
		t.Fatalf("visitor lookup = %+v, %v", got, err)
	}

	if err := book.DeleteUser(guest.ID); err != nil {
		t.Fatalf("delete guest: %v", err)
	}
	if err := reg.Detach(host.BookID, guest.ID); err != nil {
		t.Fatalf("detach guest: %v", err)
	}
	if _, err := reg.Lookup("visitor"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("detached name still resolves")
	}
}

func TestImportExistingLedgers(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "keep.db")
	second := filepath.Join(dir, "leave.db")
	keep, err := store.Open(first, store.Admin{Username: "keeper", Password: "keeper-secret"})
	if err != nil {
		t.Fatalf("open keep: %v", err)
	}
	if _, err := keep.CreateTransaction(expense(t, keep, 1, "rent")); err != nil {
		t.Fatalf("keep entry: %v", err)
	}
	keep.Close()
	leave, err := store.Open(second, store.Admin{Username: "leaver", Password: "leaver-secret"})
	if err != nil {
		t.Fatalf("open leave: %v", err)
	}
	leave.Close()

	reg := openReg(t)
	keepID, added, err := reg.Import(first)
	if err != nil || !added {
		t.Fatalf("import keep: added=%v err=%v", added, err)
	}
	leaveID, added, err := reg.Import(second)
	if err != nil || !added {
		t.Fatalf("import leave: added=%v err=%v", added, err)
	}
	if keepID == leaveID {
		t.Fatal("imported ledgers shared a book id")
	}
	again, added, err := reg.Import(first)
	if err != nil || added || again != keepID {
		t.Fatalf("reimport keep = %q added=%v err=%v; want %q", again, added, err, keepID)
	}

	keepBook, err := reg.OpenBook(keepID)
	if err != nil {
		t.Fatalf("open imported keep: %v", err)
	}
	txs, err := keepBook.Transactions(store.TxFilter{Month: "2026-03"})
	if err != nil || len(txs) != 1 || txs[0].Note != "rent" {
		t.Fatalf("imported keep transactions = %+v, %v", txs, err)
	}
	acct, _, _, err := reg.Authenticate("leaver", "leaver-secret")
	if err != nil || acct.BookID != leaveID {
		t.Fatalf("leaver login = %+v, %v", acct, err)
	}
}

func TestImportRefusesUsernameClash(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "one.db")
	two := filepath.Join(dir, "two.db")
	for _, path := range []string{one, two} {
		st, err := store.Open(path, store.Admin{Username: "same", Password: "same-secret"})
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		st.Close()
	}
	reg := openReg(t)
	if _, _, err := reg.Import(one); err != nil {
		t.Fatalf("import one: %v", err)
	}
	if _, _, err := reg.Import(two); !isTaken(err) {
		t.Fatalf("import clash = %v, want username taken", err)
	}
}

func openReg(t *testing.T) *books.Books {
	t.Helper()
	reg, err := books.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open books: %v", err)
	}
	t.Cleanup(func() { reg.Close() })
	return reg
}

func isTaken(err error) bool {
	var invalid store.InvalidError
	return errors.As(err, &invalid) && strings.Contains(invalid.Msg, "已被使用")
}

func expense(t *testing.T, st *store.Store, userID int64, note string) store.TxInput {
	t.Helper()
	cats, err := st.Categories()
	if err != nil {
		t.Fatalf("categories: %v", err)
	}
	acts, err := st.Activities()
	if err != nil {
		t.Fatalf("activities: %v", err)
	}
	var catID, actID int64
	for _, c := range cats {
		if c.Kind == store.KindExpense {
			catID = c.ID
			break
		}
	}
	if len(acts) > 0 {
		actID = acts[0].ID
	}
	return store.TxInput{
		Kind: store.KindExpense, Amount: 1200, CategoryID: catID, ActivityID: actID,
		UserID: userID, Date: "2026-03-01", Note: note, Currency: "CNY",
	}
}
