package main

import "testing"

// TestFlagDrawings checks the flags the app draws itself, which is what makes them show
// on systems whose emoji fonts have none.
func TestFlagDrawings(t *testing.T) {
	for _, code := range []string{"GB", "DE", "IR", "US", "CH", "EU"} {
		svg := flagDrawing(code)
		if svg == nil {
			t.Errorf("no flag drawing for %s", code)
			continue
		}
		if w, h := svg.Size(); w <= 0 || h <= 0 || w <= h {
			t.Errorf("%s: size %vx%v, want a landscape drawing", code, w, h)
		}
	}
	if flagDrawing("ZZ") != nil {
		t.Error("drew a flag for a code that has none")
	}
}

func TestFlagCode(t *testing.T) {
	for _, tc := range []struct{ flag, code string }{
		{"🇬🇧", "GB"},
		{"🇩🇪", "DE"},
		{"GB", "GB"},
		{"de", "DE"},
		{"", ""},
		{"United Kingdom", ""},
	} {
		if got := flagCode(tc.flag); got != tc.code {
			t.Errorf("flagCode(%q) = %q, want %q", tc.flag, got, tc.code)
		}
	}
}
