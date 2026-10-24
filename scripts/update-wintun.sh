#!/bin/sh
# Refreshes the Wintun driver embedded in masir.exe (Xray's TUN loads it on Windows) in
# libs/masir-daemon/internal/wintun. Run it when moving to a new Wintun release, then
# update version and sha256 below to what wintun.net publishes. Its license allows
# shipping wintun.dll with software that uses only the Wintun API, as Xray does.
set -eu

version=0.14.1
sha256=07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51
out=libs/masir-daemon/internal/wintun
mkdir -p "$out"

zip="$out/wintun-$version.zip"
curl -fsSL --retry 3 -o "$zip" "https://www.wintun.net/builds/wintun-$version.zip"
if command -v sha256sum >/dev/null; then
	got=$(sha256sum "$zip" | awk '{print $1}')
else
	got=$(shasum -a 256 "$zip" | awk '{print $1}')
fi
if [ "$got" != "$sha256" ]; then
	echo "wintun-$version.zip: checksum mismatch" >&2
	exit 1
fi

rm -rf "$out/x"
unzip -q "$zip" -d "$out/x"
for arch in amd64 arm64; do
	cp "$out/x/wintun/bin/$arch/wintun.dll" "$out/wintun_$arch.dll"
done
cp "$out/x/wintun/LICENSE.txt" "$out/LICENSE.txt"
rm -rf "$out/x" "$zip"
echo "wintun $version ok"
