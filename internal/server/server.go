// Package server exposes the ledger over HTTP: a JSON API behind a session
// cookie plus the embedded single-page web app.
package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"ledger/internal/books"
	"ledger/internal/store"
	"ledger/web"
)

// Config holds the runtime settings the server needs.
type Config struct {
	// Secret signs session cookies.
	Secret []byte
	// Secure marks the session cookie as HTTPS-only. It follows the TLS setup.
	Secure bool
	// Signup lets anyone open a new isolated book.
	Signup bool
	// DeepSeekKey enables bill recognition when set. It never goes to the browser.
	DeepSeekKey string
	// DeepSeekURL is the API origin. Empty uses the public DeepSeek endpoint.
	DeepSeekURL string
}

// Server wires the book directory, the session logic and the routes together.
type Server struct {
	books *books.Books
	cfg   Config
	limit *throttle
}

// New builds the HTTP handler for the whole application.
func New(reg *books.Books, cfg Config) (http.Handler, error) {
	if reg == nil {
		return nil, errors.New("book directory must not be nil")
	}
	if len(cfg.Secret) == 0 {
		return nil, errors.New("session secret must not be empty")
	}
	static, err := newStatic(web.FS)
	if err != nil {
		return nil, err
	}
	s := &Server{books: reg, cfg: cfg, limit: newThrottle()}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", s.publicConfig)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/signup", s.signup)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/me", s.guard(s.me))
	mux.HandleFunc("PUT /api/me/password", s.guard(s.changeMyPassword))
	mux.HandleFunc("POST /api/recognize", s.guard(s.recognize))

	mux.HandleFunc("GET /api/users", s.guard(s.listUsers))
	mux.HandleFunc("POST /api/users", s.guard(s.adminOnly(s.createUser)))
	mux.HandleFunc("PUT /api/users/{id}", s.guard(s.renameUser))
	mux.HandleFunc("PUT /api/users/{id}/password", s.guard(s.adminOnly(s.resetUserPassword)))
	mux.HandleFunc("DELETE /api/users/{id}", s.guard(s.adminOnly(s.deleteUser)))

	mux.HandleFunc("GET /api/transactions", s.guard(s.listTransactions))
	mux.HandleFunc("POST /api/transactions", s.guard(s.createTransaction))
	mux.HandleFunc("PUT /api/transactions/{id}", s.guard(s.updateTransaction))
	mux.HandleFunc("DELETE /api/transactions/{id}", s.guard(s.deleteTransaction))

	mux.HandleFunc("GET /api/summary", s.guard(s.summary))
	mux.HandleFunc("GET /api/trend", s.guard(s.trend))

	mux.HandleFunc("GET /api/cards", s.guard(s.listCards))
	mux.HandleFunc("POST /api/cards", s.guard(s.createCard))
	mux.HandleFunc("PUT /api/cards/order", s.guard(s.reorderCards))
	mux.HandleFunc("PUT /api/cards/{id}", s.guard(s.updateCard))
	mux.HandleFunc("DELETE /api/cards/{id}", s.guard(s.deleteCard))

	mux.HandleFunc("GET /api/activities", s.guard(s.listActivities))
	mux.HandleFunc("POST /api/activities", s.guard(s.createActivity))
	mux.HandleFunc("PUT /api/activities/order", s.guard(s.reorderActivities))
	mux.HandleFunc("PUT /api/activities/{id}", s.guard(s.updateActivity))
	mux.HandleFunc("DELETE /api/activities/{id}", s.guard(s.deleteActivity))
	mux.HandleFunc("GET /api/activities/months", s.guard(s.activityMonths))
	mux.HandleFunc("GET /api/search", s.guard(s.searchNotes))

	mux.HandleFunc("GET /api/categories", s.guard(s.listCategories))
	mux.HandleFunc("POST /api/categories", s.guard(s.createCategory))
	mux.HandleFunc("PUT /api/categories/order", s.guard(s.reorderCategories))
	mux.HandleFunc("PUT /api/categories/{id}", s.guard(s.updateCategory))
	mux.HandleFunc("DELETE /api/categories/{id}", s.guard(s.deleteCategory))

	mux.HandleFunc("GET /api/settings", s.guard(s.getSettings))
	mux.HandleFunc("PUT /api/settings", s.guard(s.putSettings))
	mux.HandleFunc("GET /api/export.csv", s.guard(s.exportCSV))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "接口不存在")
	})
	mux.Handle("/", static)
	return mux, nil
}

// fail maps a store error to the right status code and a message for the user.
func (s *Server) fail(w http.ResponseWriter, err error) {
	var invalid store.InvalidError
	switch {
	case errors.As(err, &invalid):
		writeErr(w, http.StatusBadRequest, invalid.Msg)
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "记录不存在")
	case errors.Is(err, store.ErrInUse):
		writeErr(w, http.StatusConflict, "已有账目在使用它，无法删除；可以改为归档")
	default:
		log.Printf("ledger: %v", err)
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		log.Printf("ledger: encode response: %v", err)
		http.Error(w, `{"error":"服务器内部错误"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(body)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeOK(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// decodeBody reads a small JSON body, reporting parse problems to the client.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeBodyLimit(w, r, dst, 64<<10)
}

func decodeBodyLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "请求内容无法解析："+err.Error())
		return false
	}
	return true
}

// pathID reads the {id} path segment.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "编号不正确")
		return 0, false
	}
	return id, true
}

func queryInt(r *http.Request, key string) int64 {
	v, err := strconv.ParseInt(r.URL.Query().Get(key), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func queryFlag(r *http.Request, key string) bool {
	return r.URL.Query().Get(key) == "1"
}
