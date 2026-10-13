package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRoleRules pins who may change whom: superadmins manage everyone but
// themselves (including other superadmins); admins have the same admin
// routes but can't change superadmin accounts in any way, can't grant the
// superadmin role, and can't deactivate themselves either.
func TestRoleRules(t *testing.T) {
	g, st := testStoreGateway(t, "http://127.0.0.1:1")
	s1, _ := st.UserByEmail("admin@example.com")
	s2 := insertPlainUser(t, st, "s2@example.com", "s2-password-123", "superadmin")
	a1 := insertPlainUser(t, st, "a1@example.com", "a1-password-123", "admin")
	a2 := insertPlainUser(t, st, "a2@example.com", "a2-password-123", "admin")
	u := insertPlainUser(t, st, "u@example.com", "u-password-1234", "user")
	if err := g.rebuildPools(); err != nil {
		t.Fatal(err)
	}
	super := loginAs(t, g, "admin@example.com", "super-secret-pass")
	admin := loginAs(t, g, "a1@example.com", "a1-password-123")
	plain := loginAs(t, g, "u@example.com", "u-password-1234")

	call := func(cookie *http.Cookie, method, path string, id int64, body string, h http.HandlerFunc) int {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.SetPathValue("id", fmt.Sprint(id))
		req.SetPathValue("keyId", "999999")
		req.AddCookie(cookie)
		req.Header.Set("Origin", "https://gateway.example.com")
		rec := httptest.NewRecorder()
		g.requireSession(g.requireAdmin(h))(rec, req)
		return rec.Code
	}
	patchU := func(c *http.Cookie, id int64, body string) int {
		return call(c, "PATCH", "/api/users/x", id, body, g.handlePatchUser)
	}
	deleteU := func(c *http.Cookie, id int64) int {
		return call(c, "DELETE", "/api/users/x", id, "", g.handleDeleteUser)
	}
	createU := func(c *http.Cookie, body string) int {
		return call(c, "POST", "/api/users", 0, body, g.handleCreateUser)
	}
	expect := func(what string, got, want int) {
		t.Helper()
		if got != want {
			t.Errorf("%s: got %d, want %d", what, got, want)
		}
	}

	// superadmin over superadmin, never over themselves
	expect("super deactivates super", patchU(super, s2.ID, `{"disabled":true}`), 200)
	expect("super reactivates super", patchU(super, s2.ID, `{"disabled":false}`), 200)
	expect("super renames super", patchU(super, s2.ID, `{"name":"Second Super"}`), 200)
	expect("super demotes super to admin", patchU(super, s2.ID, `{"role":"admin"}`), 200)
	expect("super promotes back to superadmin", patchU(super, s2.ID, `{"role":"superadmin"}`), 200)
	expect("super resets super password", patchU(super, s2.ID, `{"password":"another-pass-123"}`), 200)
	expect("super deactivates self", patchU(super, s1.ID, `{"disabled":true}`), http.StatusConflict)
	expect("super deletes self", deleteU(super, s1.ID), http.StatusConflict)

	// admins can't change a superadmin account in any way
	expect("admin deactivates super", patchU(admin, s2.ID, `{"disabled":true}`), http.StatusForbidden)
	expect("admin deletes super", deleteU(admin, s2.ID), http.StatusForbidden)
	expect("admin renames super", patchU(admin, s2.ID, `{"name":"x"}`), http.StatusForbidden)
	expect("admin demotes super", patchU(admin, s2.ID, `{"role":"user"}`), http.StatusForbidden)
	expect("admin resets super password", patchU(admin, s2.ID, `{"password":"new-password-123"}`), http.StatusForbidden)
	expect("admin lists super keys", call(admin, "GET", "/api/users/x/keys", s2.ID, "", g.handleListUserKeys), 200)

	// ...and can't hand out the superadmin role
	expect("admin creates superadmin", createU(admin, `{"name":"n","email":"n@example.com","password":"n-password-123","role":"superadmin"}`), http.StatusForbidden)
	expect("admin promotes to superadmin", patchU(admin, u.ID, `{"role":"superadmin"}`), http.StatusForbidden)
	expect("bogus role", createU(super, `{"name":"n","email":"n@example.com","password":"n-password-123","role":"owner"}`), http.StatusBadRequest)

	// everything else is the same as a superadmin
	expect("admin creates admin", createU(admin, `{"name":"n","email":"n@example.com","password":"n-password-123","role":"admin"}`), http.StatusCreated)
	expect("admin promotes user to admin", patchU(admin, u.ID, `{"role":"admin"}`), 200)
	expect("admin demotes admin to user", patchU(admin, u.ID, `{"role":"user"}`), 200)
	expect("admin deactivates admin", patchU(admin, a2.ID, `{"disabled":true}`), 200)
	expect("admin deactivates self", patchU(admin, a1.ID, `{"disabled":true}`), http.StatusConflict)
	expect("admin deletes admin", deleteU(admin, a2.ID), 200)
	expect("admin reads settings", call(admin, "GET", "/api/settings", 0, "", g.handleGetSettings), 200)
	expect("user reads settings", call(plain, "GET", "/api/settings", 0, "", g.handleGetSettings), http.StatusForbidden)

	// a superadmin can delete another superadmin
	expect("super deletes super", deleteU(super, s2.ID), 200)
}

// TestOnlyOwnersManageKeys: no route lets anyone — even a superadmin —
// create, toggle or delete another user's client keys (only viewing them);
// a user still manages their own.
func TestOnlyOwnersManageKeys(t *testing.T) {
	g, st := testStoreGateway(t, "http://127.0.0.1:1")
	worker := insertPlainUser(t, st, "worker@example.com", "worker-pass-1234", "user")
	if _, _, err := st.CreateClientKey(worker.ID, "laptop"); err != nil {
		t.Fatal(err)
	}
	keys, _ := st.ListClientKeys(worker.ID)
	mux := http.NewServeMux()
	g.RegisterRoutes(mux, nil)
	send := func(cookie *http.Cookie, method, path, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(cookie)
		req.Header.Set("Origin", "https://gateway.example.com")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	super := loginAs(t, g, "admin@example.com", "super-secret-pass")
	base := fmt.Sprintf("/api/users/%d/keys", worker.ID)
	one := fmt.Sprintf("%s/%d", base, keys[0].ID)
	if code := send(super, "GET", base, ""); code != 200 {
		t.Fatalf("superadmin listing a user's keys: got %d", code)
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", base, `{"alias":"x"}`},
		{"PATCH", one, `{"disabled":true}`},
		{"DELETE", one, ""},
	} {
		if code := send(super, c.method, c.path, c.body); code != http.StatusMethodNotAllowed && code != http.StatusNotFound {
			t.Fatalf("superadmin %s %s: got %d, want 404/405", c.method, c.path, code)
		}
	}
	after, _ := st.ListClientKeys(worker.ID)
	if len(after) != 1 || after[0].Disabled {
		t.Fatalf("another user's keys were changed: %+v", after)
	}
	own := loginAs(t, g, "worker@example.com", "worker-pass-1234")
	if code := send(own, "POST", "/api/me/keys", `{"alias":"x"}`); code != http.StatusCreated {
		t.Fatalf("user creating their own key: got %d", code)
	}
	if code := send(own, "PATCH", fmt.Sprintf("/api/me/keys/%d", keys[0].ID), `{"disabled":true}`); code != 200 {
		t.Fatalf("user toggling their own key: got %d", code)
	}
}
