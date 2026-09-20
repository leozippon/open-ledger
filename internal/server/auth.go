package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"ledger/internal/store"
)

const (
	cookieName = "ledger_session"
	sessionTTL = 30 * 24 * time.Hour

	failWindow  = 10 * time.Minute
	failLimit   = 5
	lockoutTime = 10 * time.Minute
)

// session is the signed-in member and the ledger they belong to.
type session struct {
	BookID string
	Book   *store.Store
	User   store.User
}

// authedHandler is an API handler that acts on behalf of a signed-in member.
type authedHandler func(http.ResponseWriter, *http.Request, session)

// guard rejects requests without a valid session cookie.
func (s *Server) guard(h authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.session(r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "请先登录")
			return
		}
		h(w, r, sess)
	}
}

// adminOnly narrows a guarded handler to administrators.
func (s *Server) adminOnly(h authedHandler) authedHandler {
	return func(w http.ResponseWriter, r *http.Request, sess session) {
		if !sess.User.IsAdmin {
			writeErr(w, http.StatusForbidden, "只有管理员可以管理家庭成员")
			return
		}
		h(w, r, sess)
	}
}

// session resolves the cookie to a member of one book. A password change bumps
// the stored token version, which is what retires sessions opened before it.
func (s *Server) session(r *http.Request) (session, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return session{}, false
	}
	bookID, id, version, ok := s.parseToken(c.Value, time.Now())
	if !ok {
		return session{}, false
	}
	book, err := s.books.OpenBook(bookID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			log.Printf("ledger: open session book: %v", err)
		}
		return session{}, false
	}
	user, err := book.User(id)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			log.Printf("ledger: read session user: %v", err)
		}
		return session{}, false
	}
	if user.TokenVersion() != version {
		return session{}, false
	}
	return session{BookID: bookID, Book: book, User: user}, true
}

// meResponse is the identity the web app keeps in memory.
type meResponse struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	IsAdmin   bool   `json:"is_admin"`
	Recognize bool   `json:"recognize"`
}

func (s *Server) identity(u store.User) meResponse {
	return meResponse{ID: u.ID, Username: u.Username, IsAdmin: u.IsAdmin, Recognize: s.cfg.DeepSeekKey != ""}
}

func (s *Server) publicConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"signup": s.cfg.Signup})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	now, from := time.Now(), clientIP(r)
	if !s.limit.allow(now) {
		log.Printf("ledger: login throttled for %s from %s", logName(body.Username), from)
		writeErr(w, http.StatusTooManyRequests, "登录尝试过于频繁，请稍后再试")
		return
	}
	acct, _, user, err := s.books.Authenticate(body.Username, body.Password)
	if err != nil {
		if !errors.Is(err, store.ErrBadCredentials) {
			s.fail(w, err)
			return
		}
		s.limit.record(now)
		log.Printf("ledger: login failed for %s from %s", logName(body.Username), from)
		writeErr(w, http.StatusUnauthorized, "用户名或密码不正确")
		return
	}
	s.limit.clear()
	s.setSession(w, acct.BookID, user, now)
	log.Printf("ledger: login ok for %s from %s", logName(user.Username), from)
	writeJSON(w, http.StatusOK, s.identity(user))
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Signup {
		writeErr(w, http.StatusForbidden, "未开放注册")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	now, from := time.Now(), clientIP(r)
	if !s.limit.allow(now) {
		log.Printf("ledger: signup throttled for %s from %s", logName(body.Username), from)
		writeErr(w, http.StatusTooManyRequests, "尝试过于频繁，请稍后再试")
		return
	}
	acct, err := s.books.Create(store.Admin{Username: body.Username, Password: body.Password})
	if err != nil {
		var invalid store.InvalidError
		if errors.As(err, &invalid) {
			s.limit.record(now)
		}
		s.fail(w, err)
		return
	}
	book, err := s.books.OpenBook(acct.BookID)
	if err != nil {
		s.fail(w, err)
		return
	}
	user, err := book.User(acct.UserID)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.limit.clear()
	s.setSession(w, acct.BookID, user, now)
	log.Printf("ledger: signup ok for %s from %s", logName(user.Username), from)
	writeJSON(w, http.StatusCreated, s.identity(user))
}

func (s *Server) logout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, s.cookie("", -1, time.Time{}))
	writeOK(w)
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request, sess session) {
	writeJSON(w, http.StatusOK, s.identity(sess.User))
}

func (s *Server) setSession(w http.ResponseWriter, bookID string, user store.User, now time.Time) {
	http.SetCookie(w, s.cookie(s.token(bookID, user, now), int(sessionTTL/time.Second), now.Add(sessionTTL)))
}

func (s *Server) cookie(value string, maxAge int, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// token is "<book id>.<user id>.<token version>.<expiry unix>.<hmac>".
func (s *Server) token(bookID string, user store.User, now time.Time) string {
	payload := fmt.Sprintf("%s.%d.%d.%d", bookID, user.ID, user.TokenVersion(), now.Add(sessionTTL).Unix())
	return payload + "." + s.sign(payload)
}

func (s *Server) sign(payload string) string {
	mac := hmac.New(sha256.New, s.cfg.Secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) parseToken(token string, now time.Time) (bookID string, id int64, version int, ok bool) {
	cut := strings.LastIndexByte(token, '.')
	if cut < 0 {
		return "", 0, 0, false
	}
	payload, signature := token[:cut], token[cut+1:]
	if !hmac.Equal([]byte(signature), []byte(s.sign(payload))) {
		return "", 0, 0, false
	}
	fields := strings.Split(payload, ".")
	if len(fields) != 4 || fields[0] == "" {
		return "", 0, 0, false
	}
	id, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return "", 0, 0, false
	}
	version, err = strconv.Atoi(fields[2])
	if err != nil {
		return "", 0, 0, false
	}
	expiry, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil || now.Unix() >= expiry {
		return "", 0, 0, false
	}
	return fields[0], id, version, true
}

// clientIP is the peer address without its port. The ledger is exposed directly,
// so forwarding headers are deliberately ignored: they would be attacker input.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// logName renders an attempted username as one quoted token, so a hostile value
// cannot inject extra lines into the log fail2ban reads.
func logName(name string) string {
	if utf8.RuneCountInString(name) > 40 {
		name = string([]rune(name)[:40])
	}
	return strconv.Quote(name)
}

// throttle blocks logins for a while after repeated failures. One process-wide
// counter is enough for a household-sized service; fail2ban handles the rest.
type throttle struct {
	mu     sync.Mutex
	fails  []time.Time
	locked time.Time
}

func newThrottle() *throttle { return &throttle{} }

func (t *throttle) allow(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !now.Before(t.locked)
}

func (t *throttle) record(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	recent := t.fails[:0]
	for _, at := range t.fails {
		if now.Sub(at) < failWindow {
			recent = append(recent, at)
		}
	}
	t.fails = append(recent, now)
	if len(t.fails) >= failLimit {
		t.locked = now.Add(lockoutTime)
		t.fails = nil
	}
}

func (t *throttle) clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fails = nil
}
