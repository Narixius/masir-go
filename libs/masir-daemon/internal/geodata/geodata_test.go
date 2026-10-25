package geodata

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	xgeo "github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
)

func TestCategories(t *testing.T) {
	site := &xgeo.GeoSiteList{Entry: []*xgeo.GeoSite{
		{Code: "CATEGORY-AI-CHAT", Domain: []*xgeo.Domain{{Value: "openai.com"}, {Value: "claude.ai"}}},
		{Code: "google", Domain: []*xgeo.Domain{{Value: "google.com"}}},
	}}
	ip := &xgeo.GeoIPList{Entry: []*xgeo.GeoIP{{Code: "DE", Cidr: []*xgeo.CIDR{{Ip: []byte{1, 2, 3, 0}, Prefix: 24}}}}}
	dir := t.TempDir()
	for name, m := range map[string]proto.Message{"geosite.dat": site, "geoip.dat": ip} {
		b, _ := proto.Marshal(m)
		os.WriteFile(filepath.Join(dir, name), b, 0o644)
	}

	got, err := Categories(filepath.Join(dir, "geosite.dat"))
	if err != nil || len(got) != 2 || got[0] != (Category{"category-ai-chat", 2}) || got[1] != (Category{"google", 1}) {
		t.Fatalf("geosite = %+v, %v", got, err)
	}
	got, err = Categories(filepath.Join(dir, "geoip.dat"))
	if err != nil || len(got) != 1 || got[0] != (Category{"de", 1}) {
		t.Fatalf("geoip = %+v, %v", got, err)
	}

	// A rewritten file is scanned again, not served from the cache.
	site.Entry = site.Entry[:1]
	b, _ := proto.Marshal(site)
	os.WriteFile(filepath.Join(dir, "geosite.dat"), append(b, []byte{}...), 0o644)
	os.Chtimes(filepath.Join(dir, "geosite.dat"), timeLater(), timeLater())
	if got, _ := Categories(filepath.Join(dir, "geosite.dat")); len(got) != 1 {
		t.Fatalf("after a rewrite: %+v", got)
	}

	if _, err := Categories(filepath.Join(dir, "missing.dat")); err == nil {
		t.Error("a missing file is an error")
	}
	os.WriteFile(filepath.Join(dir, "bad.dat"), []byte{0x0a, 0xff}, 0o644)
	if _, err := Categories(filepath.Join(dir, "bad.dat")); err == nil {
		t.Error("a malformed file is an error")
	}
}

func timeLater() (t time.Time) { return time.Now().Add(time.Hour) }
