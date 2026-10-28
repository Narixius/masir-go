package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Clients reach the internet directly, then through the running proxy when direct
// fails, as subscriptions do: GitHub is often filtered where masir is used.
type Clients struct {
	Direct    *http.Client
	Proxy     *http.Client // nil when there's no proxy to try
	UserAgent string
}

func (c Clients) userAgent() string {
	if c.UserAgent == "" {
		return "masir"
	}
	return c.UserAgent
}

// do runs fn with the direct client, then with the proxy client if that failed and
// ctx is still live.
func (c Clients) do(ctx context.Context, fn func(*http.Client) error) error {
	err := fn(c.Direct)
	if err == nil || c.Proxy == nil || ctx.Err() != nil {
		return err
	}
	if perr := fn(c.Proxy); perr != nil {
		return fmt.Errorf("%w (through the proxy: %v)", err, perr)
	}
	return nil
}

// secureURL keeps updates on HTTPS. A test server on the loopback interface is the only
// plain-HTTP exception; a release URL must never arrive over a cleartext network.
func secureURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
		if host == "localhost" {
			return nil
		}
	}
	return fmt.Errorf("refusing %s: updates need HTTPS", raw)
}

// download fetches url into path. A partial file already at path is continued with a
// Range request, which is how a paused job resumes, including after switching from the
// direct client to the proxy. progress gets the bytes written each time.
func download(ctx context.Context, c Clients, url, path string, progress func(n int64)) error {
	if err := secureURL(url); err != nil {
		return err
	}
	return c.do(ctx, func(hc *http.Client) error {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		have, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			return err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", c.userAgent())
		if have > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
		}
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusPartialContent:
		case http.StatusOK: // no resume support, or nothing to resume: start over
			if have > 0 {
				progress(-have)
			}
			if err := f.Truncate(0); err != nil {
				return err
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return err
			}
		case http.StatusRequestedRangeNotSatisfiable:
			return nil // already complete; the checksum decides
		default:
			return fmt.Errorf("%s: %s", lastPart(url), resp.Status)
		}
		_, err = io.Copy(f, &progressReader{r: resp.Body, progress: progress})
		return err
	})
}

type progressReader struct {
	r        io.Reader
	progress func(int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.progress(int64(n))
	}
	return n, err
}

// fetchSmall reads a small file, such as a checksum list, into memory.
func fetchSmall(ctx context.Context, c Clients, url string) ([]byte, error) {
	if err := secureURL(url); err != nil {
		return nil, err
	}
	var body []byte
	err := c.do(ctx, func(hc *http.Client) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", c.userAgent())
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s: %s", lastPart(url), resp.Status)
		}
		body, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return err
	})
	return body, err
}

// checksum finds name's SHA-256 in a sha256sum-style list ("<hex>  name" per line;
// a single-file list may have just the hash).
func checksum(list []byte, name string) (string, error) {
	lines := strings.Split(strings.TrimSpace(string(list)), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		file := ""
		if len(fields) > 1 {
			file = strings.TrimPrefix(fields[len(fields)-1], "*")
		}
		if file == name || (file == "" && len(lines) == 1) {
			if len(fields[0]) != sha256.Size*2 {
				break
			}
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", name)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func verify(path, want string) error {
	got, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if got != want {
		return errors.New(lastPart(path) + " doesn't match its checksum; it was corrupted or tampered with in transit")
	}
	return nil
}

func lastPart(s string) string {
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		return s[i+1:]
	}
	return s
}

func fileSize(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}
