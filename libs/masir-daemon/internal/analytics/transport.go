package analytics

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

var errBlocked = errors.New("analytics request was blocked by a captive portal")

// Proxy is the running Xray core. Analytics goes direct first, then through this when
// the direct route is blocked.
type Proxy interface {
	Running() bool
	Dial(ctx context.Context, network, addr string) (net.Conn, error)
}

// retryTransport sends a PostHog batch directly, then through the proxy when the direct
// attempt fails or a censorship block page comes back.
type retryTransport struct {
	direct *http.Transport
	proxy  *http.Transport
	core   Proxy
}

func newTransport(core Proxy) http.RoundTripper {
	t := &retryTransport{
		direct: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 8 * time.Second,
			ForceAttemptHTTP2:     true,
		},
		core: core,
	}
	if core != nil {
		t.proxy = &http.Transport{
			Proxy:                 nil,
			DialContext:           core.Dial,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 8 * time.Second,
			ForceAttemptHTTP2:     true,
		}
	}
	return t
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := readBody(req)
	if err != nil {
		return t.direct.RoundTrip(req)
	}
	resp, err := t.send(t.direct, req, body)
	if err == nil && !blocked(resp) {
		return resp, nil
	}
	if t.proxy == nil || t.core == nil || !t.core.Running() {
		if resp != nil {
			resp.Body.Close()
		}
		if err != nil {
			return nil, err
		}
		return nil, errBlocked
	}
	if resp != nil {
		resp.Body.Close()
	}
	return t.send(t.proxy, req, body)
}

func (t *retryTransport) send(tr *http.Transport, req *http.Request, body []byte) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	return tr.RoundTrip(r)
}

func readBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	defer req.Body.Close()
	if req.GetBody != nil {
		rc, err := req.GetBody()
		if err == nil {
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, 1<<20))
		}
	}
	return io.ReadAll(io.LimitReader(req.Body, 1<<20))
}

// blocked recognizes a firewall or captive-portal page. PostHog answers with JSON, so
// an HTML response (even a 2xx block page) should be retried through the proxy.
func blocked(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	return strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/html")
}
