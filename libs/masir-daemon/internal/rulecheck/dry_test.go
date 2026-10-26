package rulecheck

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDryReportsWhatThisBuildCantBuild(t *testing.T) {
	d := data(t, []string{"ir", "de"}, []string{"google"})
	dir := d.GeoIP[:strings.LastIndex(d.GeoIP, "/")]
	req, _ := json.Marshal(Request{AssetDir: dir, Rules: []Rule{
		{Tag: "ok", Match: json.RawMessage(`{"ip":["geoip:de"],"port":"443"}`)},
		{Tag: "gone", Match: json.RawMessage(`{"ip":["geoip:in-ir"]}`)},
		{Tag: "bad-port", Match: json.RawMessage(`{"port":"not-a-port"}`)},
		{Tag: "not-json", Match: json.RawMessage(`"x"`)},
	}})
	var out bytes.Buffer
	if err := Dry(bytes.NewReader(req), &out); err != nil {
		t.Fatal(err)
	}
	var got []Report
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	byTag := map[string]Report{}
	for _, r := range got {
		byTag[r.Tag] = r
	}
	if len(got) != 3 || byTag["ok"].Tag != "" {
		t.Fatalf("report = %+v", got)
	}
	if len(byTag["gone"].Missing) != 1 || byTag["gone"].Missing[0] != "geoip:in-ir" {
		t.Errorf("gone = %+v", byTag["gone"])
	}
	if byTag["bad-port"].Message == "" || byTag["not-json"].Message == "" {
		t.Errorf("the rules Xray itself refuses must be reported: %+v", got)
	}
}

func TestDryWithNoProblemsWritesAnEmptyArray(t *testing.T) {
	d := data(t, []string{"ir"}, []string{"google"})
	dir := d.GeoIP[:strings.LastIndex(d.GeoIP, "/")]
	req, _ := json.Marshal(Request{AssetDir: dir, Rules: []Rule{{Tag: "ok", Match: json.RawMessage(`{"port":"22"}`)}}})
	var out bytes.Buffer
	if err := Dry(bytes.NewReader(req), &out); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("output = %q, want []", out.String())
	}
	if err := Dry(strings.NewReader("not json"), &out); err == nil {
		t.Error("garbage input is an error")
	}
}
