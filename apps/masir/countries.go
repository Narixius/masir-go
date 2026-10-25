// Code generated from golang.org/x/text English region names for the country codes
// in geoip.dat; edit freely.

package main

import "strings"

// countryNames maps geoip.dat's two-letter country codes to English names.
var countryNames = map[string]string{
	"ad": "Andorra",
	"ae": "United Arab Emirates",
	"af": "Afghanistan",
	"ag": "Antigua & Barbuda",
	"ai": "Anguilla",
	"al": "Albania",
	"am": "Armenia",
	"ao": "Angola",
	"aq": "Antarctica",
	"ar": "Argentina",
	"as": "American Samoa",
	"at": "Austria",
	"au": "Australia",
	"aw": "Aruba",
	"ax": "Åland Islands",
	"az": "Azerbaijan",
	"ba": "Bosnia & Herzegovina",
	"bb": "Barbados",
	"bd": "Bangladesh",
	"be": "Belgium",
	"bf": "Burkina Faso",
	"bg": "Bulgaria",
	"bh": "Bahrain",
	"bi": "Burundi",
	"bj": "Benin",
	"bl": "St. Barthélemy",
	"bm": "Bermuda",
	"bn": "Brunei",
	"bo": "Bolivia",
	"bq": "Caribbean Netherlands",
	"br": "Brazil",
	"bs": "Bahamas",
	"bt": "Bhutan",
	"bv": "Bouvet Island",
	"bw": "Botswana",
	"by": "Belarus",
	"bz": "Belize",
	"ca": "Canada",
	"cc": "Cocos (Keeling) Islands",
	"cd": "Congo - Kinshasa",
	"cf": "Central African Republic",
	"cg": "Congo - Brazzaville",
	"ch": "Switzerland",
	"ci": "Côte d’Ivoire",
	"ck": "Cook Islands",
	"cl": "Chile",
	"cm": "Cameroon",
	"cn": "China",
	"co": "Colombia",
	"cr": "Costa Rica",
	"cu": "Cuba",
	"cv": "Cape Verde",
	"cw": "Curaçao",
	"cx": "Christmas Island",
	"cy": "Cyprus",
	"cz": "Czechia",
	"de": "Germany",
	"dj": "Djibouti",
	"dk": "Denmark",
	"dm": "Dominica",
	"do": "Dominican Republic",
	"dz": "Algeria",
	"ec": "Ecuador",
	"ee": "Estonia",
	"eg": "Egypt",
	"eh": "Western Sahara",
	"er": "Eritrea",
	"es": "Spain",
	"et": "Ethiopia",
	"fi": "Finland",
	"fj": "Fiji",
	"fk": "Falkland Islands",
	"fm": "Micronesia",
	"fo": "Faroe Islands",
	"fr": "France",
	"ga": "Gabon",
	"gb": "United Kingdom",
	"gd": "Grenada",
	"ge": "Georgia",
	"gf": "French Guiana",
	"gg": "Guernsey",
	"gh": "Ghana",
	"gi": "Gibraltar",
	"gl": "Greenland",
	"gm": "Gambia",
	"gn": "Guinea",
	"gp": "Guadeloupe",
	"gq": "Equatorial Guinea",
	"gr": "Greece",
	"gs": "South Georgia & South Sandwich Islands",
	"gt": "Guatemala",
	"gu": "Guam",
	"gw": "Guinea-Bissau",
	"gy": "Guyana",
	"hk": "Hong Kong SAR China",
	"hm": "Heard & McDonald Islands",
	"hn": "Honduras",
	"hr": "Croatia",
	"ht": "Haiti",
	"hu": "Hungary",
	"id": "Indonesia",
	"ie": "Ireland",
	"il": "Israel",
	"im": "Isle of Man",
	"in": "India",
	"io": "British Indian Ocean Territory",
	"iq": "Iraq",
	"ir": "Iran",
	"is": "Iceland",
	"it": "Italy",
	"je": "Jersey",
	"jm": "Jamaica",
	"jo": "Jordan",
	"jp": "Japan",
	"ke": "Kenya",
	"kg": "Kyrgyzstan",
	"kh": "Cambodia",
	"ki": "Kiribati",
	"km": "Comoros",
	"kn": "St. Kitts & Nevis",
	"kp": "North Korea",
	"kr": "South Korea",
	"kw": "Kuwait",
	"ky": "Cayman Islands",
	"kz": "Kazakhstan",
	"la": "Laos",
	"lb": "Lebanon",
	"lc": "St. Lucia",
	"li": "Liechtenstein",
	"lk": "Sri Lanka",
	"lr": "Liberia",
	"ls": "Lesotho",
	"lt": "Lithuania",
	"lu": "Luxembourg",
	"lv": "Latvia",
	"ly": "Libya",
	"ma": "Morocco",
	"mc": "Monaco",
	"md": "Moldova",
	"me": "Montenegro",
	"mf": "St. Martin",
	"mg": "Madagascar",
	"mh": "Marshall Islands",
	"mk": "Macedonia",
	"ml": "Mali",
	"mm": "Myanmar (Burma)",
	"mn": "Mongolia",
	"mo": "Macau SAR China",
	"mp": "Northern Mariana Islands",
	"mq": "Martinique",
	"mr": "Mauritania",
	"ms": "Montserrat",
	"mt": "Malta",
	"mu": "Mauritius",
	"mv": "Maldives",
	"mw": "Malawi",
	"mx": "Mexico",
	"my": "Malaysia",
	"mz": "Mozambique",
	"na": "Namibia",
	"nc": "New Caledonia",
	"ne": "Niger",
	"nf": "Norfolk Island",
	"ng": "Nigeria",
	"ni": "Nicaragua",
	"nl": "Netherlands",
	"no": "Norway",
	"np": "Nepal",
	"nr": "Nauru",
	"nu": "Niue",
	"nz": "New Zealand",
	"om": "Oman",
	"pa": "Panama",
	"pe": "Peru",
	"pf": "French Polynesia",
	"pg": "Papua New Guinea",
	"ph": "Philippines",
	"pk": "Pakistan",
	"pl": "Poland",
	"pm": "St. Pierre & Miquelon",
	"pn": "Pitcairn Islands",
	"pr": "Puerto Rico",
	"ps": "Palestinian Territories",
	"pt": "Portugal",
	"pw": "Palau",
	"py": "Paraguay",
	"qa": "Qatar",
	"re": "Réunion",
	"ro": "Romania",
	"rs": "Serbia",
	"ru": "Russia",
	"rw": "Rwanda",
	"sa": "Saudi Arabia",
	"sb": "Solomon Islands",
	"sc": "Seychelles",
	"sd": "Sudan",
	"se": "Sweden",
	"sg": "Singapore",
	"sh": "St. Helena",
	"si": "Slovenia",
	"sj": "Svalbard & Jan Mayen",
	"sk": "Slovakia",
	"sl": "Sierra Leone",
	"sm": "San Marino",
	"sn": "Senegal",
	"so": "Somalia",
	"sr": "Suriname",
	"ss": "South Sudan",
	"st": "São Tomé & Príncipe",
	"sv": "El Salvador",
	"sx": "Sint Maarten",
	"sy": "Syria",
	"sz": "Swaziland",
	"tc": "Turks & Caicos Islands",
	"td": "Chad",
	"tf": "French Southern Territories",
	"tg": "Togo",
	"th": "Thailand",
	"tj": "Tajikistan",
	"tk": "Tokelau",
	"tl": "Timor-Leste",
	"tm": "Turkmenistan",
	"tn": "Tunisia",
	"to": "Tonga",
	"tr": "Turkey",
	"tt": "Trinidad & Tobago",
	"tv": "Tuvalu",
	"tw": "Taiwan",
	"tz": "Tanzania",
	"ua": "Ukraine",
	"ug": "Uganda",
	"um": "U.S. Outlying Islands",
	"us": "United States",
	"uy": "Uruguay",
	"uz": "Uzbekistan",
	"va": "Vatican City",
	"vc": "St. Vincent & Grenadines",
	"ve": "Venezuela",
	"vg": "British Virgin Islands",
	"vi": "U.S. Virgin Islands",
	"vn": "Vietnam",
	"vu": "Vanuatu",
	"wf": "Wallis & Futuna",
	"ws": "Samoa",
	"xk": "Kosovo",
	"ye": "Yemen",
	"yt": "Mayotte",
	"za": "South Africa",
	"zm": "Zambia",
	"zw": "Zimbabwe",
}

// countryAdjectives are the forms used in website category labels, e.g. "Russian
// websites". Countries without an entry read "<Name> websites".
var countryAdjectives = map[string]string{
	"ae": "Emirati", "af": "Afghan", "al": "Albanian", "am": "Armenian", "ar": "Argentine",
	"at": "Austrian", "au": "Australian", "az": "Azerbaijani", "ba": "Bosnian", "bd": "Bangladeshi",
	"be": "Belgian", "bg": "Bulgarian", "br": "Brazilian", "by": "Belarusian", "ca": "Canadian",
	"ch": "Swiss", "cl": "Chilean", "cn": "Chinese", "co": "Colombian", "cu": "Cuban",
	"cy": "Cypriot", "cz": "Czech", "de": "German", "dk": "Danish", "dz": "Algerian",
	"ec": "Ecuadorian", "ee": "Estonian", "eg": "Egyptian", "es": "Spanish", "et": "Ethiopian",
	"fi": "Finnish", "fr": "French", "gb": "British", "ge": "Georgian", "gr": "Greek",
	"hk": "Hong Kong", "hr": "Croatian", "hu": "Hungarian", "id": "Indonesian", "ie": "Irish",
	"il": "Israeli", "in": "Indian", "iq": "Iraqi", "ir": "Iranian", "is": "Icelandic",
	"it": "Italian", "jo": "Jordanian", "jp": "Japanese", "ke": "Kenyan", "kg": "Kyrgyz",
	"kh": "Cambodian", "kp": "North Korean", "kr": "South Korean", "kw": "Kuwaiti", "kz": "Kazakh",
	"lb": "Lebanese", "lk": "Sri Lankan", "lt": "Lithuanian", "lu": "Luxembourgish", "lv": "Latvian",
	"ly": "Libyan", "ma": "Moroccan", "md": "Moldovan", "me": "Montenegrin", "mk": "Macedonian",
	"mm": "Burmese", "mn": "Mongolian", "mx": "Mexican", "my": "Malaysian", "ng": "Nigerian",
	"nl": "Dutch", "no": "Norwegian", "np": "Nepali", "nz": "New Zealander", "om": "Omani",
	"pe": "Peruvian", "ph": "Filipino", "pk": "Pakistani", "pl": "Polish", "pt": "Portuguese",
	"qa": "Qatari", "ro": "Romanian", "rs": "Serbian", "ru": "Russian", "sa": "Saudi",
	"se": "Swedish", "sg": "Singaporean", "si": "Slovenian", "sk": "Slovak", "sy": "Syrian",
	"th": "Thai", "tm": "Turkmen", "tn": "Tunisian", "tr": "Turkish", "tw": "Taiwanese",
	"ua": "Ukrainian", "us": "American", "uy": "Uruguayan", "uz": "Uzbek", "ve": "Venezuelan",
	"vn": "Vietnamese", "ye": "Yemeni", "za": "South African",
}

// countryAliases add other names people type when searching, without changing labels.
var countryAliases = map[string]string{
	"ae": "uae emirates", "cz": "czechia", "gb": "uk britain", "kp": "north korea",
	"kr": "south korea", "mm": "burma", "nl": "holland", "us": "usa america",
}

// normalizeCountryCode turns the spellings geo data and people use into the two-letter
// code in countryNames. geoip.dat uses gb for the United Kingdom, not uk.
func normalizeCountryCode(code string) string {
	code = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(code), "!"))
	if code == "uk" {
		return "gb"
	}
	return code
}

// countryAdjective is the adjective for a country code, or the country's name.
func countryAdjective(code string) string {
	code = normalizeCountryCode(code)
	if a := countryAdjectives[code]; a != "" {
		return a
	}
	if n := countryNames[code]; n != "" {
		return n
	}
	return ""
}

// countrySearch is the text a country search also matches: name, adjective and common
// aliases. Empty when value names no country.
func countrySearch(value string) string {
	code := value
	for _, prefix := range []string{"geosite:", "geoip:"} {
		if rest, ok := strings.CutPrefix(code, prefix); ok {
			code = rest
			break
		}
	}
	if i := strings.LastIndex(code, "-"); i >= 0 {
		code = code[i+1:]
	}
	code = normalizeCountryCode(code)
	name := countryNames[code]
	if name == "" {
		return ""
	}
	parts := []string{name}
	if a := countryAdjectives[code]; a != "" {
		parts = append(parts, a)
	}
	if a := countryAliases[code]; a != "" {
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}
