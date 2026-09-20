package server

import (
	"errors"
	"log"
	"net/http"

	"ledger/internal/store"
)

// listUsers reports the family roster. Every member may read it: the ledger is
// shared, and the app needs the names for the member filter and entry labels.
func (s *Server) listUsers(w http.ResponseWriter, _ *http.Request, sess session) {
	users, err := sess.Book.Users()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) renameUser(w http.ResponseWriter, r *http.Request, sess session) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if id != sess.User.ID && !sess.User.IsAdmin {
		writeErr(w, http.StatusForbidden, "只有管理员可以修改其他成员的用户名")
		return
	}
	var body struct {
		Username string `json:"username"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	current, err := sess.Book.User(id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.books.Rename(sess.BookID, id, body.Username); err != nil {
		s.fail(w, err)
		return
	}
	user, err := sess.Book.RenameUser(id, body.Username)
	if err != nil {
		if revertErr := s.books.Rename(sess.BookID, id, current.Username); revertErr != nil {
			log.Printf("ledger: revert rename %s: %v (after %v)", current.Username, revertErr, err)
		}
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request, sess session) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	name, err := store.CheckUsername(body.Username)
	if err != nil {
		s.fail(w, err)
		return
	}
	if _, err := s.books.Lookup(name); err == nil {
		s.fail(w, store.UsernameTaken(name))
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		s.fail(w, err)
		return
	}
	user, err := sess.Book.CreateUser(body.Username, body.Password, body.IsAdmin)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.books.Attach(sess.BookID, user.Username, user.ID); err != nil {
		if delErr := sess.Book.DeleteUser(user.ID); delErr != nil {
			log.Printf("ledger: revert create user %s: %v (after %v)", user.Username, delErr, err)
		}
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

// resetUserPassword lets an administrator hand out a new password. It also
// retires that member's open sessions.
func (s *Server) resetUserPassword(w http.ResponseWriter, r *http.Request, sess session) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := sess.Book.SetPassword(id, body.Password); err != nil {
		s.fail(w, err)
		return
	}
	writeOK(w)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, sess session) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if id == sess.User.ID {
		writeErr(w, http.StatusBadRequest, "不能删除自己")
		return
	}
	if _, err := sess.Book.User(id); err != nil {
		s.fail(w, err)
		return
	}
	if err := sess.Book.DeleteUser(id); err != nil {
		if errors.Is(err, store.ErrInUse) {
			writeErr(w, http.StatusConflict, "该成员已有账目记录，请保留成员；如需停用，改掉其密码即可")
			return
		}
		s.fail(w, err)
		return
	}
	if err := s.books.Detach(sess.BookID, id); err != nil {
		s.fail(w, err)
		return
	}
	writeOK(w)
}

// changeMyPassword is the self-service path. The current session ends with it,
// so the web app sends the member back to the login screen.
func (s *Server) changeMyPassword(w http.ResponseWriter, r *http.Request, sess session) {
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := sess.Book.ChangePassword(sess.User.ID, body.OldPassword, body.NewPassword); err != nil {
		if errors.Is(err, store.ErrBadCredentials) {
			writeErr(w, http.StatusUnauthorized, "当前密码不正确")
			return
		}
		s.fail(w, err)
		return
	}
	s.logout(w, r)
}
