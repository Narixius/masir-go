# Security

## Reporting a problem

Please report vulnerabilities privately, through GitHub's "Report a vulnerability" button
on this repository's Security tab, rather than in a public issue.

## What masir protects

The daemon can run as root (the system service, or the session masir starts for TUN).
It serves one regular user. The aim is that nothing else on the machine can use it to
gain root, and that nobody can make it run code you didn't choose.

- **Who can talk to it.** The socket accepts only the user it serves, and root: the
  kernel's peer credentials are checked on every connection. A user daemon keeps its
  socket (mode 0600) in the user's folder. The root daemon keeps its socket in
  `/var/run/masir`, which only root can write to, so no link planted in your own
  folders can steer it.
- **Files in your folder.** Your settings, rules and subscriptions live in your own
  folder, which you can modify. The root daemon opens that folder once, checks that it
  is yours, and does all its reading and writing through that handle: a symlink, a hard
  link or a `..` that leads out of it is refused.
- **The binary root runs.** `masir daemon enable` installs a copy of masir into
  `/usr/local/lib/masir` (the install folder on Windows), where only root can change it,
  and the service runs that copy.
- **Updates.** `checksums.txt` of every release is signed with minisign, and `masir
  update` installs nothing whose signature doesn't verify against the key built into
  masir. A root daemon stages the download in a folder only root can write to, so it
  can't be swapped between checking and installing. The service updates its own
  root-owned copy, and replaces the copy your CLI runs only when only root controls where
  it lives; otherwise `masir update` finishes the job as you. A TUN session started from
  your own copy of masir updates that copy in place, writing a new file it creates
  exclusively, never one reached through a link.
- **Configs.** Subscriptions and custom inbounds and outbounds are Xray configs, run by
  a root daemon. While it runs as root, masir refuses a config that makes Xray read or
  write local files (TLS key logs, certificate and key files, a masquerade directory, a
  socket file).

## What it doesn't protect

- **Your own account.** Anything running as you can talk to the daemon, change your
  rules and read your subscriptions, as you can. The goal is that it can't become root,
  not that it can't be you.
- **The proxy port has no authentication.** See the README: with `allowLan` on, anyone
  on your network can use it.
- **`sudo masir ...` runs the file you ran.** The root session masir starts for TUN runs
  the `masir` you invoked, wherever it is. If that file is in a folder other software
  running as you can write to, run `masir daemon enable` once instead: the service runs
  its own root-owned copy.
- **Geo data** comes from a third party (see the README). Xray loads it, and the old
  files are put back if Xray rejects the new ones, but it isn't signed.
- **Windows** doesn't pin your folder yet, because a profile folder is often a junction
  or a redirected known folder, which a pinned handle refuses to follow.
- **Analytics** are described in [docs/telemetry.md](docs/telemetry.md).
