# The Masir desktop app as an RPM: the same layout the .deb MyGo builds ships, plus
# the two things the .deb leaves out — /usr/bin links, so `masir` and the launcher
# resolve without a hand-edited PATH, and libayatana-appindicator, which the binary
# dlopens for the tray and the .deb's Depends doesn't mention.
#
# Built by scripts/build-rpm.sh from the portable release archive; nothing is
# compiled here, so it cross-packages an ARM64 build on an x64 machine fine.
#
# debuginfo and stripping are off on purpose: the binaries come from `make gui`,
# already built (and on the strip boundary) upstream, and this package should ship
# the release bytes exactly as the archive has them.

%global debug_package %{nil}
%global __strip /bin/true

Name:           masir-desktop
# The version comes from the archive's name; scripts/build-rpm.sh passes it.
Version:        %{?masir_version}%{!?masir_version:0.0.0}
Release:        1%{?dist}
Summary:        A fast client for Xray: subscriptions, routing rules and a system-wide tunnel
License:        LicenseRef-Proprietary
URL:            https://github.com/Narixius/masir-go
Source0:        masir-desktop-portable-linux-%{version}-%{?mygo_arch}%{!?mygo_arch:amd64}.tar.gz

# gtk3 is linked; the tray icon library is dlopened at runtime, so without it the
# tray silently degrades instead of failing the dependency check. polkit supplies
# pkexec, which the app uses to ask for root (service install, TUN).
Requires:       gtk3
Requires:       libayatana-appindicator-gtk3
Requires:       polkit

%description
Masir is a desktop application: the tray app for the masir daemon. It turns masir on
and off, picks a server with live latency, shows the rules, adds subscriptions, and
has settings. All the logic lives in the daemon; the app only talks to it.

%prep
# The archive is flat — no top-level directory — so unpack it into a fresh build dir.
%setup -c -T
tar -xf %{SOURCE0}

%build
# Nothing to build; the archive ships the compiled app.

%install
rm -rf %{buildroot}

# The app beside its CLI and geo data, exactly as the archive lays it out (the .deb
# ships the same tree; AppIcon.rsrc and FinderInfo.bin are macOS metadata that travels
# with every archive and is harmless to keep, so both packages carry identical bytes).
mkdir -p %{buildroot}/opt/masir-desktop/bin
cp -p masir-desktop icon.png flags.LICENSE.txt AppIcon.rsrc FinderInfo.bin \
	%{buildroot}/opt/masir-desktop/
cp -p bin/masir bin/geoip.dat bin/geosite.dat %{buildroot}/opt/masir-desktop/bin/
chmod 755 %{buildroot}/opt/masir-desktop/masir-desktop \
	%{buildroot}/opt/masir-desktop/bin/masir

# Both binaries on PATH. The .deb ships neither link, so after a .deb install `masir`
# only works from a shell somebody remembered to configure, and the desktop entry's
# bare Exec=masir-desktop resolves only by luck of the session's PATH.
install -d %{buildroot}%{_bindir}
ln -s /opt/masir-desktop/masir-desktop %{buildroot}%{_bindir}/masir-desktop
ln -s /opt/masir-desktop/bin/masir %{buildroot}%{_bindir}/masir

# Desktop entry as the archive writes it (its Exec now resolves via the link above).
install -D -p -m 644 masir-desktop.desktop \
	%{buildroot}%{_datadir}/applications/masir-desktop.desktop

# The archive carries one 1024px icon; the hicolor theme indexes 256 and 512 — the
# same two sizes the .deb ships — so resize rather than installing an unindexed size.
for size in 256 512; do
	install -d %{buildroot}%{_datadir}/icons/hicolor/${size}x${size}/apps
	convert masir-desktop.png -resize ${size}x${size} \
		"%{buildroot}%{_datadir}/icons/hicolor/${size}x${size}/apps/masir-desktop.png"
done

# No %changelog: the version here is whatever archive was built, and a hand-written
# entry would go stale on the next release. The release notes are the changelog.

%files
/opt/masir-desktop/
%{_bindir}/masir
%{_bindir}/masir-desktop
%{_datadir}/applications/masir-desktop.desktop
%{_datadir}/icons/hicolor/256x256/apps/masir-desktop.png
%{_datadir}/icons/hicolor/512x512/apps/masir-desktop.png
