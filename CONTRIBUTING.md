# Contributing to ihasmail-oneshot

How to build and test the tool, how the code is organized, and how versions and
releases work. Back to the [README](README.md).

## Building

With Go 1.26.8 or newer:

```bash
go build -o ihasmail-oneshot ./cmd/ihasmail-oneshot
```

## Testing

```bash
go vet ./...
go test ./...          # unit tests: validation, rendered files, the Stalwart client
e2e/public.sh          # the whole mail-host deploy, for real, on this machine
```

`e2e/public.sh` runs a complete mail-host deployment with no internet involved.
[Pebble](https://github.com/letsencrypt/pebble), the ACME test server, stands in
for Let's Encrypt, and a DNS stub answers every name with the host's own address.
Caddy and Stalwart both obtain real certificates from it, through the real
ports and the real `Caddyfile`. It then checks, among other things, that:

- the deploy completes, links ihasmail and Stalwart, and reports both kinds of
  certificate;
- `credentials.txt` and `.env` are private, and the bootstrap credential is gone
  from the Stalwart container;
- Stalwart's IMAPS (993) and submission (465) ports present certificates that
  verify for the mail host;
- the webmail, Stalwart's JMAP and autoconfig are served over verified HTTPS, and
  a user signs in through the webmail;
- `dns-records.zone` holds the MX and DKIM records, and `certs` recognizes an
  existing certificate;
- a scanner probing through Caddy is banned by its own address, not Caddy's, and
  other clients still get through;
- ihasmail is exempt from bans, and a user still signs in after failed attempts.

It publishes ports 25, 80, 443, 465, 993, 995 and 4190 on the machine while it
runs, and removes everything it created when it ends, pass or fail. `KEEP=1
e2e/public.sh` leaves the stack up to inspect.

## How the code is laid out

| Package | Does |
| --- | --- |
| `cmd/ihasmail-oneshot` | Commands, flags, confirmation, the summary |
| `internal/config` | Validates the command line into a plan: names, addresses, images |
| `internal/render` | Renders `compose.yaml` and the `Caddyfile`; writes files without ever overwriting |
| `internal/docker` | Drives the `docker` and `docker compose` CLIs |
| `internal/stalwart` | The JMAP client and every Stalwart registry call: bootstrap, ACME, bans, mailboxes, DNS zone |
| `internal/webmail` | ihasmail's health check and the sign-in that proves the link |
| `internal/deploy` | Preflight, the deploy sequence, `certs`, `destroy` |

## Versions and releases

Releases are tagged by date, like ihasmail's: `v2026.9.13`, with `.1`, `.2`
added for another release the same day. Each release pins the Stalwart and
Caddy images it was tested with as its defaults. A newer release of the tool
generally means newer tested versions of those.

ihasmail is the exception: a deploy takes its newest release, so a new
ihasmail needs no new release of this tool. What keeps that safe is the
end-to-end test, which runs every Monday against ihasmail's newest release, a
few hours after ihasmail publishes it. Stalwart is never taken this way — an
upgrade migrates its data with no way back, so its version only changes in a
release of this tool.

Binaries for `linux/amd64` and `linux/arm64` and a `SHA256SUMS` file are
attached to every [release](https://github.com/Coffey-Labs/ihasmail-oneshot/releases).

Every release is built by the [release workflow](.github/workflows/release.yml)
from a tagged commit on `main`, after the tests and a known-vulnerabilities
check pass. The archives are reproducible: `scripts/build-release.sh` builds the
same bytes from the same commit.

That weekly run is [`e2e.yml`](.github/workflows/e2e.yml), Mondays at 12:00
UTC. It can also be started by hand from the Actions tab.
