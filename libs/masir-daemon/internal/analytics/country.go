package analytics

import "strings"

const regionalA = 0x1F1E6

// CountryFromName reads the first flag emoji in a config name. It returns an uppercase
// ISO code, or "" when the name has no flag. This is deliberately flag-only: no IP
// lookup and no guessing from arbitrary text.
func CountryFromName(name string) string {
	runes := []rune(name)
	for i := 0; i+1 < len(runes); i++ {
		if !isRegional(runes[i]) || !isRegional(runes[i+1]) {
			continue
		}
		a := byte(runes[i]-regionalA) + 'A'
		b := byte(runes[i+1]-regionalA) + 'A'
		code := string([]byte{a, b})
		if code == "UK" { // geo data and providers use GB
			code = "GB"
		}
		return code
	}
	return ""
}

func isRegional(r rune) bool { return r >= regionalA && r <= regionalA+25 }

// CountriesFromNames returns the distinct flags across config names.
func CountriesFromNames(names []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, name := range names {
		if code := CountryFromName(name); code != "" && !seen[code] {
			seen[code] = true
			out = append(out, code)
		}
	}
	return out
}

// NormalizeCountry turns a config-name flag or a lowercase geo code into an uppercase
// ISO code for analytics.
func NormalizeCountry(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "UK" {
		return "GB"
	}
	return code
}
