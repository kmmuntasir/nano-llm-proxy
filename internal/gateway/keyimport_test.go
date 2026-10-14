package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

type keyImportReply struct {
	Added   int               `json:"added"`
	Results []keyImportResult `json:"results"`
}

// importFixture is a store-backed gateway with one generic provider holding
// one existing key, reached through the real mux as the bootstrap admin.
type importFixture struct {
	g      *gateway
	st     *store.Store
	mux    *http.ServeMux
	p      *store.Provider
	cookie *http.Cookie
}

func newImportFixture(t *testing.T) *importFixture {
	t.Helper()
	g, st := testStoreGateway(t, "http://127.0.0.1:1")
	p := addGenericProvider(t, st, "acme", "http://127.0.0.1:1", "sk-existing")
	if err := st.UpdateProviderKey(mustKeys(t, st, p.ID)[0].ID, strPtr("old"), nil); err != nil {
		t.Fatal(err)
	}
	if err := g.rebuildPools(); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	g.RegisterRoutes(mux, nil)
	return &importFixture{g: g, st: st, mux: mux, p: p, cookie: loginAs(t, g, "admin@example.com", "super-secret-pass")}
}

func strPtr(s string) *string { return &s }

func mustKeys(t *testing.T, st *store.Store, providerID int64) []store.ProviderKey {
	t.Helper()
	keys, err := st.ListProviderKeys(providerID)
	if err != nil {
		t.Fatalf("ListProviderKeys: %v", err)
	}
	return keys
}

// post sends {"content": content} to the import route.
func (f *importFixture) post(t *testing.T, cookie *http.Cookie, providerID int64, content string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"content": content})
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/providers/%d/keys/import", providerID), strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://gateway.example.com")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func (f *importFixture) ok(t *testing.T, content string) keyImportReply {
	t.Helper()
	rec := f.post(t, f.cookie, f.p.ID, content)
	if rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	var out keyImportReply
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func (f *importFixture) labels(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, k := range mustKeys(t, f.st, f.p.ID) {
		out = append(out, k.Label)
	}
	return out
}

// poolLabels reads the live pool for the fixture's provider.
func (f *importFixture) poolLabels() []string {
	f.g.regMu.RLock()
	defer f.g.regMu.RUnlock()
	for _, ref := range f.g.registry {
		if ref.name == f.p.Name {
			var out []string
			for _, ks := range ref.pool.rawKeys() {
				out = append(out, ks.Label)
			}
			return out
		}
	}
	return nil
}

func TestKeyImportPreservesFileOrder(t *testing.T) {
	f := newImportFixture(t)
	out := f.ok(t, "{\n  \"zeta\": \"sk-z\",\n  \"alpha\": \" sk-a\\n\",\n  \"mid\": \"sk-m\"\n}\n")
	if out.Added != 3 {
		t.Fatalf("added = %d, want 3: %+v", out.Added, out.Results)
	}
	for i, want := range []string{"zeta", "alpha", "mid"} {
		if r := out.Results[i]; r.Label != want || r.Status != keyImportAdded || r.Message != "" {
			t.Errorf("result %d = %+v, want %s added", i, r, want)
		}
	}
	keys := mustKeys(t, f.st, f.p.ID)
	if got, want := f.labels(t), []string{"old", "zeta", "alpha", "mid"}; !slices.Equal(got, want) {
		t.Fatalf("stored order = %v, want %v", got, want)
	}
	for i, k := range keys {
		if k.SortOrder != int64(i) {
			t.Errorf("key %s sort_order = %d, want %d", k.Label, k.SortOrder, i)
		}
	}
	if keys[2].Key != "sk-a" {
		t.Errorf("imported key not trimmed")
	}
	// applyMutation rebuilt the pool in the same request
	if got, want := f.poolLabels(), []string{"old", "zeta", "alpha", "mid"}; !slices.Equal(got, want) {
		t.Fatalf("pool = %v, want %v", got, want)
	}
}

func TestKeyImportWholeFileRejections(t *testing.T) {
	f := newImportFixture(t)
	var big strings.Builder
	big.WriteString("{")
	for i := range maxImportKeys + 1 {
		if i > 0 {
			big.WriteString(",")
		}
		fmt.Fprintf(&big, `"k%d":"sk-%d"`, i, i)
	}
	big.WriteString("}")
	var checks atomic.Int32
	f.g.keyCheck = func(context.Context, string, string, string) error { checks.Add(1); return nil }
	cases := []struct{ name, content, want string }{
		{"not JSON", `{"a": "sk-1",`, "isn't valid JSON"},
		{"empty content", ``, "The file is empty"},
		{"array", `["sk-1"]`, `one JSON object`},
		{"string", `"sk-1"`, `one JSON object`},
		{"non-string value", `{"a": "sk-1", "b": 42}`, `The value for \"b\" must be a string`},
		{"object value", `{"a": {"key": "sk-1"}}`, `The value for \"a\" must be a string`},
		{"null value", `{"a": null}`, `must be a string`},
		{"trailing data", `{"a": "sk-1"} {"b": "sk-2"}`, "data after the JSON object"},
		{"trailing garbage", `{"a": "sk-1"} x`, "data after the JSON object"},
		{"duplicate alias", `{"work-1": "sk-1", "work-2": "sk-2", "work-1": "sk-3"}`, `Alias \"work-1\" appears twice in the file`},
		{"empty object", ` {} `, "The file has no keys"},
		{"too many", big.String(), "At most 200 keys per import"},
	}
	for _, c := range cases {
		rec := f.post(t, f.cookie, f.p.ID, c.content)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s: %d %s, want 400 containing %q", c.name, rec.Code, rec.Body.String(), c.want)
		}
		if strings.Contains(rec.Body.String(), "sk-") {
			t.Errorf("%s: response echoes key material: %s", c.name, rec.Body.String())
		}
	}
	if got := f.labels(t); !slices.Equal(got, []string{"old"}) {
		t.Fatalf("rejected files saved keys: %v", got)
	}
	if n := checks.Load(); n != 0 {
		t.Fatalf("rejected files ran %d key checks", n)
	}
	// whitespace around one object is fine; exactly the cap is fine
	if out := f.ok(t, "\n {\"a\": \"sk-1\"} \n"); out.Added != 1 {
		t.Fatalf("padded object: %+v", out)
	}
	atCap := strings.Replace(big.String(), fmt.Sprintf(`,"k%d":"sk-%d"`, maxImportKeys, maxImportKeys), "", 1)
	if out := f.ok(t, atCap); out.Added != maxImportKeys-1 || len(out.Results) != maxImportKeys {
		t.Fatalf("import at the cap: added %d of %d", out.Added, len(out.Results))
	}
}

func TestKeyImportUnknownProvider(t *testing.T) {
	f := newImportFixture(t)
	rec := f.post(t, f.cookie, 999999, `{"a": "sk-1"}`)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "no longer exists") {
		t.Fatalf("unknown provider: %d %s", rec.Code, rec.Body.String())
	}
}

func TestKeyImportPerEntryDuplicatesAndRejections(t *testing.T) {
	f := newImportFixture(t)
	long := strings.Repeat("x", maxKeyLabelLen+1)
	out := f.ok(t, `{"a": "sk-1", "b": "  ", "`+long+`": "sk-2", "c": "sk-1", "d": "sk-existing", "e": "sk-3", "  ": "sk-4"}`)
	want := []struct{ label, status, msg string }{
		{"a", keyImportAdded, ""},
		{"b", keyImportRejected, "The key is empty"},
		{long, keyImportRejected, "1-64 characters"},
		{"c", keyImportDuplicate, `Same key as "a" in this file`},
		{"d", keyImportDuplicate, `Already on this provider as "old"`},
		{"e", keyImportAdded, ""},
		{"  ", keyImportRejected, "1-64 characters"},
	}
	if len(out.Results) != len(want) {
		t.Fatalf("results = %+v", out.Results)
	}
	for i, w := range want {
		r := out.Results[i]
		if r.Label != w.label || r.Status != w.status || !strings.Contains(r.Message, w.msg) || (w.msg == "" && r.Message != "") {
			t.Errorf("result %d = %+v, want %s %s %q", i, r, w.label, w.status, w.msg)
		}
	}
	if out.Added != 2 {
		t.Errorf("added = %d, want 2", out.Added)
	}
	if got := f.labels(t); !slices.Equal(got, []string{"old", "a", "e"}) {
		t.Fatalf("stored = %v", got)
	}
}

// TestKeyImportMixedVerdicts: invalid, refused and unverifiable keys are
// reported with the single add's wording; only the good ones are saved.
func TestKeyImportMixedVerdicts(t *testing.T) {
	f := newImportFixture(t)
	f.g.keyCheck = func(_ context.Context, _, _, key string) error {
		switch key {
		case "sk-bad":
			return &keyRejectedError{surface: "OpenAI", status: 401, detail: "bad key <key>"}
		case "sk-blocked":
			return &keyRejectedError{surface: "OpenAI", status: 403, detail: "region", refused: true}
		case "sk-flaky":
			return errors.New("OpenAI endpoint answered HTTP 503")
		}
		return nil
	}
	out := f.ok(t, `{"good-1": "sk-good-1", "bad": "sk-bad", "blocked": "sk-blocked", "flaky": "sk-flaky", "good-2": "sk-good-2"}`)
	want := []struct{ status, msg string }{
		{keyImportAdded, ""},
		{keyCheckInvalid, `Key "bad" is invalid — acme's OpenAI endpoint answered HTTP 401: bad key <key>`},
		{keyCheckRefused, `Key "blocked" was refused — acme's OpenAI endpoint answered HTTP 403: region. The key may be genuine`},
		{keyCheckUnverifiable, `Couldn't verify Key "flaky" against acme — OpenAI endpoint answered HTTP 503. Nothing was saved; try again.`},
		{keyImportAdded, ""},
	}
	for i, w := range want {
		r := out.Results[i]
		if r.Status != w.status || !strings.HasPrefix(r.Message, w.msg) || (w.msg == "" && r.Message != "") {
			t.Errorf("result %d = %+v, want %s %q", i, r, w.status, w.msg)
		}
		if strings.Contains(r.Message, "sk-") {
			t.Errorf("result %d leaks key material: %q", i, r.Message)
		}
	}
	if out.Added != 2 {
		t.Errorf("added = %d, want 2", out.Added)
	}
	if got := f.labels(t); !slices.Equal(got, []string{"old", "good-1", "good-2"}) {
		t.Fatalf("stored = %v", got)
	}
	if got := f.poolLabels(); !slices.Equal(got, []string{"old", "good-1", "good-2"}) {
		t.Fatalf("pool = %v", got)
	}

	// nothing passing: 200 with added 0 and nothing saved
	out = f.ok(t, `{"bad-again": "sk-bad"}`)
	if out.Added != 0 || out.Results[0].Status != keyCheckInvalid {
		t.Fatalf("all-bad import: %+v", out)
	}
	if got := f.labels(t); len(got) != 3 {
		t.Fatalf("all-bad import saved keys: %v", got)
	}
}

// TestKeyImportRealVerifier runs the production probes against a scripted
// upstream: verdicts and key redaction come through end to end.
func TestKeyImportRealVerifier(t *testing.T) {
	f := newImportFixture(t)
	up := fakeUpstream{publicModels: true, okChatStatus: 402}.server(t)
	if err := f.st.UpdateProvider(f.p.ID, nil, &up.URL, nil, nil); err != nil {
		t.Fatal(err)
	}
	f.g.keyCheck = f.g.verifyUpstreamKey
	out := f.ok(t, `{"ok": "sk-good-imported", "nope": "sk-wrong-secret-77"}`)
	if out.Results[0].Status != keyImportAdded || out.Results[1].Status != keyCheckInvalid {
		t.Fatalf("results = %+v", out.Results)
	}
	if strings.Contains(out.Results[1].Message, "sk-wrong-secret-77") || !strings.Contains(out.Results[1].Message, "<key>") {
		t.Fatalf("echoed key not redacted: %q", out.Results[1].Message)
	}
}

// TestKeyImportConcurrencyCap: verifications overlap, but never more than
// keyImportParallel at once.
func TestKeyImportConcurrencyCap(t *testing.T) {
	f := newImportFixture(t)
	const n = 10
	var inFlight, peak atomic.Int32
	started := make(chan struct{}, n)
	gate := make(chan struct{})
	f.g.keyCheck = func(ctx context.Context, _, _, _ string) error {
		cur := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}
	var entries []string
	for i := range n {
		entries = append(entries, fmt.Sprintf(`"k%d": "sk-%d"`, i, i))
	}
	var wg sync.WaitGroup
	var out keyImportReply
	var rec *httptest.ResponseRecorder
	wg.Add(1)
	go func() {
		defer wg.Done()
		rec = f.post(t, f.cookie, f.p.ID, "{"+strings.Join(entries, ",")+"}")
	}()
	// all four slots fill before any check is allowed to finish
	for range keyImportParallel {
		<-started
	}
	close(gate)
	wg.Wait()
	if rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Added != n {
		t.Fatalf("added = %d, want %d", out.Added, n)
	}
	if p := peak.Load(); p != keyImportParallel {
		t.Fatalf("peak in-flight checks = %d, want %d", p, keyImportParallel)
	}
}

// TestKeyImportCancelledSavesNothing: a client that goes away mid-import
// gets nothing saved.
func TestKeyImportCancelledSavesNothing(t *testing.T) {
	f := newImportFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.g.keyCheck = func(context.Context, string, string, string) error {
		cancel()
		return nil
	}
	body := `{"content": "{\"a\": \"sk-1\", \"b\": \"sk-2\"}"}`
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/providers/%d/keys/import", f.p.ID), strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Origin", "https://gateway.example.com")
	req.AddCookie(f.cookie)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("cancelled import answered 200: %s", rec.Body.String())
	}
	if got := f.labels(t); !slices.Equal(got, []string{"old"}) {
		t.Fatalf("cancelled import saved keys: %v", got)
	}
}

func TestKeyImportAdminOnly(t *testing.T) {
	f := newImportFixture(t)
	insertPlainUser(t, f.st, "u@example.com", "u-password-1234", "user")
	plain := loginAs(t, f.g, "u@example.com", "u-password-1234")
	if rec := f.post(t, plain, f.p.ID, `{"a": "sk-1"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("regular user import: %d %s", rec.Code, rec.Body.String())
	}
	// mutating /api/* is Origin-checked
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/providers/%d/keys/import", f.p.ID), strings.NewReader(`{"content": "{\"a\": \"sk-1\"}"}`))
	req.Header.Set("Origin", "https://evil.example.net")
	req.AddCookie(f.cookie)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign Origin: %d %s", rec.Code, rec.Body.String())
	}
	if got := f.labels(t); !slices.Equal(got, []string{"old"}) {
		t.Fatalf("refused imports saved keys: %v", got)
	}
}
