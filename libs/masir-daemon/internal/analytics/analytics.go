package analytics

import (
	"context"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/posthog/posthog-go"

	coreanalytics "github.com/narixius/masir-go/libs/masir-core/analytics"
)

// Build-time key. It is a public PostHog project token, not a secret API key.
var (
	apiKey = ""
	host   = "https://eu.i.posthog.com"
)

// Options configure the daemon's analytics client.
type Options struct {
	Dir     string // config directory, for the install ID and spool
	Version string
	Enabled bool // the user's analytics setting
	Proxy   Proxy
}

type enqueuer interface {
	Enqueue(posthog.Message) error
	Close() error
}

// Client wraps PostHog. A nil or disabled client is safe to use: every method is a no-op.
type Client struct {
	mu        sync.Mutex
	dir       string
	version   string
	installID string
	proxy     Proxy
	enabled   bool
	api       enqueuer
}

// newPostHog is a variable so tests can substitute a fake without a network.
var newPostHog = func(key string, cfg posthog.Config) (enqueuer, error) {
	return posthog.NewWithConfig(key, cfg)
}

// New loads the install ID and opens the PostHog client when analytics is enabled.
// A missing key, DO_NOT_TRACK, MASIR_ANALYTICS=off, or a development build disables it.
func New(opts Options) *Client {
	c := &Client{dir: opts.Dir, version: opts.Version, proxy: opts.Proxy}
	if id, err := installID(opts.Dir); err == nil {
		c.installID = id
	}
	if !c.shouldEnable(opts.Enabled) {
		return c
	}
	if err := c.open(); err != nil {
		return c // stay a no-op; analytics must never break the daemon
	}
	return c
}

func (c *Client) shouldEnable(setting bool) bool {
	if !setting { // the user's explicit opt-out always wins
		return false
	}
	return c.canEnable()
}

func (c *Client) canEnable() bool {
	if os.Getenv("DO_NOT_TRACK") != "" && os.Getenv("DO_NOT_TRACK") != "0" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MASIR_ANALYTICS"))) {
	case "off", "0", "false", "no":
		return false
	case "on", "1", "true", "yes":
		// Explicit on overrides the development-build guard below.
	default:
		if c.version == "dev" {
			return false
		}
	}
	if key := projectKey(); key == "" {
		return false
	}
	return c.installID != ""
}

func (c *Client) open() error {
	client, err := newPostHog(projectKey(), posthog.Config{
		Endpoint:        endpoint(),
		Interval:        15 * time.Second,
		BatchSize:       20,
		MaxQueueSize:    500,
		ShutdownTimeout: 2 * time.Second,
		MaxRetries:      posthog.Ptr(1),
		DisableGeoIP:    posthog.Ptr(true),
		IsServer:        posthog.Ptr(false),
		Compression:     posthog.CompressionGzip,
		Transport:       newTransport(c.proxy),
		Logger:          silentLogger{},
	})
	if err != nil {
		log.Printf("analytics disabled: %v", err)
		return err
	}
	c.mu.Lock()
	c.api, c.enabled = client, true
	c.mu.Unlock()
	return nil
}

// Enabled reports whether events are currently sent.
func (c *Client) Enabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled
}

// SetEnabled turns analytics on or off. Turning it off flushes and closes the client.
func (c *Client) SetEnabled(on bool) {
	c.mu.Lock()
	already := c.enabled
	c.mu.Unlock()
	if on == already {
		return
	}
	if on {
		if c.canEnable() {
			_ = c.open()
		}
		return
	}
	c.FlushAndClose()
	coreanalytics.ClearSpool(c.dir)
}

// Track records one anonymous event. Person profiles are explicitly disabled.
func (c *Client) Track(event string, props map[string]any) {
	c.trackAt(event, props, time.Time{})
}

// TrackAt records an event that happened at ts, e.g. after draining the spool.
func (c *Client) TrackAt(event string, props map[string]any, ts time.Time) {
	c.trackAt(event, props, ts)
}

func (c *Client) trackAt(event string, props map[string]any, ts time.Time) {
	if event == "" {
		return
	}
	c.mu.Lock()
	client, enabled := c.api, c.enabled
	c.mu.Unlock()
	if !enabled || client == nil {
		return
	}
	p := posthog.NewProperties()
	for k, v := range props {
		p.Set(k, v)
	}
	p.Set("$process_person_profile", false)
	_ = client.Enqueue(posthog.Capture{
		DistinctId: c.installID,
		Event:      event,
		Timestamp:  ts,
		Properties: p,
	})
}

// TrackError records a failure. Expected failures keep only the kind and fingerprint;
// unexpected ones also keep a scrubbed message.
func (c *Client) TrackError(event string, err error, props map[string]any, unexpected bool) {
	if err == nil {
		return
	}
	if props == nil {
		props = map[string]any{}
	}
	props["error_kind"] = ErrorKind(err)
	props["error_fingerprint"] = Fingerprint(err.Error())
	if code := GRPCCode(err); code != "" {
		props["grpc_code"] = code
	}
	if reason := ErrorReason(err); reason != "" {
		props["error_reason"] = reason
	}
	if unexpected {
		props["error_message"] = SanitizeMessage(err.Error())
	}
	c.Track(event, props)
}

// FlushAndClose sends what is queued and stops the client. Safe to call repeatedly.
func (c *Client) FlushAndClose() {
	c.mu.Lock()
	client := c.api
	c.api, c.enabled = nil, false
	c.mu.Unlock()
	if client != nil {
		_ = client.Close()
	}
}

// DrainSpool sends events the CLI left while the daemon was unreachable. If analytics
// is off, the spool is deleted instead.
func (c *Client) DrainSpool(ctx context.Context) {
	if !c.Enabled() {
		coreanalytics.ClearSpool(c.dir)
		return
	}
	_ = coreanalytics.DrainSpool(c.dir, func(e coreanalytics.Event) {
		if ctx.Err() != nil {
			return
		}
		props := e.Properties
		if props == nil {
			props = map[string]any{}
		}
		props["source"] = "cli_spool"
		c.TrackAt(e.Event, props, e.CreatedAt)
	})
}

func projectKey() string {
	if v := strings.TrimSpace(os.Getenv("MASIR_POSTHOG_KEY")); v != "" {
		return v
	}
	return strings.TrimSpace(apiKey)
}

func endpoint() string {
	if v := strings.TrimSpace(os.Getenv("MASIR_POSTHOG_HOST")); v != "" {
		return v
	}
	if strings.TrimSpace(host) != "" {
		return strings.TrimSpace(host)
	}
	return "https://eu.i.posthog.com"
}

type silentLogger struct{}

func (silentLogger) Debugf(string, ...interface{}) {}
func (silentLogger) Logf(string, ...interface{})   {}
func (silentLogger) Warnf(string, ...interface{})  {}
func (silentLogger) Errorf(string, ...interface{}) {}
