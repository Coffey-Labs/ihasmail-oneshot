# ihasmail-oneshot

[![Latest release](https://img.shields.io/github/v/release/Coffey-Labs/ihasmail-oneshot?sort=date)](https://github.com/Coffey-Labs/ihasmail-oneshot/releases/latest)
[![License: AGPL-3.0-or-later](https://img.shields.io/badge/license-AGPL--3.0--or--later-blue)](LICENSE)
[![Docs: docs.ihasmail.org](https://img.shields.io/badge/docs-docs.ihasmail.org-0ea5e9)](https://docs.ihasmail.org/install/oneshot/)

**One command that turns a Linux Docker host into a working mail server with
webmail.** It deploys a fresh [Stalwart](https://stalw.art) mail server and a
fresh [ihasmail](https://github.com/Coffey-Labs/ihasmail) webmail, links them
together, gets them certificates, and hands you the administrator password and
the DNS records to publish.

```bash
ihasmail-oneshot deploy --domain example.com --user alice
```

## Documentation

| | |
| --- | --- |
| 📘 **[Step-by-step guide](https://docs.ihasmail.org/install/oneshot/)** | **Start here.** On docs.ihasmail.org: DNS, ports, deploying, publishing records, signing in, upgrading and backups |
| 📋 **[Reference](docs/reference.md)** | Requirements, every command and flag, the deployment directory, upgrading, backing up, removing |
| ⚙️ **[How it works](docs/how-it-works.md)** | Why it exists, the deploy sequence, Stalwart setup without its wizard, shared certificates, IP bans behind a proxy |
| 🔒 **[Security model](docs/security-model.md)** | What is exposed, where secrets live, the trust decisions it makes |
| 🧰 **[Troubleshooting](docs/troubleshooting.md)** | Problems by message or symptom, and known limits |
| 🧪 **[Contributing](CONTRIBUTING.md)** | Building, the unit and end-to-end tests, the code layout, releases |

## What you get

- A mail server for `example.com`, with webmail at `https://webmail.example.com`
  and Stalwart's admin UI at `https://mail.example.com/admin`.
- A mailbox for each `--user`, and an administrator, with generated passwords in
  `credentials.txt`.
- Certificates for the webmail and for Stalwart's mail ports.
- `dns-records.zone`: every DNS record to publish, DKIM keys included.
- An ordinary `docker compose` project, managed with the usual commands and not
  tied to this tool. ihasmail runs read-only with no volume; everything durable
  is in Stalwart's volume.

## Requirements

- Linux (amd64 or arm64) with Docker Engine and the compose plugin.
- For a mail host: a domain whose DNS you control, a static public IP, ports
  **25, 80, 443, 465, 993, 995 and 4190** open, **outbound port 25** allowed by
  your provider, and **reverse DNS** for the host's address naming the mail host.

The full list is in [docs/reference.md](docs/reference.md#requirements).

## Install

```bash
ARCH=amd64   # or arm64
curl -fsSLO https://github.com/Coffey-Labs/ihasmail-oneshot/releases/latest/download/ihasmail-oneshot-linux-$ARCH.tar.gz
curl -fsSLO https://github.com/Coffey-Labs/ihasmail-oneshot/releases/latest/download/SHA256SUMS
sha256sum --ignore-missing -c SHA256SUMS
tar -xzf ihasmail-oneshot-linux-$ARCH.tar.gz
sudo install -m 0755 ihasmail-oneshot /usr/local/bin/
```

Or build from source with Go 1.26.8 or newer:
`go build -o ihasmail-oneshot ./cmd/ihasmail-oneshot`.

## Use

Try ihasmail against a real Stalwart on your own machine, with no domain and
nothing reachable from outside:

```bash
ihasmail-oneshot deploy --local --user alice
ihasmail-oneshot destroy --dir ihasmail-example-test     # when you're done
```

Deploy a real mail host, after pointing `mail.example.com` and
`webmail.example.com` at it:

```bash
ihasmail-oneshot deploy --domain example.com --email you@example.net --user alice
```

It checks the host, shows the plan with the exact ihasmail release it will use,
and asks before changing anything. Then publish `dns-records.zone`, and mail
flows.

## Versions

Releases are tagged by date (`v2026.9.15`). Each pins the Stalwart and Caddy
versions it was tested with. ihasmail is the newest release at deploy time,
written into `compose.yaml` by its dated tag, and checked by an end-to-end test
every Monday. Nothing upgrades by itself afterwards. Details in
[CONTRIBUTING.md](CONTRIBUTING.md#versions-and-releases).

## Security

To report a vulnerability, see [SECURITY.md](SECURITY.md). Please don't open a
public issue.

## License

AGPL-3.0-or-later, the same as ihasmail. See [LICENSE](LICENSE).

Running the tool to deploy your own mail host places no obligations on you. The
license matters if you modify the tool and offer it to others, including as a
hosted service that deploys on their behalf: then your modified source must be
available to them.
