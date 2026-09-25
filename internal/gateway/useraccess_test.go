package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestUserProviderAccess walks the whole feature: a superadmin revokes a
// provider for one user, that user's requests route like an unknown prefix
// and the provider vanishes from their catalog (both /v1/models and the GUI's
// /api/models) while other users are untouched; re-granting restores
// everything.
func TestUserProviderAccess(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":[{"id":"m1","context_length":4096},{"id":"m2"}]}`))
			return
		}
		sseOK(w)
	}))
	defer up.Close()

	g, st := testStoreGateway(t, up.URL)
	prov := addGenericProvider(t, st, "acme", up.URL, "ak1")

	restricted := insertPlainUser(t, st, "restricted@example.com", "restricted-pass-123", "user")
	admin, err := st.UserByEmail("admin@example.com")
	if err != nil || admin == nil {
		t.Fatalf("admin user: %v", err)
	}
	restrictedKey, _, err := st.CreateClientKey(restricted.ID, "r")
	if err != nil {
		t.Fatalf("create restricted key: %v", err)
	}
	adminKey, _, err := st.CreateClientKey(admin.ID, "a")
	if err != nil {
		t.Fatalf("create admin key: %v", err)
	}
	if err := g.rebuildPools(); err != nil {
		t.Fatalf("rebuildPools: %v", err)
	}

	adminCookie := loginAs(t, g, "admin@example.com", "super-secret-pass")

	// PUT /api/users/{id}/providers/{providerId}/access as the superadmin
	setAccess := func(disabled bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PUT", "/api/users/x/providers/x/access",
			strings.NewReader(`{"disabled":`+strconv.FormatBool(disabled)+`}`))
		req.AddCookie(adminCookie)
		req.Header.Set("Origin", "https://gateway.example.com")
		req.SetPathValue("id", strconv.FormatInt(restricted.ID, 10))
		req.SetPathValue("providerId", strconv.FormatInt(prov.ID, 10))
		rec := httptest.NewRecorder()
		g.requireSession(g.requireSuperadmin(g.handleSetUserProviderAccess))(rec, req)
		return rec
	}

	// chat as one of the two client keys
	chat := func(key string) *httptest.ResponseRecorder {
		req := chatReq("acme", "m1")
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		g.clientOnly(g.handleChat)(rec, req)
		return rec
	}
	// /v1/models model ids for a client key
	v1Models := func(key string) map[string]bool {
		req := httptest.NewRequest("GET", "/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		g.clientOnly(g.handleModels)(rec, req)
		if rec.Code != 200 {
			t.Fatalf("/v1/models: got %d: %s", rec.Code, rec.Body.String())
		}
		return catalogIDs(t, rec.Body.String())
	}
	// /api/models model ids for a GUI session
	apiModels := func(cookie *http.Cookie) map[string]bool {
		req := httptest.NewRequest("GET", "/api/models", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		g.requireSession(g.handleAllModels)(rec, req)
		if rec.Code != 200 {
			t.Fatalf("/api/models: got %d: %s", rec.Code, rec.Body.String())
		}
		return catalogIDs(t, rec.Body.String())
	}

	// baseline: everything visible and routable for both users
	if rec := chat(restrictedKey); rec.Code != 200 {
		t.Fatalf("baseline chat: got %d: %s", rec.Code, rec.Body.String())
	}
	if !v1Models(restrictedKey)["acme/m1"] || !v1Models(adminKey)["acme/m1"] {
		t.Fatal("baseline catalogs should list acme for both users")
	}
	if !apiModels(adminCookie)["acme/m1"] {
		t.Fatal("baseline GUI catalog should list acme")
	}

	// revoke
	if rec := setAccess(true); rec.Code != 200 {
		t.Fatalf("revoke: got %d: %s", rec.Code, rec.Body.String())
	}

	// the restricted user's requests look like an unknown provider
	rec := chat(restrictedKey)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "Unknown provider") {
		t.Fatalf("revoked chat: got %d: %s", rec.Code, rec.Body.String())
	}
	if v1Models(restrictedKey)["acme/m1"] {
		t.Fatal("revoked provider still in the restricted user's /v1/models")
	}
	if !v1Models(adminKey)["acme/m1"] {
		t.Fatal("other users must keep the provider")
	}

	// GUI models page for the restricted user's own session
	userCookie := loginAs(t, g, "restricted@example.com", "restricted-pass-123")
	if apiModels(userCookie)["acme/m1"] {
		t.Fatal("revoked provider still in the restricted user's GUI catalog")
	}

	// direct store view stays consistent
	if got, err := st.DisabledProviderIDs(restricted.ID); err != nil || !got[prov.ID] {
		t.Fatalf("DisabledProviderIDs = %v, %v", got, err)
	}

	// re-grant
	if rec := setAccess(false); rec.Code != 200 {
		t.Fatalf("re-grant: got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := chat(restrictedKey); rec.Code != 200 {
		t.Fatalf("restored chat: got %d: %s", rec.Code, rec.Body.String())
	}
	if !v1Models(restrictedKey)["acme/m1"] {
		t.Fatal("restored catalog should list acme again")
	}

	// unknown provider id -> 404
	req := httptest.NewRequest("PUT", "/api/users/x/providers/x/access", strings.NewReader(`{"disabled":true}`))
	req.AddCookie(adminCookie)
	req.Header.Set("Origin", "https://gateway.example.com")
	req.SetPathValue("id", strconv.FormatInt(restricted.ID, 10))
	req.SetPathValue("providerId", "99999")
	rec = httptest.NewRecorder()
	g.requireSession(g.requireSuperadmin(g.handleSetUserProviderAccess))(rec, req)
	if rec.Code != 404 {
		t.Fatalf("unknown provider: got %d, want 404", rec.Code)
	}

	// plain users cannot read the access listing
	userCookie = loginAs(t, g, "restricted@example.com", "restricted-pass-123")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/users/x/providers", nil)
	req.AddCookie(userCookie)
	req.SetPathValue("id", strconv.FormatInt(restricted.ID, 10))
	g.requireSession(g.requireSuperadmin(g.handleListUserProviders))(rec, req)
	if rec.Code != 403 {
		t.Fatalf("plain user list: got %d, want 403", rec.Code)
	}

	// the superadmin listing reflects the re-granted state
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/users/x/providers", nil)
	req.AddCookie(adminCookie)
	req.SetPathValue("id", strconv.FormatInt(restricted.ID, 10))
	g.requireSession(g.requireSuperadmin(g.handleListUserProviders))(rec, req)
	if rec.Code != 200 {
		t.Fatalf("list: got %d: %s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Providers []struct {
			ID              int64  `json:"id"`
			Name            string `json:"name"`
			DisabledForUser bool   `json:"disabledForUser"`
		} `json:"providers"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, p := range listed.Providers {
		if p.ID == prov.ID {
			found = true
			if p.DisabledForUser {
				t.Error("provider still marked disabled after re-grant")
			}
		}
	}
	if !found {
		t.Error("acme missing from the user provider listing")
	}
}

// catalogIDs pulls the model id set out of a /v1/models-style body.
func catalogIDs(t *testing.T, body string) map[string]bool {
	t.Helper()
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("decode catalog: %v (%s)", err, body)
	}
	out := map[string]bool{}
	for _, d := range parsed.Data {
		out[d.ID] = true
	}
	return out
}
