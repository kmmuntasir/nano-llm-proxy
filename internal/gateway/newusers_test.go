package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// putSettings sends PUT /api/settings as the session behind cookie.
func putSettings(t *testing.T, g *gateway, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("Origin", "https://gateway.example.com")
	rec := httptest.NewRecorder()
	g.requireSession(g.requireAdmin(g.handlePutSettings))(rec, req)
	return rec
}

// getNewUserDefaults reads newUsers.disabledProviderIds through GET
// /api/settings and also returns the raw body.
func getNewUserDefaults(t *testing.T, g *gateway, cookie *http.Cookie) ([]int64, string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/settings", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	g.requireSession(g.requireAdmin(g.handleGetSettings))(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET settings: got %d: %s", rec.Code, rec.Body.String())
	}
	var parsed struct {
		Settings struct {
			NewUsers struct {
				DisabledProviderIDs []int64 `json:"disabledProviderIds"`
			} `json:"newUsers"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	return parsed.Settings.NewUsers.DisabledProviderIDs, rec.Body.String()
}

// createUserReq sends POST /api/users as the session behind cookie and
// returns the new user's id.
func createUserReq(t *testing.T, g *gateway, cookie *http.Cookie, email, role string) int64 {
	t.Helper()
	body := fmt.Sprintf(`{"name":"N","email":%q,"password":"new-user-pass-123","role":%q}`, email, role)
	req := httptest.NewRequest("POST", "/api/users", strings.NewReader(body))
	req.AddCookie(cookie)
	req.Header.Set("Origin", "https://gateway.example.com")
	rec := httptest.NewRecorder()
	g.requireSession(g.requireAdmin(g.handleCreateUser))(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %s: got %d: %s", email, rec.Code, rec.Body.String())
	}
	var parsed struct {
		User userJSON `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode created user: %v", err)
	}
	return parsed.User.ID
}

func TestNewUserDefaultsSettingsShape(t *testing.T) {
	g, _ := testStoreGateway(t, "http://127.0.0.1:1")
	cookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	ids, body := getNewUserDefaults(t, g, cookie)
	if len(ids) != 0 || !strings.Contains(body, `"newUsers":{"disabledProviderIds":[]}`) {
		t.Fatalf("default newUsers must be an empty list, not null: %s", body)
	}
	// an explicit null in a PUT still comes back as []
	if rec := putSettings(t, g, cookie, `{"newUsers":{"disabledProviderIds":null}}`); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), `"disabledProviderIds":[]`) {
		t.Fatalf("PUT null ids: got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestNewUserDefaultsValidation(t *testing.T) {
	g, st := testStoreGateway(t, "http://127.0.0.1:1")
	cookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	a := addGenericProvider(t, st, "acme", "http://127.0.0.1:1", "k1")
	b := addGenericProvider(t, st, "bravo", "http://127.0.0.1:1", "k2")

	for _, bad := range []string{`[-1]`, `[0]`, fmt.Sprintf(`[%d,-3]`, a.ID)} {
		rec := putSettings(t, g, cookie, `{"newUsers":{"disabledProviderIds":`+bad+`}}`)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "positive integer") {
			t.Fatalf("PUT ids %s: got %d: %s", bad, rec.Code, rec.Body.String())
		}
	}
	if ids, _ := getNewUserDefaults(t, g, cookie); len(ids) != 0 {
		t.Fatalf("rejected PUTs changed the stored ids: %v", ids)
	}

	// duplicates collapse, order normalizes, unknown ids are dropped
	body := fmt.Sprintf(`{"newUsers":{"disabledProviderIds":[%d,%d,%d,999999]}}`, b.ID, a.ID, b.ID)
	rec := putSettings(t, g, cookie, body)
	if rec.Code != 200 {
		t.Fatalf("PUT ids: got %d: %s", rec.Code, rec.Body.String())
	}
	want := []int64{a.ID, b.ID}
	if ids, _ := getNewUserDefaults(t, g, cookie); !slices.Equal(ids, want) {
		t.Fatalf("stored ids = %v, want %v", ids, want)
	}
	stored, err := st.LoadRuntimeSettings()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !slices.Equal(stored.NewUsers.DisabledProviderIDs, want) {
		t.Fatalf("db ids = %v, want %v (unknown id must be pruned on save)", stored.NewUsers.DisabledProviderIDs, want)
	}

	// deleting a provider leaves its id in the doc; reads hide it and the
	// GUI's read-modify-write round trip still saves
	if err := st.DeleteProvider(a.ID); err != nil {
		t.Fatalf("delete provider: %v", err)
	}
	ids, raw := getNewUserDefaults(t, g, cookie)
	if !slices.Equal(ids, []int64{b.ID}) {
		t.Fatalf("ids after provider delete = %v, want [%d]", ids, b.ID)
	}
	var doc struct {
		Settings json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rec := putSettings(t, g, cookie, string(doc.Settings)); rec.Code != 200 {
		t.Fatalf("re-save after provider delete: got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestCreateUserAppliesNewUserDefaults: a regular user created after the
// setting is saved starts without the default-disabled providers — on the
// hot path too — while admins get no revocations, existing users are left
// alone, and a since-deleted provider id is skipped.
func TestCreateUserAppliesNewUserDefaults(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":[{"id":"m1"}]}`))
			return
		}
		sseOK(w)
	}))
	defer up.Close()
	g, st := testStoreGateway(t, up.URL)
	cookie := loginAs(t, g, "admin@example.com", "super-secret-pass")
	acme := addGenericProvider(t, st, "acme", up.URL, "k1")
	addGenericProvider(t, st, "bravo", up.URL, "k2")
	gone := addGenericProvider(t, st, "gone", up.URL, "k3")

	existing := insertPlainUser(t, st, "old@example.com", "old-user-pass-123", "user")

	body := fmt.Sprintf(`{"newUsers":{"disabledProviderIds":[%d,%d]}}`, acme.ID, gone.ID)
	if rec := putSettings(t, g, cookie, body); rec.Code != 200 {
		t.Fatalf("PUT settings: got %d: %s", rec.Code, rec.Body.String())
	}
	// the provider disappears after the save: its id stays in the stored doc
	if err := st.DeleteProvider(gone.ID); err != nil {
		t.Fatalf("delete provider: %v", err)
	}
	if rs, _ := st.LoadRuntimeSettings(); !slices.Contains(rs.NewUsers.DisabledProviderIDs, gone.ID) {
		t.Fatalf("test setup: stale id should still be stored, got %v", rs.NewUsers.DisabledProviderIDs)
	}

	userID := createUserReq(t, g, cookie, "new@example.com", "user")
	adminID := createUserReq(t, g, cookie, "newadmin@example.com", "admin")

	got, err := st.DisabledProviderIDs(userID)
	if err != nil {
		t.Fatalf("disabled ids: %v", err)
	}
	if len(got) != 1 || !got[acme.ID] {
		t.Fatalf("new user revocations = %v, want only acme (%d)", got, acme.ID)
	}
	all, err := st.AllProviderAccess()
	if err != nil {
		t.Fatalf("all access: %v", err)
	}
	if len(all[adminID]) != 0 {
		t.Fatalf("admin must get no revocations, got %v", all[adminID])
	}
	if len(all[existing.ID]) != 0 {
		t.Fatalf("existing user must be untouched, got %v", all[existing.ID])
	}
	if all[userID][gone.ID] {
		t.Fatal("deleted provider id must not produce a revocation")
	}

	// hot path: acme routes as unknown and is missing from /v1/models for
	// the new user; bravo still works
	key, _, err := st.CreateClientKey(userID, "k")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if err := g.rebuildPools(); err != nil { // client-key cache, as the key API does
		t.Fatalf("rebuildPools: %v", err)
	}
	chat := func(provider string) *httptest.ResponseRecorder {
		req := chatReq(provider, "m1")
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		g.clientOnly(g.handleChat)(rec, req)
		return rec
	}
	if rec := chat("acme"); rec.Code != 400 || !strings.Contains(rec.Body.String(), "Unknown provider") {
		t.Fatalf("chat to default-disabled provider: got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := chat("bravo"); rec.Code != 200 {
		t.Fatalf("chat to allowed provider: got %d: %s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	g.clientOnly(g.handleModels)(rec, req)
	ids := catalogIDs(t, rec.Body.String())
	if ids["acme/m1"] || !ids["bravo/m1"] {
		t.Fatalf("/v1/models for new user = %v, want bravo without acme", ids)
	}
}
