package server

import (
	"errors"
	"net/http"

	"ledger/internal/store"
)

// listUsers reports the family roster. Every member may read it: the ledger is
// shared, and the app needs the names for the member filter and entry labels.
func (s *Server) listUsers(w http.ResponseWriter, _ *http.Request, _ store.User) {
	users, err := s.store.Users()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) renameUser(w http.ResponseWriter, r *http.Request, actor store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if id != actor.ID && !actor.IsAdmin {
		writeErr(w, http.StatusForbidden, "只有管理员可以修改其他成员的用户名")
		return
	}
	var body struct {
		Username string `json:"username"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	user, err := s.store.RenameUser(id, body.Username)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request, _ store.User) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	user, err := s.store.CreateUser(body.Username, body.Password, body.IsAdmin)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

// resetUserPassword lets an administrator hand out a new password. It also
// retires that member's open sessions.
func (s *Server) resetUserPassword(w http.ResponseWriter, r *http.Request, _ store.User) {
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
	if err := s.store.SetPassword(id, body.Password); err != nil {
		s.fail(w, err)
		return
	}
	writeOK(w)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, actor store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if id == actor.ID {
		writeErr(w, http.StatusBadRequest, "不能删除自己")
		return
	}
	if err := s.store.DeleteUser(id); err != nil {
		if errors.Is(err, store.ErrInUse) {
			writeErr(w, http.StatusConflict, "该成员已有账目记录，请保留成员；如需停用，改掉其密码即可")
			return
		}
		s.fail(w, err)
		return
	}
	writeOK(w)
}

// changeMyPassword is the self-service path. The current session ends with it,
// so the web app sends the member back to the login screen.
func (s *Server) changeMyPassword(w http.ResponseWriter, r *http.Request, user store.User) {
	var body struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.store.ChangePassword(user.ID, body.OldPassword, body.NewPassword); err != nil {
		if errors.Is(err, store.ErrBadCredentials) {
			writeErr(w, http.StatusUnauthorized, "当前密码不正确")
			return
		}
		s.fail(w, err)
		return
	}
	s.logout(w, r)
}
