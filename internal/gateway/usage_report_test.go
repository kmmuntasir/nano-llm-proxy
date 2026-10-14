package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kmmuntasir/nano-llm-proxy/internal/store"
)

// --- capture (migration V6 columns) ---

// keyedUpstream answers per upstream key: sk-zen-1 (key-a) with failStatus,
// sk-zen-2 (key-b) with okStatus (200 = a streamed completion with usage).
func keyedUpstream(t *testing.T, failStatus, okStatus int) *httptest.Server {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := okStatus
		if r.Header.Get("Authorization") == "Bearer sk-zen-1" {
			status = failStatus
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			w.Write([]byte(`{"error":"boom"}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"id\":\"x\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(up.Close)
	return up
}

func TestUsageCaptureV6DetailOnSuccess(t *testing.T) {
	up := keyedUpstream(t, http.StatusInternalServerError, http.StatusOK)
	g, st := testStoreGateway(t, up.URL)
	admin, _ := st.UserByEmail("admin@example.com")
	ck := newClientKey(t, g, st, admin.ID, "laptop")

	rec := httptest.NewRecorder()
	req := chatReq("zen", "mimo-test-free") // stream:true
	req.Header.Set("Authorization", "Bearer "+ck)
	g.clientOnly(g.handleChat)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("chat: got %d: %s", rec.Code, rec.Body.String())
	}
	events := g.usageBuf.drain()
	if len(events) != 1 {
		t.Fatalf("drained %d events, want 1", len(events))
	}
	e := events[0]
	if e.Surface != "chat" || !e.Stream || e.Status != 200 || e.FailReason != "" {
		t.Errorf("surface/stream/status wrong: %+v", e)
	}
	// key-a 500s (priority rotation tries it first), key-b serves
	if e.Attempts != 2 || e.UpstreamStatus != 200 || e.UpstreamKey != "key-b" {
		t.Errorf("attempts/upstream wrong: %+v", e)
	}
	if e.TtfbMs <= 0 || e.TtfbMs > e.DurationMs+1 {
		t.Errorf("ttfb = %d (duration %d), want 0 < ttfb <= duration", e.TtfbMs, e.DurationMs)
	}
	if e.UserEmail != "admin@example.com" || e.KeyAlias != "laptop" {
		t.Errorf("snapshots wrong: email=%q alias=%q", e.UserEmail, e.KeyAlias)
	}
}

func TestUsageCaptureV6DetailWhenAllKeysFail(t *testing.T) {
	up := keyedUpstream(t, http.StatusInternalServerError, http.StatusServiceUnavailable)
	g, st := testStoreGateway(t, up.URL)
	admin, _ := st.UserByEmail("admin@example.com")
	ck := newClientKey(t, g, st, admin.ID, "laptop")

	rec := httptest.NewRecorder()
	body := `{"model":"zen/mimo-test-free","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+ck)
	g.clientOnly(g.handleChat)(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("chat: got %d, want 503: %s", rec.Code, rec.Body.String())
	}
	var errBody struct {
		Error struct{ Message string } `json:"error"`
	}
	json.Unmarshal(rec.Body.Bytes(), &errBody)

	events := g.usageBuf.drain()
	if len(events) != 1 {
		t.Fatalf("drained %d events, want 1", len(events))
	}
	e := events[0]
	if e.Status != http.StatusServiceUnavailable || e.Surface != "chat" || e.Stream {
		t.Errorf("status/surface/stream wrong: %+v", e)
	}
	if e.FailReason == "" || e.FailReason != errBody.Error.Message {
		t.Errorf("fail reason %q, want the client hint %q", e.FailReason, errBody.Error.Message)
	}
	if e.Attempts != 2 || e.UpstreamStatus != http.StatusServiceUnavailable || e.UpstreamKey != "key-b" || e.TtfbMs != 0 {
		t.Errorf("upstream detail wrong: %+v", e)
	}
	if e.UserEmail != "admin@example.com" || e.KeyAlias != "laptop" {
		t.Errorf("snapshots wrong: %+v", e)
	}
}

func TestUsageCapturePreRoutingClientErrors(t *testing.T) {
	g, st := testStoreGateway(t, "http://127.0.0.1:1") // upstream never reached
	admin, _ := st.UserByEmail("admin@example.com")
	ck := newClientKey(t, g, st, admin.ID, "laptop")

	for _, tc := range []struct {
		name, model, provider, wantModel, wantReason string
	}{
		{"bad model string", "no-slash", "", "no-slash", "Model must be"},
		{"unknown provider", "nope/x", "nope", "x", "Unknown provider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			body := fmt.Sprintf(`{"model":%q,"messages":[],"stream":true}`, tc.model)
			req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
			req.Header.Set("x-api-key", ck)
			g.clientOnly(g.handleMessages)(rec, req)
			if rec.Code != 400 {
				t.Fatalf("got %d, want 400", rec.Code)
			}
			events := g.usageBuf.drain()
			if len(events) != 1 {
				t.Fatalf("drained %d events, want 1", len(events))
			}
			e := events[0]
			if e.Status != 400 || e.Provider != tc.provider || e.Model != tc.wantModel ||
				e.Surface != "messages" || !e.Stream || e.Attempts != 0 ||
				!strings.Contains(e.FailReason, tc.wantReason) || e.UserID != admin.ID {
				t.Errorf("event wrong: %+v", e)
			}
		})
	}
}

func TestTruncateReason(t *testing.T) {
	if got := truncateReason("short"); got != "short" {
		t.Errorf("short reason changed: %q", got)
	}
	long := strings.Repeat("é", 400) // 2 bytes per rune
	got := truncateReason(long)
	if n := len([]rune(got)); n != maxFailReason {
		t.Errorf("truncated to %d runes, want %d", n, maxFailReason)
	}
	if !strings.HasPrefix(long, got) || strings.ContainsRune(got, '�') {
		t.Errorf("truncation split a rune")
	}
}

// --- dashboard scoping ---

func TestDashboardScopesActivityForPlainUsers(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sseOK(w) }))
	t.Cleanup(up.Close)
	g, st := testStoreGateway(t, up.URL)
	admin, _ := st.UserByEmail("admin@example.com")
	insertPlainUser(t, st, "pi@example.com", "pi-password-123", "user")
	pi, _ := st.UserByEmail("pi@example.com")
	for _, ck := range []string{newClientKey(t, g, st, admin.ID, "admin-key"), newClientKey(t, g, st, pi.ID, "pi-key")} {
		rec := httptest.NewRecorder()
		req := chatReq("zen", "mimo-test-free")
		req.Header.Set("Authorization", "Bearer "+ck)
		g.clientOnly(g.handleChat)(rec, req)
		if rec.Code != 200 {
			t.Fatalf("chat: %d", rec.Code)
		}
	}

	type dash struct {
		Providers []struct {
			Total int               `json:"total"`
			Keys  []json.RawMessage `json:"keys"`
		} `json:"providers"`
		Activity []map[string]any `json:"activity"`
	}
	get := func(cookie *http.Cookie) (dash, string) {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/dashboard", nil)
		req.AddCookie(cookie)
		g.requireSession(g.handleDashboard)(rec, req)
		if rec.Code != 200 {
			t.Fatalf("dashboard: %d %s", rec.Code, rec.Body.String())
		}
		var d dash
		if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return d, rec.Body.String()
	}

	d, raw := get(loginAs(t, g, "pi@example.com", "pi-password-123"))
	if len(d.Activity) != 1 || d.Activity[0]["user"] != "pi@example.com" {
		t.Fatalf("plain user activity not scoped: %s", raw)
	}
	if h, _ := d.Activity[0]["keyHash"].(string); h != "" {
		t.Errorf("plain user sees upstream key hash %q", h)
	}
	if strings.Contains(raw, "admin@example.com") || strings.Contains(raw, "admin-key") {
		t.Errorf("dashboard leaks another user's activity: %s", raw)
	}
	for _, p := range d.Providers {
		if len(p.Keys) != 0 {
			t.Errorf("plain user sees pool key views: %s", raw)
		}
		if p.Total == 0 {
			t.Errorf("health counts should stay visible: %s", raw)
		}
	}

	d, raw = get(loginAs(t, g, "admin@example.com", "super-secret-pass"))
	if len(d.Activity) != 2 {
		t.Fatalf("superadmin should see all activity: %s", raw)
	}
	if h, _ := d.Activity[0]["keyHash"].(string); h == "" {
		t.Errorf("superadmin lost the upstream key hash: %s", raw)
	}
	if len(d.Providers) == 0 || len(d.Providers[0].Keys) == 0 {
		t.Errorf("superadmin lost pool key views: %s", raw)
	}
}

// --- v2 usage endpoints ---

// usageFixture seeds a deterministic usage log:
//
//	admin / admin-key: zen/big-pickle chat ok (+100), zen/big-pickle chat 502 (+3700),
//	                   kilo/m2 messages ok (+7300)
//	pi    / pi-key:    zen/big-pickle responses ok (+200)
//	user #999 (deleted, no snapshots) / key #888: zen/ghost chat ok (+300)
type usageFixture struct {
	g            *gateway
	st           *store.Store
	day0         int64
	admin, pi    *store.User
	adminKeyID   int64
	piKeyID      int64
	adminCookie  *http.Cookie
	piCookie     *http.Cookie
	fullRangeQry string
}

func newUsageFixture(t *testing.T) *usageFixture {
	t.Helper()
	g, st := testStoreGateway(t, "http://127.0.0.1:1")
	admin, _ := st.UserByEmail("admin@example.com")
	insertPlainUser(t, st, "pi@example.com", "pi-password-123", "user")
	pi, _ := st.UserByEmail("pi@example.com")
	_, ak, err := st.CreateClientKey(admin.ID, "admin-key")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	_, pk, err := st.CreateClientKey(pi.ID, "pi-key")
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	day0 := int64(1790000000)
	day0 -= day0 % 86400
	ev := func(off, user, key int64, prov, model, surface string, status int, in, out, dur, ttfb int64, upKey string) store.UsageEvent {
		return store.UsageEvent{
			TS: day0 + off, UserID: user, ClientKeyID: key, Provider: prov, Model: model,
			InputTokens: in, OutputTokens: out, Status: status, DurationMs: dur,
			Surface: surface, Stream: surface == "chat", UpstreamStatus: status, UpstreamKey: upKey,
			TtfbMs: ttfb, Attempts: 1,
		}
	}
	failed := ev(3700, admin.ID, ak.ID, "zen", "big-pickle", "chat", 502, 0, 0, 300, 0, "key-b")
	failed.FailReason = "No healthy zen keys available"
	failed.UpstreamStatus = 503
	if err := st.InsertUsageEvents([]store.UsageEvent{
		ev(100, admin.ID, ak.ID, "zen", "big-pickle", "chat", 200, 10, 5, 100, 50, "key-a"),
		failed,
		ev(7300, admin.ID, ak.ID, "kilo", "m2", "messages", 200, 100, 50, 200, 0, "a"),
		ev(200, pi.ID, pk.ID, "zen", "big-pickle", "responses", 200, 1, 1, 50, 20, "key-a"),
		ev(300, 999, 888, "zen", "ghost", "chat", 200, 2, 2, 80, 0, "key-a"),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return &usageFixture{
		g: g, st: st, day0: day0, admin: admin, pi: pi, adminKeyID: ak.ID, piKeyID: pk.ID,
		adminCookie:  loginAs(t, g, "admin@example.com", "super-secret-pass"),
		piCookie:     loginAs(t, g, "pi@example.com", "pi-password-123"),
		fullRangeQry: fmt.Sprintf("from=%d&to=%d", day0, day0+4*3600),
	}
}

// get calls one usage handler through requireSession and decodes the body.
func (f *usageFixture) get(t *testing.T, h http.HandlerFunc, cookie *http.Cookie, path string, out any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", path, nil)
	req.AddCookie(cookie)
	f.g.requireSession(h)(rec, req)
	if out != nil && rec.Code == 200 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v: %s", path, err, rec.Body.String())
		}
	}
	if rec.Code != 200 && !strings.Contains(rec.Body.String(), `"error":{"message":`) {
		t.Errorf("%s: %d without the standard error shape: %s", path, rec.Code, rec.Body.String())
	}
	return rec.Code
}

type usageRangeJSON struct{ From, To int64 }

func TestUsageOverviewEndpoint(t *testing.T) {
	f := newUsageFixture(t)
	var ov struct {
		Range  usageRangeJSON
		Bucket string
		Totals store.UsageOverviewTotals
		Points []store.UsageOverviewPoint
	}
	if code := f.get(t, f.g.handleUsageOverview, f.adminCookie, "/api/usage/overview?"+f.fullRangeQry, &ov); code != 200 {
		t.Fatalf("overview: %d", code)
	}
	if ov.Range.From != f.day0 || ov.Range.To != f.day0+4*3600 || ov.Bucket != "hour" {
		t.Errorf("range/bucket echo wrong: %+v %s", ov.Range, ov.Bucket)
	}
	want := store.UsageOverviewTotals{
		Requests: 5, Errors: 1, InputTokens: 113, OutputTokens: 58,
		AvgDurationMs: 146, // (100+300+200+50+80)/5
		P95DurationMs: 200, // nearest rank over ok rows {50,80,100,200}
		AvgTtfbMs:     35,  // (50+20)/2 — zero ttfb rows excluded
		Users:         3, Models: 3,
	}
	if ov.Totals != want {
		t.Errorf("totals = %+v, want %+v", ov.Totals, want)
	}
	// zero-filled hourly buckets day0 .. day0+4h inclusive
	if len(ov.Points) != 5 {
		t.Fatalf("points = %d, want 5: %+v", len(ov.Points), ov.Points)
	}
	for i, p := range ov.Points {
		if p.TS != f.day0+int64(i)*3600 {
			t.Errorf("point %d ts = %d", i, p.TS)
		}
	}
	if ov.Points[0].Requests != 3 || ov.Points[1].Errors != 1 || ov.Points[2].Requests != 1 || ov.Points[3].Requests != 0 {
		t.Errorf("points wrong: %+v", ov.Points)
	}

	// filters
	for q, wantReq := range map[string]int64{
		"provider=kilo":                       1,
		"status=error":                        1,
		"status=ok":                           4,
		"surface=responses":                   1,
		"model=big-pickle":                    3,
		fmt.Sprintf("keyId=%d", f.adminKeyID): 3,
		fmt.Sprintf("userId=%d", f.pi.ID):     1,
	} {
		var o struct{ Totals store.UsageOverviewTotals }
		if code := f.get(t, f.g.handleUsageOverview, f.adminCookie, "/api/usage/overview?"+f.fullRangeQry+"&"+q, &o); code != 200 {
			t.Fatalf("%s: %d", q, code)
		}
		if o.Totals.Requests != wantReq {
			t.Errorf("%s: requests = %d, want %d", q, o.Totals.Requests, wantReq)
		}
	}

	// plain users are forced to themselves
	var own struct{ Totals store.UsageOverviewTotals }
	f.get(t, f.g.handleUsageOverview, f.piCookie, "/api/usage/overview?"+f.fullRangeQry, &own)
	if own.Totals.Requests != 1 || own.Totals.Users != 1 {
		t.Errorf("plain user overview not scoped: %+v", own.Totals)
	}

	// an over-long span is capped and the cap is echoed
	to := f.day0 + 3600
	var capped struct{ Range usageRangeJSON }
	f.get(t, f.g.handleUsageOverview, f.adminCookie, fmt.Sprintf("/api/usage/overview?from=%d&to=%d&bucket=day", to-200*86400, to), &capped)
	if capped.Range.From != to-int64(usageMaxRangeDays)*86400 || capped.Range.To != to {
		t.Errorf("capped range = %+v", capped.Range)
	}
}

type breakdownResp struct {
	Range    usageRangeJSON
	GroupBy  string
	Page     int
	PageSize int
	Total    int64
	Totals   store.UsageTotals
	Rows     []store.UsageBreakdownRow
}

func TestUsageBreakdownEndpoint(t *testing.T) {
	f := newUsageFixture(t)
	bd := func(cookie *http.Cookie, q string) breakdownResp {
		t.Helper()
		var b breakdownResp
		if code := f.get(t, f.g.handleUsageBreakdown, cookie, "/api/usage/breakdown?"+f.fullRangeQry+"&"+q, &b); code != 200 {
			t.Fatalf("breakdown %s: %d", q, code)
		}
		return b
	}

	b := bd(f.adminCookie, "groupBy=model")
	if b.GroupBy != "model" || b.Page != 1 || b.PageSize != 25 || b.Total != 3 || b.Range.From != f.day0 {
		t.Fatalf("envelope wrong: %+v", b)
	}
	if b.Totals.Requests != 5 || b.Totals.Errors != 1 || b.Totals.InputTokens != 113 {
		t.Errorf("totals wrong: %+v", b.Totals)
	}
	// requests desc, ties broken by label asc
	if len(b.Rows) != 3 || b.Rows[0].Model != "big-pickle" || b.Rows[1].Model != "m2" || b.Rows[2].Model != "ghost" {
		t.Fatalf("model order wrong: %+v", b.Rows)
	}
	top := b.Rows[0]
	if top.Provider != "zen" || top.Requests != 3 || top.Errors != 1 || top.UserID != 0 || top.Email != "" ||
		top.FirstTs != f.day0+100 || top.LastTs != f.day0+3700 || top.AvgTtfbMs != 35 || top.AvgDurationMs != 150 {
		t.Errorf("model row wrong: %+v", top)
	}

	// pagination: total is the group count, rows are the page
	p := bd(f.adminCookie, "groupBy=model&pageSize=1&page=2")
	if p.Total != 3 || p.Page != 2 || p.PageSize != 1 || len(p.Rows) != 1 || p.Rows[0].Model != "m2" {
		t.Errorf("page 2 wrong: %+v", p)
	}
	if past := bd(f.adminCookie, "groupBy=model&pageSize=2&page=3"); past.Total != 3 || len(past.Rows) != 0 {
		t.Errorf("past last page: %+v", past)
	}
	if p.Totals.Requests != 5 {
		t.Errorf("totals must cover the whole set, not the page: %+v", p.Totals)
	}

	// sorting
	if s := bd(f.adminCookie, "groupBy=model&sort=label&order=asc"); s.Rows[0].Provider != "kilo" {
		t.Errorf("label asc: %+v", s.Rows)
	}
	if s := bd(f.adminCookie, "groupBy=model&sort=totalTokens"); s.Rows[0].Model != "m2" {
		t.Errorf("totalTokens desc: %+v", s.Rows)
	}
	if s := bd(f.adminCookie, "groupBy=model&sort=errorRate"); s.Rows[0].Model != "big-pickle" {
		t.Errorf("errorRate desc: %+v", s.Rows)
	}
	if s := bd(f.adminCookie, "groupBy=model&sort=lastTs&order=asc"); s.Rows[0].Model != "ghost" {
		t.Errorf("lastTs asc: %+v", s.Rows)
	}

	// user grouping: live email, then placeholder for a deleted user
	u := bd(f.adminCookie, "groupBy=user&sort=label&order=asc")
	if u.Total != 3 || u.Rows[0].Email != "admin@example.com" || u.Rows[1].Email != "deleted user #999" || u.Rows[2].Email != "pi@example.com" {
		t.Errorf("user rows wrong: %+v", u.Rows)
	}
	// q: case-insensitive substring; LIKE wildcards are literal
	if s := bd(f.adminCookie, "groupBy=user&q=DELETED"); s.Total != 1 || s.Rows[0].UserID != 999 {
		t.Errorf("q search: %+v", s)
	}
	if s := bd(f.adminCookie, "groupBy=user&q=%25"); s.Total != 0 {
		t.Errorf("q '%%' must be literal: %+v", s)
	}
	if s := bd(f.adminCookie, "groupBy=user_model&q=pi@"); s.Total != 1 || s.Rows[0].Email != "pi@example.com" ||
		s.Rows[0].Model != "big-pickle" || s.Rows[0].Provider != "zen" || s.Totals.Requests != 1 {
		t.Errorf("user_model search: %+v", s)
	}
	k := bd(f.adminCookie, "groupBy=key&q=deleted")
	if k.Total != 1 || k.Rows[0].KeyID != 888 || k.Rows[0].Alias != "deleted key #888" {
		t.Errorf("deleted key label: %+v", k.Rows)
	}
	uk := bd(f.adminCookie, "groupBy=upstream_key&sort=label&order=asc")
	if uk.Total != 3 || uk.Rows[0].UpstreamKey != "a" || uk.Rows[0].Provider != "kilo" {
		t.Errorf("upstream_key rows: %+v", uk.Rows)
	}

	// plain user: own data only
	own := bd(f.piCookie, "groupBy=key")
	if own.Total != 1 || own.Rows[0].KeyID != f.piKeyID || own.Rows[0].Alias != "pi-key" {
		t.Errorf("plain user keys: %+v", own)
	}
	if m := bd(f.piCookie, "groupBy=model"); m.Total != 1 || m.Totals.Requests != 1 {
		t.Errorf("plain user models: %+v", m)
	}
}

func TestUsageSeriesEndpoint(t *testing.T) {
	f := newUsageFixture(t)
	type seriesResp struct {
		Range   usageRangeJSON
		Bucket  string
		GroupBy string
		Metric  string
		Series  []store.UsageSeries
	}
	get := func(q string) seriesResp {
		t.Helper()
		var s seriesResp
		if code := f.get(t, f.g.handleUsageSeries, f.adminCookie, "/api/usage/series?"+f.fullRangeQry+"&"+q, &s); code != 200 {
			t.Fatalf("series %s: %d", q, code)
		}
		return s
	}
	values := func(s store.UsageSeries) []int64 {
		out := make([]int64, len(s.Points))
		for i, p := range s.Points {
			out[i] = p.Value
		}
		return out
	}

	s := get("groupBy=model&top=1")
	if s.Bucket != "hour" || s.GroupBy != "model" || s.Metric != "requests" || s.Range.To != f.day0+4*3600 {
		t.Errorf("envelope: %+v", s)
	}
	if len(s.Series) != 2 || s.Series[0].Key != "zen/big-pickle" || s.Series[0].Label != "zen/big-pickle" ||
		s.Series[1].Key != "other" || s.Series[1].Label != "Other" {
		t.Fatalf("series keys: %+v", s.Series)
	}
	if got := fmt.Sprint(values(s.Series[0])); got != "[2 1 0 0 0]" {
		t.Errorf("top series = %s", got)
	}
	if got := fmt.Sprint(values(s.Series[1])); got != "[1 0 1 0 0]" {
		t.Errorf("other series = %s (ghost at +300, m2 at +7300)", got)
	}
	for _, se := range s.Series {
		for i, p := range se.Points {
			if p.TS != f.day0+int64(i)*3600 {
				t.Errorf("series %s point %d ts %d not shared/zero-filled", se.Key, i, p.TS)
			}
		}
	}

	// no "other" when every group fits
	if p := get("groupBy=provider"); len(p.Series) != 2 || p.Series[0].Key != "zen" || p.Series[1].Key != "kilo" {
		t.Errorf("provider series: %+v", p.Series)
	}
	// metric=errors: only groups with a non-zero metric are ranked
	if e := get("groupBy=model&metric=errors"); len(e.Series) != 1 || fmt.Sprint(values(e.Series[0])) != "[0 1 0 0 0]" {
		t.Errorf("errors series: %+v", e.Series)
	}
	if tt := get("groupBy=surface&metric=totalTokens&top=1"); tt.Series[0].Key != "messages" || tt.Series[1].Key != "other" {
		t.Errorf("totalTokens by surface: %+v", tt.Series)
	}
	u := get("groupBy=user&top=3")
	if len(u.Series) != 3 || u.Series[0].Key != fmt.Sprint(f.admin.ID) || u.Series[0].Label != "admin@example.com" {
		t.Errorf("user series: %+v", u.Series)
	}

	// plain users: own data, no per-user series
	var own seriesResp
	f.get(t, f.g.handleUsageSeries, f.piCookie, "/api/usage/series?"+f.fullRangeQry+"&groupBy=model", &own)
	if len(own.Series) != 1 || fmt.Sprint(values(own.Series[0])) != "[1 0 0 0 0]" {
		t.Errorf("plain user series: %+v", own.Series)
	}
}

func TestUsageRequestsEndpoint(t *testing.T) {
	f := newUsageFixture(t)
	type reqResp struct {
		Range    usageRangeJSON
		Page     int
		PageSize int
		Total    int64
		Rows     []store.UsageRequestRow
	}
	var r reqResp
	if code := f.get(t, f.g.handleUsageRequests, f.adminCookie, "/api/usage/requests?"+f.fullRangeQry+"&pageSize=2", &r); code != 200 {
		t.Fatalf("requests: %d", code)
	}
	if r.Total != 5 || r.Page != 1 || r.PageSize != 2 || len(r.Rows) != 2 || r.Range.From != f.day0 {
		t.Fatalf("envelope: %+v", r)
	}
	if r.Rows[0].Model != "m2" || r.Rows[1].Status != 502 {
		t.Fatalf("ts desc order wrong: %+v", r.Rows)
	}
	failed := r.Rows[1]
	if failed.FailReason == "" || failed.UpstreamStatus != 503 || failed.UpstreamKey != "key-b" ||
		failed.Email != "admin@example.com" || failed.Alias != "admin-key" || !failed.Stream || failed.Surface != "chat" || failed.Attempts != 1 {
		t.Errorf("failed row detail: %+v", failed)
	}
	var d reqResp
	f.get(t, f.g.handleUsageRequests, f.adminCookie, "/api/usage/requests?"+f.fullRangeQry+"&sort=durationMs&order=asc&page=2&pageSize=2", &d)
	if len(d.Rows) != 2 || d.Rows[0].DurationMs != 100 || d.Rows[1].DurationMs != 200 {
		t.Errorf("durationMs asc page 2: %+v", d.Rows)
	}
	var e reqResp
	f.get(t, f.g.handleUsageRequests, f.adminCookie, "/api/usage/requests?"+f.fullRangeQry+"&status=error", &e)
	if e.Total != 1 || e.Rows[0].Status != 502 {
		t.Errorf("status=error: %+v", e)
	}

	// plain user: own rows only, upstream key labels blanked
	var own reqResp
	f.get(t, f.g.handleUsageRequests, f.piCookie, "/api/usage/requests?"+f.fullRangeQry, &own)
	if own.Total != 1 || own.Rows[0].UserID != f.pi.ID || own.Rows[0].UpstreamKey != "" || own.Rows[0].TtfbMs != 20 {
		t.Errorf("plain user requests: %+v", own)
	}
}

func TestUsageFacetsEndpoint(t *testing.T) {
	f := newUsageFixture(t)
	var all store.UsageFacets
	if code := f.get(t, f.g.handleUsageFacets, f.adminCookie, "/api/usage/facets?"+f.fullRangeQry, &all); code != 200 {
		t.Fatalf("facets: %d", code)
	}
	if len(all.Users) != 3 || fmt.Sprint(all.Providers) != "[kilo zen]" || len(all.Models) != 3 ||
		len(all.Keys) != 3 || fmt.Sprint(all.Surfaces) != "[chat messages responses]" {
		t.Errorf("admin facets: %+v", all)
	}
	var own map[string]json.RawMessage
	f.get(t, f.g.handleUsageFacets, f.piCookie, "/api/usage/facets?"+f.fullRangeQry, &own)
	if string(own["users"]) != "[]" || string(own["providers"]) != `["zen"]` || string(own["surfaces"]) != `["responses"]` {
		t.Errorf("plain user facets: %v", own)
	}
	var keys []store.UsageFacetKey
	json.Unmarshal(own["keys"], &keys)
	if len(keys) != 1 || keys[0].ID != f.piKeyID || keys[0].UserID != f.pi.ID || keys[0].Alias != "pi-key" {
		t.Errorf("plain user key facets: %+v", keys)
	}
}

func TestUsageV2ParamValidationAndScoping(t *testing.T) {
	f := newUsageFixture(t)
	r := "&" + f.fullRangeQry
	for _, tc := range []struct {
		name   string
		h      http.HandlerFunc
		cookie *http.Cookie
		query  string
		want   int
	}{
		{"bad range", f.g.handleUsageOverview, f.adminCookie, "/api/usage/overview?from=10&to=5", 400},
		{"bad bucket", f.g.handleUsageOverview, f.adminCookie, "/api/usage/overview?bucket=week" + r, 400},
		{"bad tz", f.g.handleUsageOverview, f.adminCookie, "/api/usage/overview?tz=999999" + r, 400},
		{"bad surface", f.g.handleUsageOverview, f.adminCookie, "/api/usage/overview?surface=grpc" + r, 400},
		{"bad status", f.g.handleUsageOverview, f.adminCookie, "/api/usage/overview?status=meh" + r, 400},
		{"bad userId", f.g.handleUsageOverview, f.adminCookie, "/api/usage/overview?userId=abc" + r, 400},
		{"bad keyId", f.g.handleUsageRequests, f.adminCookie, "/api/usage/requests?keyId=-1" + r, 400},
		{"bad groupBy", f.g.handleUsageBreakdown, f.adminCookie, "/api/usage/breakdown?groupBy=planet" + r, 400},
		{"bad sort", f.g.handleUsageBreakdown, f.adminCookie, "/api/usage/breakdown?sort=id" + r, 400},
		{"bad order", f.g.handleUsageBreakdown, f.adminCookie, "/api/usage/breakdown?order=up" + r, 400},
		{"page 0", f.g.handleUsageBreakdown, f.adminCookie, "/api/usage/breakdown?page=0" + r, 400},
		{"pageSize 0", f.g.handleUsageBreakdown, f.adminCookie, "/api/usage/breakdown?pageSize=0" + r, 400},
		{"pageSize 201", f.g.handleUsageRequests, f.adminCookie, "/api/usage/requests?pageSize=201" + r, 400},
		{"requests bad sort", f.g.handleUsageRequests, f.adminCookie, "/api/usage/requests?sort=label" + r, 400},
		{"series bad metric", f.g.handleUsageSeries, f.adminCookie, "/api/usage/series?metric=cost" + r, 400},
		{"series bad groupBy", f.g.handleUsageSeries, f.adminCookie, "/api/usage/series?groupBy=key" + r, 400},
		{"series top 11", f.g.handleUsageSeries, f.adminCookie, "/api/usage/series?top=11" + r, 400},
		{"series top 0", f.g.handleUsageSeries, f.adminCookie, "/api/usage/series?top=0" + r, 400},
		{"facets bad range", f.g.handleUsageFacets, f.adminCookie, "/api/usage/facets?from=x", 400},

		{"other userId", f.g.handleUsageOverview, f.piCookie, fmt.Sprintf("/api/usage/overview?userId=%d", f.admin.ID) + r, 403},
		{"own userId", f.g.handleUsageOverview, f.piCookie, fmt.Sprintf("/api/usage/overview?userId=%d", f.pi.ID) + r, 200},
		{"other userId requests", f.g.handleUsageRequests, f.piCookie, fmt.Sprintf("/api/usage/requests?userId=%d", f.admin.ID) + r, 403},
		{"groupBy user", f.g.handleUsageBreakdown, f.piCookie, "/api/usage/breakdown?groupBy=user" + r, 403},
		{"groupBy user_model", f.g.handleUsageBreakdown, f.piCookie, "/api/usage/breakdown?groupBy=user_model" + r, 403},
		{"groupBy upstream_key", f.g.handleUsageBreakdown, f.piCookie, "/api/usage/breakdown?groupBy=upstream_key" + r, 403},
		{"series by user", f.g.handleUsageSeries, f.piCookie, "/api/usage/series?groupBy=user" + r, 403},
		{"admin any userId", f.g.handleUsageBreakdown, f.adminCookie, fmt.Sprintf("/api/usage/breakdown?groupBy=user&userId=%d", f.pi.ID) + r, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code := f.get(t, tc.h, tc.cookie, tc.query, nil); code != tc.want {
				t.Errorf("%s: got %d, want %d", tc.query, code, tc.want)
			}
		})
	}
}

// --- hardening ---

// Hostile ranges must be refused or capped fast: an overflowing span used to
// skip the 92-day cap (unbounded bucket fill-in) and to=MaxInt64 used to wrap
// the bucket cursor into an endless loop.
func TestUsageRangeExtremeBounds(t *testing.T) {
	f := newUsageFixture(t)
	const maxI, minI = "9223372036854775807", "-9223372036854775808"
	handlers := map[string]http.HandlerFunc{
		"overview":   f.g.handleUsageOverview,
		"series":     f.g.handleUsageSeries,
		"timeseries": f.g.handleUsageTimeseries,
	}
	for name, h := range handlers {
		for _, qs := range []string{
			"from=" + minI + "&to=" + maxI,
			"from=" + minI,
			"from=" + minI + "&to=0",
			"from=-1",
			"to=-5",
			"from=" + maxI, // after clamping to=now, from >= to
		} {
			if code := f.get(t, h, f.adminCookie, "/api/usage/"+name+"?"+qs+"&bucket=hour", nil); code != http.StatusBadRequest {
				t.Errorf("%s?%s: got %d, want 400", name, qs, code)
			}
		}
		for _, qs := range []string{"from=0&to=" + maxI, "from=0", "from=1&to=" + maxI} {
			var body struct {
				Range  usageRangeJSON
				Points []json.RawMessage
				Series []struct{ Points []json.RawMessage }
			}
			before := time.Now().Unix()
			if code := f.get(t, h, f.adminCookie, "/api/usage/"+name+"?"+qs+"&bucket=hour", &body); code != 200 {
				t.Fatalf("%s?%s: got %d, want a capped 200", name, qs, code)
			}
			if name == "timeseries" {
				continue // legacy shape: no range echo, sparse points
			}
			if body.Range.To < before || body.Range.To > time.Now().Unix() {
				t.Errorf("%s?%s: to=%d not clamped to now", name, qs, body.Range.To)
			}
			if span := body.Range.To - body.Range.From; span != usageMaxRangeDays*86400 {
				t.Errorf("%s?%s: span %ds, want the 92-day cap", name, qs, span)
			}
			if n := len(body.Points); n > usageMaxRangeDays*24+1 {
				t.Errorf("%s?%s: %d points exceed the capped range", name, qs, n)
			}
		}
	}
}

func TestDashboardActivityScopedBeforeLimit(t *testing.T) {
	ut := newUsageTracker()
	// 100 slots: the caller (user 7) owns only the 3 oldest entries
	for i := range activityRingSize {
		uid := int64(1)
		if i < 3 {
			uid = 7
		}
		ut.addActivity(activityEntry{TS: int64(1000 + i), UserID: uid, KeyAlias: fmt.Sprintf("k%d", i)})
	}
	mine := func(e activityEntry) bool { return e.UserID == 7 }
	got := ut.recentWhere(2, mine)
	if len(got) != 2 || got[0].KeyAlias != "k2" || got[1].KeyAlias != "k1" {
		t.Fatalf("recentWhere(2) = %+v, want k2, k1", got)
	}
	if got := ut.recentWhere(50, mine); len(got) != 3 {
		t.Fatalf("recentWhere(50) returned %d entries, want all 3 own", len(got))
	}
	if got := ut.recentWhere(5, func(activityEntry) bool { return false }); len(got) != 0 {
		t.Fatalf("no-match predicate returned %d entries", len(got))
	}
	if got := ut.recent(10); len(got) != 10 || got[0].KeyAlias != "k99" {
		t.Fatalf("unfiltered recent changed: %d first=%+v", len(got), got[0])
	}
}

func TestUsageCaptureTruncatesClientIdentifiers(t *testing.T) {
	g, st := testStoreGateway(t, "http://127.0.0.1:1")
	admin, _ := st.UserByEmail("admin@example.com")
	ck := newClientKey(t, g, st, admin.ID, "laptop")

	prov := strings.Repeat("p", 500)
	model := strings.Repeat("é", 500) // multi-byte: truncation must stay rune-safe
	body := fmt.Sprintf(`{"model":%q,"messages":[],"stream":false}`, prov+"/"+model)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("x-api-key", ck)
	g.clientOnly(g.handleMessages)(rec, req)
	if rec.Code != 400 {
		t.Fatalf("got %d, want 400", rec.Code)
	}
	events := g.usageBuf.drain()
	if len(events) != 1 {
		t.Fatalf("drained %d events, want 1", len(events))
	}
	e := events[0]
	if n := len([]rune(e.Provider)); n != maxUsageLabel {
		t.Errorf("provider kept %d runes, want %d", n, maxUsageLabel)
	}
	if n := len([]rune(e.Model)); n != maxUsageLabel || !strings.HasPrefix(model, e.Model) {
		t.Errorf("model kept %d runes (rune-safe prefix: %v), want %d", n, strings.HasPrefix(model, e.Model), maxUsageLabel)
	}
}
