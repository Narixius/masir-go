#!/bin/sh
# Downloads the latest geo data from XTLS/Xray-rules-dat into build/geo, verified
# against the published SHA-256 sums, for bundling into release archives. The lite
# GeoIP is what Xray itself ships as geoip.dat.
set -eu

base=https://github.com/XTLS/Xray-rules-dat/releases/latest/download
out=build/geo
mkdir -p "$out"

fetch() { # remote name, local name
	curl -fsSL --retry 3 -o "$out/$2" "$base/$1"
	want=$(curl -fsSL --retry 3 "$base/$1.sha256sum" | awk '{print $1}')
	if command -v sha256sum >/dev/null; then
		got=$(sha256sum "$out/$2" | awk '{print $1}')
	else
		got=$(shasum -a 256 "$out/$2" | awk '{print $1}')
	fi
	if [ "$got" != "$want" ]; then
		echo "$1: checksum mismatch" >&2
		exit 1
	fi
	echo "$2 ok ($1)"
}

fetch geosite.dat geosite.dat
fetch geoip-lite.dat geoip.dat
