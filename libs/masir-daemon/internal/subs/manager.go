// Package subs fetches, parses and stores xray subscriptions, one JSON file each.
package subs

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/atomicfile"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/xray"
)

const (
	DefaultInterval   = 12 * time.Hour
	retryAfterFailure = 30 * time.Minute
	checkEvery        = 5 * time.Minute
	fetchTimeout      = 30 * time.Second
	latencyWorkers    = 8
	latencyTimeout    = 8 * time.Second
)

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
	ErrInvalid  = errors.New("invalid argument")
	ErrFetch    = errors.New("fetch failed")
)

type Subscription struct {
	ID                      string    `json:"id"`
	Name                    string    `json:"name"`
	URL                     string    `json:"url"`
	CustomIntervalMinutes   uint32    `json:"customIntervalMinutes,omitempty"` // 0 means automatic
	ProviderIntervalMinutes uint32    `json:"providerIntervalMinutes,omitempty"`
	CreatedAt               time.Time `json:"createdAt"`
	UpdatedAt               time.Time `json:"updatedAt,omitzero"` // last successful fetch
	CheckedAt               time.Time `json:"checkedAt,omitzero"` // last attempt
	LastError               string    `json:"lastError,omitempty"`
	Usage                   *Usage    `json:"usage,omitempty"`
	Configs                 []Config  `json:"configs"`
}

type Latency struct {
	Ms       int64     `json:"ms,omitempty"`
	Error    string    `json:"error,omitempty"`
	TestedAt time.Time `json:"testedAt"`
}

// Active is the config selected as the proxy. Outbound is a copy, so the proxy keeps
// working when the subscription drops the config or is removed. Name lets a refresh
// find its replacement when the provider rotates the server's settings.
type Active struct {
	SubscriptionID string          `json:"subscriptionId"`
	ConfigID       string          `json:"configId"`
	Name           string          `json:"name"`
	Outbound       json.RawMessage `json:"outbound,omitempty"`
}

// Interval is how often the subscription refreshes: custom, else the provider's, else the default.
func (s *Subscription) Interval() time.Duration {
	switch {
	case s.CustomIntervalMinutes > 0:
		return time.Duration(s.CustomIntervalMinutes) * time.Minute
	case s.ProviderIntervalMinutes > 0:
		return time.Duration(s.ProviderIntervalMinutes) * time.Minute
	default:
		return DefaultInterval
	}
}

func (s *Subscription) due(now time.Time) bool {
	last := cmp.Or(s.CheckedAt, s.CreatedAt)
	wait := s.Interval()
	if s.LastError != "" {
		wait = min(wait, retryAfterFailure)
	}
	return now.Sub(last) >= wait
}

func (s *Subscription) config(id string) (Config, bool) {
	for _, c := range s.Configs {
		if c.ID == id {
			return c, true
		}
	}
	return Config{}, false
}

type Manager struct {
	dir        string
	activePath string
	direct     *http.Client
	viaProxy   *http.Client

	mu sync.Mutex // serializes read-modify-write of the files; never held across a fetch

	// Log reports background activity. OnChange runs after a subscription's configs
	// change or it's removed, so whoever pins configs can rebuild. Both are optional
	// and set before Run; OnChange is called without the manager's lock held.
	Log      func(level, msg string)
	OnChange func(ctx context.Context)

	now func() time.Time
}

// NewManager stores subscriptions in dir and the selection in activePath. A fetch that
// fails directly is retried through proxyDial, if given.
func NewManager(dir, activePath string, proxyDial func(ctx context.Context, network, addr string) (net.Conn, error)) (*Manager, error) {
	if err := atomicfile.MkdirAll(dir); err != nil {
		return nil, err
	}
	m := &Manager{
		dir:        dir,
		activePath: activePath,
		direct:     &http.Client{Timeout: fetchTimeout},
		now:        time.Now,
	}
	if proxyDial != nil {
		m.viaProxy = &http.Client{Timeout: fetchTimeout, Transport: &http.Transport{DialContext: proxyDial}}
	}
	return m, nil
}

func (m *Manager) List() ([]Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, err
	}
	var out []Subscription
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !validID(id) {
			continue // temp files and strays
		}
		s, err := m.load(id)
		if err != nil {
			m.log("warn", fmt.Sprintf("skipping subscription %s: %v", id, err))
			continue
		}
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b Subscription) int { return a.CreatedAt.Compare(b.CreatedAt) })
	return out, nil
}

func (m *Manager) Get(id string) (*Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.load(id)
}

func (m *Manager) Config(subID, configID string) (Config, error) {
	s, err := m.Get(subID)
	if err != nil {
		return Config{}, err
	}
	c, ok := s.config(configID)
	if !ok {
		return Config{}, fmt.Errorf("config %q: %w", configID, ErrNotFound)
	}
	return c, nil
}

// Add fetches rawURL and saves the subscription only if that works.
func (m *Manager) Add(ctx context.Context, rawURL, name string, customIntervalMinutes uint32) (*Subscription, error) {
	u, err := checkURL(rawURL)
	if err != nil {
		return nil, err
	}
	if err := m.checkUniqueURL(u, ""); err != nil {
		return nil, err
	}
	f, configs, err := m.download(ctx, u)
	if err != nil {
		return nil, err
	}

	now := m.now()
	s := &Subscription{
		ID:                    newID(),
		Name:                  cmp.Or(strings.TrimSpace(name), f.title, hostOf(u)),
		URL:                   u,
		CustomIntervalMinutes: customIntervalMinutes,
		CreatedAt:             now,
		CheckedAt:             now,
	}
	s.applyFetch(f, configs, now)

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.save(s); err != nil {
		return nil, err
	}
	m.log("info", fmt.Sprintf("subscription added: %s (%d configs)", s.Name, len(s.Configs)))
	return s, nil
}

// Alter changes the given fields. A new URL is fetched first and rejected if that fails.
func (m *Manager) Alter(ctx context.Context, id string, name, rawURL *string, customIntervalMinutes *uint32) (*Subscription, error) {
	cur, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	var (
		newURL  string
		f       fetched
		configs []Config
	)
	if rawURL != nil {
		if newURL, err = checkURL(*rawURL); err != nil {
			return nil, err
		}
		if newURL == cur.URL {
			newURL = ""
		} else {
			if err := m.checkUniqueURL(newURL, id); err != nil {
				return nil, err
			}
			if f, configs, err = m.download(ctx, newURL); err != nil {
				return nil, err
			}
		}
	}
	if name != nil && strings.TrimSpace(*name) == "" {
		return nil, fmt.Errorf("name can't be empty: %w", ErrInvalid)
	}

	m.mu.Lock()
	s, err := m.load(id)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if name != nil {
		s.Name = strings.TrimSpace(*name)
	}
	if customIntervalMinutes != nil {
		s.CustomIntervalMinutes = *customIntervalMinutes
	}
	if newURL != "" {
		s.URL = newURL
		s.CheckedAt = m.now()
		s.applyFetch(f, configs, s.CheckedAt)
	}
	err = m.save(s)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if newURL != "" {
		m.configsChanged(ctx, s)
	}
	return s, nil
}

// Remove deletes the subscription. A selected config from it keeps running from its copy.
func (m *Manager) Remove(ctx context.Context, id string) error {
	if !validID(id) {
		return fmt.Errorf("subscription %q: %w", id, ErrNotFound)
	}
	m.mu.Lock()
	err := os.Remove(m.path(id))
	m.mu.Unlock()
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("subscription %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return err
	}
	m.changed(ctx)
	return nil
}

// Find returns the config with configID, or else one with the same name: the
// provider rotated that server's settings, which changes its ID.
func (m *Manager) Find(subID, configID, name string) (Config, bool) {
	s, err := m.Get(subID)
	if err != nil {
		return Config{}, false
	}
	if c, ok := s.config(configID); ok {
		return c, true
	}
	if name == "" {
		return Config{}, false
	}
	for _, c := range s.Configs {
		if c.Name == name {
			return c, true
		}
	}
	return Config{}, false
}

// Update re-fetches now. On failure the old configs stay and LastError records why.
func (m *Manager) Update(ctx context.Context, id string) (*Subscription, error) {
	cur, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	f, configs, ferr := m.download(ctx, cur.URL)

	m.mu.Lock()
	s, err := m.load(id) // re-read: it may have changed or gone while we fetched
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	s.CheckedAt = m.now()
	if ferr != nil {
		s.LastError = ferr.Error()
	} else {
		s.applyFetch(f, configs, s.CheckedAt)
	}
	err = m.save(s)
	m.mu.Unlock()
	if ferr != nil {
		return nil, ferr
	}
	if err != nil {
		return nil, err
	}
	m.configsChanged(ctx, s)
	return s, nil
}

// TestLatency probes the subscription's configs (all, or just configIDs), calls report
// as each finishes, and saves the results. Probes cut short by ctx aren't saved.
func (m *Manager) TestLatency(ctx context.Context, subID string, configIDs []string, probeURL string, report func(configID string, l Latency)) error {
	s, err := m.Get(subID)
	if err != nil {
		return err
	}
	var probes []xray.Probe
	if len(configIDs) == 0 {
		for _, c := range s.Configs {
			probes = append(probes, xray.Probe{Tag: c.ID, Outbound: c.Outbound})
		}
	} else {
		for _, id := range configIDs {
			c, ok := s.config(id)
			if !ok {
				return fmt.Errorf("config %q: %w", id, ErrNotFound)
			}
			probes = append(probes, xray.Probe{Tag: c.ID, Outbound: c.Outbound})
		}
	}

	results := map[string]Latency{}
	opt := xray.LatencyOptions{URL: probeURL, Timeout: latencyTimeout}
	xray.MeasureEach(ctx, probes, opt, latencyWorkers, func(id string, d time.Duration, err error) {
		l := Latency{Ms: d.Milliseconds(), TestedAt: m.now()}
		if err != nil {
			if ctx.Err() != nil {
				return // cancelled, not a verdict on the server
			}
			l = Latency{Error: err.Error(), TestedAt: l.TestedAt}
		}
		results[id] = l
		report(id, l)
	})

	m.mu.Lock()
	defer m.mu.Unlock()
	cur, err := m.load(subID)
	if err != nil {
		return err
	}
	for i := range cur.Configs {
		if l, ok := results[cur.Configs[i].ID]; ok {
			cur.Configs[i].Latency = &l
		}
	}
	return m.save(cur)
}

func (m *Manager) Active() (Active, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readActive()
}

func (m *Manager) SetActive(a Active) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writeActive(a)
}

// Run refreshes due subscriptions until ctx is done.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(checkEvery)
	defer t.Stop()
	for {
		m.refreshDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Manager) refreshDue(ctx context.Context) {
	all, err := m.List()
	if err != nil {
		m.log("error", "list subscriptions: "+err.Error())
		return
	}
	for _, s := range all {
		if ctx.Err() != nil {
			return
		}
		if !s.due(m.now()) {
			continue
		}
		if u, err := m.Update(ctx, s.ID); err != nil {
			m.log("warn", fmt.Sprintf("subscription %s: %v", s.Name, err))
		} else {
			m.log("info", fmt.Sprintf("subscription %s refreshed (%d configs)", u.Name, len(u.Configs)))
		}
	}
}

// configsChanged runs after s got new configs. If the selection's ID is gone but a
// config with its name remains, the selection moves to it; if nothing matches, the
// copy keeps running. Then OnChange lets pinned rules re-resolve.
func (m *Manager) configsChanged(ctx context.Context, s *Subscription) {
	m.mu.Lock()
	a, err := m.readActive()
	if err == nil && a.SubscriptionID == s.ID && a.ConfigID != "" {
		if _, ok := s.config(a.ConfigID); !ok {
			moved := false
			for _, c := range s.Configs {
				if c.Name == a.Name {
					err = m.writeActive(Active{SubscriptionID: s.ID, ConfigID: c.ID, Name: c.Name, Outbound: c.Outbound})
					moved = err == nil
					break
				}
			}
			switch {
			case moved:
				m.log("info", fmt.Sprintf("active config %q changed upstream; switched to its new settings", a.Name))
			case err != nil:
				m.log("error", "save selection: "+err.Error())
			default:
				m.log("warn", fmt.Sprintf("active config %q is no longer in %s; it keeps running", a.Name, s.Name))
			}
		}
	}
	m.mu.Unlock()
	m.changed(ctx)
}

func (m *Manager) changed(ctx context.Context) {
	if m.OnChange != nil {
		m.OnChange(ctx)
	}
}

// download fetches and parses url directly, then through the proxy if that fails
// (censored provider domains, or an ISP serving a block page).
func (m *Manager) download(ctx context.Context, u string) (fetched, []Config, error) {
	f, configs, err := fetchAndParse(ctx, m.direct, u)
	if err == nil || m.viaProxy == nil {
		return f, configs, wrapFetch(err)
	}
	f, configs, perr := fetchAndParse(ctx, m.viaProxy, u)
	if perr == nil {
		return f, configs, nil
	}
	if errors.Is(perr, xray.ErrNotRunning) {
		return fetched{}, nil, wrapFetch(err)
	}
	return fetched{}, nil, wrapFetch(fmt.Errorf("%v; via proxy: %v", err, perr))
}

func fetchAndParse(ctx context.Context, client *http.Client, u string) (fetched, []Config, error) {
	f, err := fetch(ctx, client, u)
	if err != nil {
		return fetched{}, nil, err
	}
	configs, err := Parse(f.body)
	if err != nil {
		return fetched{}, nil, err
	}
	return f, configs, nil
}

func wrapFetch(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrFetch, err)
}

// applyFetch replaces the configs, keeping latency results for configs that didn't change.
func (s *Subscription) applyFetch(f fetched, configs []Config, now time.Time) {
	for i := range configs {
		if old, ok := s.config(configs[i].ID); ok {
			configs[i].Latency = old.Latency
		}
	}
	s.Configs = configs
	s.Usage = f.usage
	s.ProviderIntervalMinutes = uint32(f.interval / time.Minute)
	s.UpdatedAt = now
	s.LastError = ""
}

func (m *Manager) checkUniqueURL(u, exceptID string) error {
	all, err := m.List()
	if err != nil {
		return err
	}
	for _, s := range all {
		if s.URL == u && s.ID != exceptID {
			return fmt.Errorf("subscription %q has this url: %w", s.Name, ErrExists)
		}
	}
	return nil
}

// load expects m.mu to be held.
func (m *Manager) load(id string) (*Subscription, error) {
	if !validID(id) {
		return nil, fmt.Errorf("subscription %q: %w", id, ErrNotFound)
	}
	raw, err := os.ReadFile(m.path(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("subscription %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var s Subscription
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("subscription %s: %w", id, err)
	}
	return &s, nil
}

// save expects m.mu to be held.
func (m *Manager) save(s *Subscription) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(m.path(s.ID), raw)
}

func (m *Manager) readActive() (Active, error) {
	raw, err := os.ReadFile(m.activePath)
	if errors.Is(err, fs.ErrNotExist) {
		return Active{}, nil
	}
	if err != nil {
		return Active{}, err
	}
	var a Active
	err = json.Unmarshal(raw, &a)
	return a, err
}

func (m *Manager) writeActive(a Active) error {
	raw, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(m.activePath, raw)
}

func (m *Manager) path(id string) string { return filepath.Join(m.dir, id+".json") }

func (m *Manager) log(level, msg string) {
	if m.Log != nil {
		m.Log(level, msg)
	}
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// validID also keeps request IDs from escaping the subscriptions directory.
func validID(id string) bool { return idPattern.MatchString(id) }

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func checkURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("url must be an http(s) address: %w", ErrInvalid)
	}
	return u.String(), nil
}

func hostOf(u string) string {
	p, _ := url.Parse(u)
	return p.Hostname()
}
