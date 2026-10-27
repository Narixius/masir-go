#!/bin/sh
# Downloads the latest geo data from Loyalsoldier/v2ray-rules-dat into build/geo,
# verified against the published SHA-256 sums, for bundling into release archives.
#
# A file already there is checked against the current sum and left alone when it
# matches. Downloads go to a temporary file first: a killed download must never leave
# a truncated geosite.dat/geoip.dat that a later build would reuse.
set -eu

base=https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download
out=build/geo
mkdir -p "$out"

sum() { # path
	if command -v sha256sum >/dev/null; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

fetch() { # remote name, local name
	want=$(curl -fsSL --retry 3 "$base/$1.sha256sum" | awk '{print $1}')
	if [ -f "$out/$2" ] && [ "$(sum "$out/$2")" = "$want" ]; then
		echo "$2 ok (cached)"
		return
	fi
	tmp="$out/$2.part"
	trap 'rm -f "$tmp"' EXIT INT TERM
	curl -fsSL --retry 3 -o "$tmp" "$base/$1"
	got=$(sum "$tmp")
	if [ "$got" != "$want" ]; then
		echo "$1: checksum mismatch" >&2
		exit 1
	fi
	mv -f "$tmp" "$out/$2"
	trap - EXIT INT TERM
	echo "$2 ok ($1)"
}

fetch geosite.dat geosite.dat
fetch geoip.dat geoip.dat
