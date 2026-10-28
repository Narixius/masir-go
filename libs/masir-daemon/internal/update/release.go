// Package update installs new versions of masir and fresh geo data. Every update runs
// as a job (see Runner) that a UI can watch, pause, resume and stop.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	// ReleaseRepo publishes masir's releases: one archive per OS and architecture,
	// plus checksums.txt.
	ReleaseRepo = "narixius/masir-go"
	// GeoRepo publishes geosite.dat and geoip.dat, each with a .sha256sum: the v2fly
	// domain list with many more domains added (ads, regional lists), and every country.
	GeoRepo = "Loyalsoldier/v2ray-rules-dat"
)

// apiBase is GitHub's API; tests point it at a local server.
var apiBase = "https://api.github.com"

type Release struct {
	Tag       string    `json:"tag_name"`
	URL       string    `json:"html_url"`
	Published time.Time `json:"published_at"`
	Assets    []Asset   `json:"assets"`
}

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

func (r *Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Latest is repo's newest published release.
func Latest(ctx context.Context, c Clients, repo string) (*Release, error) {
	rel, _, _, err := LatestConditional(ctx, c, repo, "")
	return rel, err
}

// LatestConditional is Latest, telling GitHub which release it already has (etag) so an
// unchanged release comes back as a 304 with no body. It still counts against the
// unauthenticated rate limit, so callers keep a cadence rather than polling.
// notModified reports that the caller's copy is still current, and rel is nil then.
func LatestConditional(ctx context.Context, c Clients, repo, etag string) (rel *Release, newETag string, notModified bool, err error) {
	var body []byte
	err = c.do(ctx, func(hc *http.Client) error {
		notModified, newETag, body = false, "", nil
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/repos/"+repo+"/releases/latest", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", c.userAgent())
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusNotModified && etag != "":
			notModified, newETag = true, etag
			return nil
		case resp.StatusCode == http.StatusNotModified:
			// Nothing was sent to compare with: comparing again would answer the same.
			return errors.New("GitHub answered 304 to a check that sent no tag")
		case resp.StatusCode == http.StatusNotFound:
			return fmt.Errorf("%s has no releases yet", repo)
		case resp.StatusCode != http.StatusOK:
			return fmt.Errorf("GitHub answered %s", resp.Status)
		}
		newETag = resp.Header.Get("ETag")
		body, err = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		return err
	})
	if err != nil {
		return nil, "", false, err
	}
	if notModified {
		return nil, newETag, true, nil
	}
	var r Release
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, "", false, err
	}
	if r.Tag == "" {
		return nil, "", false, errors.New("GitHub returned a release without a tag")
	}
	return &r, newETag, false, nil
}

// Newer reports whether release tag latest is newer than version current. Development
// builds ("dev", or anything that isn't a version) are never older than a release.
func Newer(latest, current string) bool {
	l, c := canonical(latest), canonical(current)
	return semver.IsValid(l) && semver.IsValid(c) && semver.Compare(l, c) > 0
}

// IsRelease reports whether version came from a release build.
func IsRelease(version string) bool { return semver.IsValid(canonical(version)) }

func canonical(v string) string {
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

// VersionTag checks that tag is a release version before it is used to name an archive
// and a work directory. GitHub tags are external input; a tag such as "../../x" must
// never reach filepath.Join.
func VersionTag(tag string) (string, error) {
	t := strings.TrimSpace(tag)
	if t == "" || strings.ContainsAny(t, `/\`) || strings.Contains(t, "..") || !semver.IsValid(canonical(t)) {
		return "", fmt.Errorf("release tag %q isn't a version", tag)
	}
	return t, nil
}

// safeDirName rejects a tag that can't be a single directory name. Third-party releases,
// such as the geo data, use date-like tags rather than versions.
func safeDirName(tag string) (string, error) {
	t := strings.TrimSpace(tag)
	if t == "" || t == "." || strings.ContainsAny(t, `/\`) || strings.Contains(t, "..") {
		return "", fmt.Errorf("release tag %q can't name a directory", tag)
	}
	return t, nil
}
