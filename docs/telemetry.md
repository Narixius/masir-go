# Telemetry

Masir sends anonymous usage and error events to [PostHog](https://posthog.com) (EU
region). This page lists everything it can send. To turn it off:

```sh
masir settings set analytics off      # or DO_NOT_TRACK=1, or MASIR_ANALYTICS=off
```

Nothing is sent until masir has shown a one-time notice on your first interactive
command, or you have turned analytics on yourself. Opting out deletes the install ID and
any events still waiting to be sent.

## Identity

- A random install ID, not derived from your machine, account or network. It is deleted
  when you turn analytics off.
- Person profiles are disabled (`$process_person_profile: false`) and IP geolocation is off.

## Properties on daemon events

| Property | Meaning |
|---|---|
| `app_version`, `xray_version`, `arch` | masir and Xray versions, CPU architecture |
| `xray_running`, `tun_enabled` | what is running |
| `daemon_role`, `privileged`, `service_installed` | whether the daemon runs as root or a system service |
| `rule_count`, `inbound_count`, `outbound_count`, `subscription_count`, `total_config_count` | counts only |
| `$timezone`, `utc_offset_minutes`, `tz_abbrev` | your timezone, to see which regions use masir |

## Events

**Daemon:** `proxy_selected`, `xray_started`, `xray_stopped`, `xray_apply_failed`,
`analytics_enabled`, `analytics_disabled`, `power_changed_failed`,
`routing_mode_changed_failed`, `tun_failed`, `rule_added`, `rule_altered`, `rule_moved`,
`rule_removed`, `subscription_added`, `subscription_altered`, `subscription_removed`,
`update_started`, `update_checked`, `update_paused`, `update_resumed`, `update_stopped`,
`update_finished`, `rpc_failed`, `daemon_error`, `daemon_panic`, `daemon_stopped`.

**CLI:** `cli_panic` and `self_update_finished`.

## Errors

Failed events carry `error_kind` (a small category such as `timeout` or `dns`),
`error_fingerprint` (a hash that groups identical errors), and, where applicable,
`grpc_code` and `error_reason`.

Unexpected errors also carry `error_message`, so the problem can be found and fixed. Before
a message leaves your machine, masir masks anything in it that could identify you or your
setup, replacing it with a placeholder such as `<ip>`, `<domain>` or `<url>`:

- IP addresses (IPv4 and IPv6) and domain names
- URLs, including their paths and query strings
- file paths
- UUIDs, email addresses and long tokens or keys
- anything in quotes, which is where masir puts the names you choose for rules, configs and
  subscriptions

What is left is the wording of the error itself, cut to 200 characters. masir's logs, and
Xray's own log output, are never sent.

## Never sent

Subscription URLs or names, which server you use (its name, address, country, protocol or
transport), UUIDs and passwords, rule domains, IPs, process names and app paths, DNS and
DoH addresses, proxy and TUN ports, file paths, usernames, hostnames, and raw logs.

## Delivery

Masir tries PostHog directly and falls back to your running proxy when the direct
connection fails or returns a block page. Events recorded while the daemon is
unavailable wait in `analytics/spool` in masir's config folder (at most 100 events and
256 KB, and nothing older than 7 days) and are sent on the next daemon start.
