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
		if !strings.Contains(string(body), "餐饮") || !strings.Contains(string(body), "喜茶") || !strings.Contains(string(body), "日常生活") {
			t.Errorf("prompt missing ledger context: %s", body)
		}
		if !strings.Contains(string(body), "不同订单或不同付款不要合并") || !strings.Contains(string(body), "activity_id") {
			t.Errorf("prompt missing core rules: %s", body)
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
	entries := got["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %s", payload)
	}
	row := entries[0].(map[string]any)
	if row["kind"] != "expense" || row["note"] != "奶茶" || row["date"] != "2026-09-17" {
		t.Fatalf("draft = %s", payload)
	}
	if int64(row["amount"].(float64)) != 2600 {
		t.Fatalf("amount = %v, want 2600 cents", row["amount"])
	}
	if int64(row["category_id"].(float64)) != food {
		t.Fatalf("category_id = %v, want %d", row["category_id"], food)
	}
}

func TestRecognizeRejectsEmptyAndFallsBackCategory(t *testing.T) {
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
	_, cats := do(t, client, http.MethodGet, srv.URL+"/api/categories", nil)
	other := categoryIDFromJSON(t, cats, "expense", "其他")
	response, payload := do(t, client, http.MethodPost, srv.URL+"/api/recognize", map[string]any{"text": "x"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unknown category = %d %s, want 200 with 其他", response.StatusCode, payload)
	}
	row := decode[map[string]any](t, payload)["entries"].([]any)[0].(map[string]any)
	if int64(row["category_id"].(float64)) != other {
		t.Fatalf("fallback category = %v, want %d", row["category_id"], other)
	}
}

func TestRecognizeMultipleEntries(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"content": `{"entries":[{"kind":"expense","amount":26,"category_name":"餐饮","date":"2026-09-17","note":"奶茶"},{"kind":"expense","amount":12.5,"category_name":"交通","date":"2026-09-17","note":"地铁"},{"kind":"income","amount":200,"category_name":"工资","date":"2026-09-01","note":"薪水"}]}`},
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
	_, cats := do(t, client, http.MethodGet, srv.URL+"/api/categories", nil)
	food := categoryIDFromJSON(t, cats, "expense", "餐饮")
	transit := categoryIDFromJSON(t, cats, "expense", "交通")
	salary := categoryIDFromJSON(t, cats, "income", "工资")

	response, payload := do(t, client, http.MethodPost, srv.URL+"/api/recognize", map[string]any{"text": "三笔订单"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("recognize = %d %s", response.StatusCode, payload)
	}
	entries := decode[map[string]any](t, payload)["entries"].([]any)
	if len(entries) != 3 {
		t.Fatalf("entries = %s", payload)
	}
	want := []struct {
		kind  string
		note  string
		cents int64
		cat   int64
	}{
		{"expense", "奶茶", 2600, food},
		{"expense", "地铁", 1250, transit},
		{"income", "薪水", 20000, salary},
	}
	for i, row := range want {
		got := entries[i].(map[string]any)
		if got["kind"] != row.kind || got["note"] != row.note || int64(got["amount"].(float64)) != row.cents || int64(got["category_id"].(float64)) != row.cat {
			t.Fatalf("entry %d = %v, want %+v", i, got, row)
		}
	}
}

func TestRecognizeBindsActivityAndCard(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "香港旅行") || !strings.Contains(string(body), "招商") {
			t.Errorf("prompt missing activity or card: %s", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"content": `{"entries":[{"kind":"expense","amount":88,"category_name":"交通","activity_name":"香港旅行","card_last4":"1234","date":"2026-09-21","note":"机场大巴"}]}`},
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
	response, actPayload := do(t, client, http.MethodPost, srv.URL+"/api/activities", map[string]any{"name": "香港旅行", "start_date": "2026-09-01"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create activity = %d %s", response.StatusCode, actPayload)
	}
	tripID := int64(decode[map[string]any](t, actPayload)["id"].(float64))
	response, cardPayload := do(t, client, http.MethodPost, srv.URL+"/api/cards", map[string]any{"kind": "debit", "bank": "招商", "last4": "1234", "balance": 0})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create card = %d %s", response.StatusCode, cardPayload)
	}
	cardID := int64(decode[map[string]any](t, cardPayload)["id"].(float64))
	response, payload := do(t, client, http.MethodPost, srv.URL+"/api/recognize", map[string]any{"text": "机场大巴 88"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("recognize = %d %s", response.StatusCode, payload)
	}
	row := decode[map[string]any](t, payload)["entries"].([]any)[0].(map[string]any)
	if int64(row["activity_id"].(float64)) != tripID {
		t.Fatalf("activity_id = %v, want %d", row["activity_id"], tripID)
	}
	if int64(row["card_id"].(float64)) != cardID {
		t.Fatalf("card_id = %v, want %d", row["card_id"], cardID)
	}
}

func TestRecognizeOrdersKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"content": `{"orders":[{"kind":"expense","amount":199,"category_name":"购物","date":"2026-09-21","note":"除螨仪"},{"kind":"expense","amount":89,"category_name":"购物","date":"2026-09-21","note":"口腔护理"}]}`},
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
	response, payload := do(t, client, http.MethodPost, srv.URL+"/api/recognize", map[string]any{"text": "两笔京东订单"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("recognize = %d %s", response.StatusCode, payload)
	}
	entries := decode[map[string]any](t, payload)["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %s", payload)
	}
	if entries[0].(map[string]any)["note"] != "除螨仪" || int64(entries[0].(map[string]any)["amount"].(float64)) != 19900 {
		t.Fatalf("first = %v", entries[0])
	}
	if entries[1].(map[string]any)["note"] != "口腔护理" || int64(entries[1].(map[string]any)["amount"].(float64)) != 8900 {
		t.Fatalf("second = %v", entries[1])
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
