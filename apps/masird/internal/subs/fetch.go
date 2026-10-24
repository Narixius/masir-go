package subs

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	userAgent    = "v2rayNG/2.0.15" // panels pick the response format by client; this one is widely served
	maxBodyBytes = 10 << 20
)

// Usage is the provider's subscription-userinfo header.
type Usage struct {
	Upload   int64     `json:"upload"`
	Download int64     `json:"download"`
	Total    int64     `json:"total"`           // 0 means unlimited
	Expire   time.Time `json:"expire,omitzero"` // zero means never
}

type fetched struct {
	body     []byte
	usage    *Usage
	interval time.Duration // from profile-update-interval; 0 if absent
	title    string        // from profile-title
}

func fetch(ctx context.Context, client *http.Client, url string) (fetched, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fetched{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return fetched{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fetched{}, fmt.Errorf("provider returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return fetched{}, fmt.Errorf("read body: %w", err)
	}
	if len(body) > maxBodyBytes {
		return fetched{}, errors.New("subscription is larger than 10MB")
	}
	return fetched{
		body:     body,
		usage:    parseUsage(resp.Header.Get("Subscription-Userinfo")),
		interval: parseInterval(resp.Header.Get("Profile-Update-Interval")),
		title:    parseTitle(resp.Header.Get("Profile-Title")),
	}, nil
}

// parseUsage reads "upload=1; download=2; total=3; expire=1700000000".
func parseUsage(h string) *Usage {
	if h == "" {
		return nil
	}
	u := &Usage{}
	found := false
	for part := range strings.SplitSeq(h, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64) // some panels send 1.5e10
		if err != nil {
			continue
		}
		found = true
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "upload":
			u.Upload = int64(n)
		case "download":
			u.Download = int64(n)
		case "total":
			u.Total = int64(n)
		case "expire":
			if n > 0 {
				u.Expire = time.Unix(int64(n), 0).UTC()
			}
		}
	}
	if !found {
		return nil
	}
	return u
}

// parseInterval reads profile-update-interval, which is in hours.
func parseInterval(h string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(h))
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Hour
}

// parseTitle reads profile-title, which may be "base64:<encoded>".
func parseTitle(h string) string {
	h = strings.TrimSpace(h)
	if enc, ok := strings.CutPrefix(h, "base64:"); ok {
		b, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	return h
}
