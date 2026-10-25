package analytics

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

var (
	urlRe    = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]+`)
	ipv4Re   = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	ipv6Re   = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{1,4}:){2,}[0-9a-f:.]*\b`)
	emailRe  = regexp.MustCompile(`\b[^\s@]+@[^\s@]+\.[^\s@]+\b`)
	uuidRe   = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	domainRe = regexp.MustCompile(`(?i)\b(?:[a-z0-9-]+\.)+[a-z]{2,}\b`)
	unixPath = regexp.MustCompile(`(?:/[\w.@%+-]+){2,}/?`)
	winPath  = regexp.MustCompile(`(?i)\b[A-Z]:\\[^\s"'<>]+`)
	tokenRe  = regexp.MustCompile(`\b[A-Za-z0-9+/_-]{32,}={0,2}\b`)
	spaceRe  = regexp.MustCompile(`\s+`)
)

// SanitizeMessage removes things that could identify a person or a private setup from
// an error message before it is sent. It keeps enough text to debug the category.
func SanitizeMessage(s string) string {
	s = urlRe.ReplaceAllString(s, "<url>")
	s = uuidRe.ReplaceAllString(s, "<id>")
	s = emailRe.ReplaceAllString(s, "<email>")
	s = winPath.ReplaceAllString(s, "<path>")
	s = unixPath.ReplaceAllString(s, "<path>")
	s = domainRe.ReplaceAllString(s, "<domain>")
	s = ipv4Re.ReplaceAllString(s, "<ip>")
	s = ipv6Re.ReplaceAllString(s, "<ip>")
	s = tokenRe.ReplaceAllString(s, "<token>")
	s = spaceRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// Fingerprint groups repeated errors without keeping their text.
func Fingerprint(s string) string {
	sum := sha256.Sum256([]byte(SanitizeMessage(s)))
	return hex.EncodeToString(sum[:6])
}
