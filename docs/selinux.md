# SELinux

Masir runs as a system service on Linux, and on Fedora, RHEL and other SELinux
distros the label on its binary decides whether the daemon is allowed to use the
network at all.

## The symptom

The service is active, `masir status` shows the daemon running — but every
connection fails:

```
Couldn't turn masir on: ... dial tcp ...: connect: permission denied
```

The journal has the real story, one line per blocked attempt:

```
AVC avc: denied { name_connect } for pid=... comm="masir" dest=443
  scontext=system_u:system_r:init_t:s0 tcontext=system_u:object_r:http_port_t:s0
  tclass=tcp_socket
```

Read it: the daemon (`comm="masir"`) tried to reach port 443 and SELinux said no.

## Why it happens

SELinux puts every process in a *domain*, and the policy gives each domain its
permissions. Two labels decide which domain the daemon lands in:

1. **Where the binary lives.** `masir daemon enable` installs the service's copy
   into `/usr/local/lib/masir/`. Files created under `/usr/local/lib` carry the
   default label `lib_t` — a type for libraries, not the entry point of any
   service domain.
2. **The result.** systemd starts a binary that has no domain of its own in the
   confined template domain `init_t`. The policy allows `init_t` to run, write
   its own files and stop — but **not to open sockets**. So the daemon is
   healthy and useless at the same time: `name_connect` is denied for every
   destination, `updates are off: mkdir .../updates: permission denied` shows up
   in the journal for the same reason.

Services whose binary carries `bin_t` — the label files in `/usr/bin` and
`/usr/local/bin` get — start in `unconfined_service_t` instead, where a proxy
daemon belongs: allowed to do what the user asked it to do. On a default Fedora
install `earlyoom`, `tailscaled` and `v2raya` all run there for exactly this
reason.

## The fix

`masir daemon enable` (and every service update afterwards) now labels the
service binary itself:

```sh
chcon -t bin_t /usr/local/lib/masir/masir          # effective now
semanage fcontext -a -t bin_t \
  /usr/local/lib/masir/masir                       # survives a relabel
```

`chcon` is required; the `semanage` rule is best effort — it needs
`policycoreutils-python-utils`, and without it the label holds until the next
full relabel, which the next install or update re-applies anyway. On a machine
without SELinux neither command runs.

To fix a service installed by an older masir, reinstall it once:

```sh
masir daemon enable
```

To check the result:

```sh
ps -eZ | grep masir          # expect unconfined_service_t, not init_t
journalctl _TRANSPORT=audit | grep denied | tail   # expect silence
```

## If you see it anyway

1. `ps -eZ | grep masir` — is the daemon in `init_t`? Then the binary is not
   labeled; run `sudo chcon -t bin_t /usr/local/lib/masir/masir` and
   `sudo systemctl restart masird`.
2. Is the label right but the domain still confined? Check
   `journalctl _TRANSPORT=audit --since -1h | grep denied` for the exact
   denial and match its `scontext` to the process — the denial names what is
   actually blocked, not what the error message guessed.
3. `getenforce` — in `Permissive` mode nothing is blocked; the problem is
   elsewhere.

Never `semanage permissive -a init_t` to make this go away: `init_t` is the
catch-all template domain every unlabeled service shares, and making it
permissive turns off enforcement for all of them. Label masir's own binary
instead.
