package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ledger/internal/server"
)

func TestRecognizeRequiresSessionAndKey(t *testing.T) {
	srv, client := newServer(t)
	if response, _ := do(t, client, http.MethodPost, srv.URL+"/api/recognize", map[string]any{"text": "奶茶 26"}); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated recognize = %d, want 401", response.StatusCode)
	}
	mustLogin(t, client, srv, adminUser, adminPassword)
	response, payload := do(t, client, http.MethodPost, srv.URL+"/api/recognize", map[string]any{"text": "奶茶 26"})
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("recognize without key = %d %s, want 503", response.StatusCode, payload)
	}
	_, me := do(t, client, http.MethodGet, srv.URL+"/api/me", nil)
	if decode[map[string]any](t, me)["recognize"] != false {
		t.Fatalf("me.recognize = %s, want false when key is unset", me)
	}
}

func TestRecognizeFillsDraftFromModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "餐饮") || !strings.Contains(string(body), "喜茶") {
			t.Errorf("prompt missing ledger context: %s", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"content": `{"kind":"expense","amount":26,"category_name":"餐饮","date":"2026-09-17","note":"奶茶"}`},
			}},
		})
	}))
	t.Cleanup(upstream.Close)

	srv, client := newServerWith(t, server.Config{
		Secret:      []byte("test-session-secret"),
		DeepSeekKey: "test-key",
		DeepSeekURL: upstream.URL,
	})
	mustLogin(t, client, srv, adminUser, adminPassword)
	_, me := do(t, client, http.MethodGet, srv.URL+"/api/me", nil)
	if decode[map[string]any](t, me)["recognize"] != true {
		t.Fatalf("me.recognize = %s, want true", me)
	}

	_, cats := do(t, client, http.MethodGet, srv.URL+"/api/categories", nil)
	food := categoryIDFromJSON(t, cats, "expense", "餐饮")

	response, payload := do(t, client, http.MethodPost, srv.URL+"/api/recognize", map[string]any{"text": "喜茶 26 元"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("recognize = %d %s", response.StatusCode, payload)
	}
	got := decode[map[string]any](t, payload)
	if got["kind"] != "expense" || got["note"] != "奶茶" || got["date"] != "2026-09-17" {
		t.Fatalf("draft = %s", payload)
	}
	if int64(got["amount"].(float64)) != 2600 {
		t.Fatalf("amount = %v, want 2600 cents", got["amount"])
	}
	if int64(got["category_id"].(float64)) != food {
		t.Fatalf("category_id = %v, want %d", got["category_id"], food)
	}
}

func TestRecognizeRejectsEmptyAndUnknownCategory(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"content": `{"kind":"expense","amount":10,"category_name":"不存在的类","date":"2026-09-17","note":""}`},
			}},
		})
	}))
	t.Cleanup(upstream.Close)
	srv, client := newServerWith(t, server.Config{
		Secret:      []byte("test-session-secret"),
		DeepSeekKey: "test-key",
		DeepSeekURL: upstream.URL,
	})
	mustLogin(t, client, srv, adminUser, adminPassword)
	if response, _ := do(t, client, http.MethodPost, srv.URL+"/api/recognize", map[string]any{}); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty recognize = %d, want 400", response.StatusCode)
	}
	if response, _ := do(t, client, http.MethodPost, srv.URL+"/api/recognize", map[string]any{"text": "x"}); response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unknown category = %d, want 422", response.StatusCode)
	}
}

func categoryIDFromJSON(t *testing.T, payload []byte, kind, name string) int64 {
	t.Helper()
	for _, category := range decode[[]map[string]any](t, payload) {
		if category["kind"] == kind && category["name"] == name {
			return int64(category["id"].(float64))
		}
	}
	t.Fatalf("category %s/%s not in %s", kind, name, payload)
	return 0
}
