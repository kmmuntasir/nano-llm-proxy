package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pi / oh-my-pi send the instruction message as role:"developer" whenever the
// catalog says the model reasons. Most upstreams shrug that off, but zen's
// free tier sits behind an "airlock" layer that validates the role enum and
// answers 400 "[airlock_error] unknown variant `developer`" — which the client
// surfaces as a broken model. opencode never sends the role, which is why the
// model works there and not here.

// strictRoleZen is a fake zen upstream that enforces the same role enum as the
// real airlock layer, and records the body it was handed.
func strictRoleZen(t *testing.T, got chan<- map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if got != nil {
			select {
			case got <- body:
			default:
			}
		}
		allowed := map[string]bool{"system": true, "user": true, "assistant": true, "tool": true}
		for _, field := range []string{"messages", "input"} {
			items, _ := body[field].([]any)
			for _, it := range items {
				m, _ := it.(map[string]any)
				role, _ := m["role"].(string)
				if role != "" && !allowed[role] {
					w.WriteHeader(http.StatusBadRequest)
					w.Write([]byte(`{"error":{"message":"[airlock_error] invalid request: unknown variant ` +
						role + `, expected one of ` + "`system`" + `, ` + "`user`" + `, ` + "`assistant`" + `, ` + "`tool`" + `"}}`))
					return
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"ok"}}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNormalizeInstructionRoles(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "developer", "content": "sys"},
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "assistant", "content": ""},
			map[string]any{"role": "tool", "content": "res"},
			"not an object",
		},
		"input": []any{map[string]any{"role": "developer", "content": "sys"}},
	}
	if !normalizeInstructionRoles(body) {
		t.Fatal("expected a change")
	}
	msgs := body["messages"].([]any)
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Errorf("developer not renamed: %v", msgs[0])
	}
	// order and every other role are untouched — a rename, not a rewrite
	for i, want := range []string{"system", "user", "assistant", "tool"} {
		if got, _ := msgs[i].(map[string]any)["role"].(string); got != want {
			t.Errorf("message %d role = %q, want %q", i, got, want)
		}
	}
	if body["input"].([]any)[0].(map[string]any)["role"] != "system" {
		t.Errorf("responses input item not renamed: %v", body["input"])
	}
	if normalizeInstructionRoles(body) {
		t.Error("second pass should be a no-op (idempotent)")
	}
	if normalizeInstructionRoles(map[string]any{"model": "x"}) {
		t.Error("body without messages must report no change")
	}
}

// A client that sends "developer" to a strict zen model must be served, with
// the role renamed on the wire — the regression behind "fledge-alpha-free
// works in opencode but not through the gateway".
func TestZenChatRenamesDeveloperRoleForStrictUpstream(t *testing.T) {
	seen := make(chan map[string]any, 1)
	up := strictRoleZen(t, seen)
	g := testGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.Write([]byte(`{"data":[{"id":"fledge-alpha-free"}]}`))
			return
		}
		up.Config.Handler.ServeHTTP(w, r)
	}))

	body := `{"model":"zen/fledge-alpha-free","messages":[
		{"role":"developer","content":"be brief"},{"role":"user","content":"say ok"}],"stream":true}`
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	g.handleChat(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("strict zen model rejected a developer-role request: %d: %s",
			rec.Code, rec.Body.String())
	}
	select {
	case sent := <-seen:
		msgs := sent["messages"].([]any)
		if role, _ := msgs[0].(map[string]any)["role"].(string); role != "system" {
			t.Errorf("upstream saw role %q, want system", role)
		}
		if role, _ := msgs[1].(map[string]any)["role"].(string); role != "user" {
			t.Errorf("second message role changed: %q", role)
		}
	default:
		t.Fatal("upstream never received a request")
	}
}

// Same shim on the Responses surface, where input items carry the role.
func TestZenResponsesRenamesDeveloperRole(t *testing.T) {
	seen := make(chan map[string]any, 1)
	strict := strictRoleZen(t, seen)
	g := testGateway(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		strict.Config.Handler.ServeHTTP(w, r)
	}))

	body := `{"model":"zen/muse-test-free","input":[
		{"role":"developer","content":"be brief"},{"role":"user","content":"say ok"}],"stream":true}`
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	g.handleResponses(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("strict zen responses model rejected a developer-role request: %d: %s",
			rec.Code, rec.Body.String())
	}
	select {
	case sent := <-seen:
		input := sent["input"].([]any)
		if role, _ := input[0].(map[string]any)["role"].(string); role != "system" {
			t.Errorf("upstream saw input role %q, want system", role)
		}
	default:
		t.Fatal("upstream never received a request")
	}
}
