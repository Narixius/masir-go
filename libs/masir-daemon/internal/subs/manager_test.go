package subs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// provider is a fake subscription server whose body and status can change between fetches.
type provider struct {
	mu     sync.Mutex
	body   string
	status int
	srv    *httptest.Server
}

func newProvider(t *testing.T, body string) *provider {
	p := &provider{body: body, status: http.StatusOK}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		w.Header().Set("Subscription-Userinfo", "upload=1; download=2; total=100; expire=0")
		w.Header().Set("Profile-Update-Interval", "6")
		w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte("Provider Title")))
		w.WriteHeader(p.status)
		_, _ = w.Write([]byte(p.body))
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *provider) set(body string, status int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.body, p.status = body, status
}

func newTestManager(t *testing.T, proxyDial func(context.Context, string, string) (net.Conn, error)) *Manager {
	t.Helper()
	dir := t.TempDir()
	m, err := NewManager(filepath.Join(dir, "subscriptions"), filepath.Join(dir, "active.json"), proxyDial)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAddAndList(t *testing.T) {
	ctx := context.Background()
	p := newProvider(t, links())
	m := newTestManager(t, nil)

	s, err := m.Add(ctx, p.srv.URL+"/sub", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "Provider Title" || len(s.Configs) != 4 || s.Usage == nil || s.Usage.Total != 100 {
		t.Fatalf("added = %+v", s)
	}
	if s.Interval() != 6*time.Hour {
		t.Fatalf("interval = %v, want the provider's 6h", s.Interval())
	}
	info, err := os.Stat(m.path(s.ID))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("subscription file: %v, %v", info, err)
	}

	if _, err := m.Add(ctx, p.srv.URL+"/sub", "dup", 0); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate url: got %v", err)
	}
	if _, err := m.Add(ctx, "ftp://x", "", 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad url: got %v", err)
	}

	p.set("", http.StatusInternalServerError)
	if _, err := m.Add(ctx, p.srv.URL+"/other", "", 0); !errors.Is(err, ErrFetch) {
		t.Fatalf("failing provider: got %v", err)
	}
	all, err := m.List()
	if err != nil || len(all) != 1 {
		t.Fatalf("a failed add must not be saved; list = %d, %v", len(all), err)
	}
}

func TestUpdateKeepsLatencyAndSurvivesFailure(t *testing.T) {
	ctx := context.Background()
	p := newProvider(t, trojanWS+"\n"+ssLink())
	m := newTestManager(t, nil)
	s, err := m.Add(ctx, p.srv.URL, "mine", 0)
	if err != nil {
		t.Fatal(err)
	}

	// Pretend trojan was tested, then the provider rotates the ss server.
	m.mu.Lock()
	cur, _ := m.load(s.ID)
	for i := range cur.Configs {
		cur.Configs[i].Latency = &Latency{Ms: 42}
	}
	_ = m.save(cur)
	m.mu.Unlock()
	p.set(trojanWS+"\n"+strings.Replace(ssLink(), "8388", "9999", 1), http.StatusOK)

	u, err := m.Update(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := byName(t, u.Configs)
	if got["Trojan WS"].Latency == nil || got["Trojan WS"].Latency.Ms != 42 {
		t.Error("unchanged config lost its latency")
	}
	if got["SS"].Latency != nil || got["SS"].Port != 9999 {
		t.Errorf("changed config = %+v", got["SS"])
	}

	p.set("", http.StatusBadGateway)
	if _, err := m.Update(ctx, s.ID); !errors.Is(err, ErrFetch) {
		t.Fatalf("got %v, want ErrFetch", err)
	}
	after, _ := m.Get(s.ID)
	if after.LastError == "" || len(after.Configs) != 2 || after.UpdatedAt.After(after.CheckedAt) {
		t.Fatalf("after failed update = %+v", after)
	}
}

func TestFetchRetriesThroughProxy(t *testing.T) {
	p := newProvider(t, trojanWS)
	var proxied bool
	m := newTestManager(t, func(ctx context.Context, network, _ string) (net.Conn, error) {
		proxied = true
		return (&net.Dialer{}).DialContext(ctx, network, p.srv.Listener.Addr().String())
	})
	// .invalid never resolves, so only the proxy path can reach the provider.
	s, err := m.Add(context.Background(), "http://sub.invalid/x", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !proxied || len(s.Configs) != 1 {
		t.Fatalf("proxied=%v configs=%d", proxied, len(s.Configs))
	}
}

func TestAlterAndRemove(t *testing.T) {
	ctx := context.Background()
	p := newProvider(t, trojanWS)
	m := newTestManager(t, nil)
	s, _ := m.Add(ctx, p.srv.URL, "old", 0)

	name, interval := "new", uint32(90)
	a, err := m.Alter(ctx, s.ID, &name, nil, &interval)
	if err != nil || a.Name != "new" || a.Interval() != 90*time.Minute {
		t.Fatalf("alter = %+v, %v", a, err)
	}
	empty := " "
	if _, err := m.Alter(ctx, s.ID, &empty, nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty name: got %v", err)
	}
	bad := "http://sub.invalid/x"
	if _, err := m.Alter(ctx, s.ID, nil, &bad, nil); !errors.Is(err, ErrFetch) {
		t.Fatalf("unreachable url: got %v", err)
	}
	if g, _ := m.Get(s.ID); g.URL != s.URL {
		t.Fatal("a url that failed to fetch was saved")
	}

	sel := Active{SubscriptionID: s.ID, ConfigID: s.Configs[0].ID, Name: s.Configs[0].Name, Outbound: s.Configs[0].Outbound}
	if err := m.SetActive(sel); err != nil {
		t.Fatal(err)
	}
	changes := 0
	m.OnChange = func(context.Context) { changes++ }
	if err := m.Remove(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after remove: %v", err)
	}
	if a, _ := m.Active(); a.ConfigID != sel.ConfigID || !sameJSON(a.Outbound, sel.Outbound) {
		t.Fatalf("the selection should keep running from its copy, got %+v", a)
	}
	if changes != 1 {
		t.Fatalf("OnChange called %d times, want 1", changes)
	}
	if err := m.Remove(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove: %v", err)
	}
}

func TestIDsCantEscapeDir(t *testing.T) {
	m := newTestManager(t, nil)
	for _, id := range []string{"../active", "../../etc/passwd", "", "ABCDEF0123456789"} {
		if _, err := m.Get(id); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) = %v, want ErrNotFound", id, err)
		}
	}
}

func TestFollowActive(t *testing.T) {
	ctx := context.Background()
	p := newProvider(t, trojanWS+"\n"+ssLink())
	m := newTestManager(t, nil)
	changes := 0
	m.OnChange = func(context.Context) { changes++ }
	s, _ := m.Add(ctx, p.srv.URL, "", 0)
	tr := byName(t, s.Configs)["Trojan WS"]
	_ = m.SetActive(Active{SubscriptionID: s.ID, ConfigID: tr.ID, Name: tr.Name, Outbound: tr.Outbound})

	// Same settings: the selection stays.
	if _, err := m.Update(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if a, _ := m.Active(); a.ConfigID != tr.ID || changes != 1 {
		t.Fatalf("active=%+v changes=%d", a, changes)
	}

	// Credentials rotated, same name: follow it, copy included.
	p.set(strings.Replace(trojanWS, "secret", "rotated", 1)+"\n"+ssLink(), http.StatusOK)
	u, _ := m.Update(ctx, s.ID)
	rotated := byName(t, u.Configs)["Trojan WS"]
	a, _ := m.Active()
	if a.ConfigID != rotated.ID || !sameJSON(a.Outbound, rotated.Outbound) {
		t.Fatalf("active=%+v, want the rotated config", a)
	}
	if c, ok := m.Find(s.ID, tr.ID, tr.Name); !ok || c.ID != rotated.ID {
		t.Fatalf("Find by the old ID and name = %v, %v", c.ID, ok)
	}

	// Gone entirely: the copy keeps running.
	p.set(ssLink(), http.StatusOK)
	_, _ = m.Update(ctx, s.ID)
	if b, _ := m.Active(); b.ConfigID != a.ConfigID || !sameJSON(b.Outbound, a.Outbound) {
		t.Fatalf("active=%+v, want it unchanged", b)
	}
	if _, ok := m.Find(s.ID, tr.ID, tr.Name); ok {
		t.Fatal("Find should fail once the config is gone")
	}
}

func TestDue(t *testing.T) {
	now := time.Now()
	s := Subscription{CreatedAt: now.Add(-time.Hour), CheckedAt: now.Add(-time.Hour)}
	if s.due(now) {
		t.Error("due an hour into a 12h interval")
	}
	s.CustomIntervalMinutes = 30
	if !s.due(now) {
		t.Error("not due an hour into a 30m interval")
	}
	s = Subscription{CheckedAt: now.Add(-45 * time.Minute), LastError: "boom"}
	if !s.due(now) {
		t.Error("a failed subscription should retry after 30m")
	}
}

func TestTestLatency(t *testing.T) {
	ctx := context.Background()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	p := newProvider(t, `[{"tag":"works","protocol":"freedom"},{"tag":"dead","protocol":"blackhole"}]`)
	m := newTestManager(t, nil)
	s, err := m.Add(ctx, p.srv.URL, "", 0)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]Latency{}
	if err := m.TestLatency(ctx, s.ID, nil, target.URL, func(id string, l Latency) { got[id] = l }); err != nil {
		t.Fatal(err)
	}
	cfg := byName(t, s.Configs)
	if l := got[cfg["works"].ID]; l.Error != "" {
		t.Errorf("freedom probe failed: %+v", l)
	}
	if l := got[cfg["dead"].ID]; l.Error == "" {
		t.Errorf("blackhole probe succeeded: %+v", l)
	}

	saved, _ := m.Get(s.ID)
	for _, c := range saved.Configs {
		if c.Latency == nil || c.Latency.TestedAt.IsZero() {
			t.Errorf("%s: latency not saved", c.Name)
		}
	}

	if err := m.TestLatency(ctx, s.ID, []string{"nope"}, target.URL, func(string, Latency) {}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown config id: %v", err)
	}
}

// sameJSON compares by content: saved files are indented, freshly parsed configs aren't.
func sameJSON(a, b []byte) bool {
	var ca, cb bytes.Buffer
	return json.Compact(&ca, a) == nil && json.Compact(&cb, b) == nil && bytes.Equal(ca.Bytes(), cb.Bytes())
}
