# Reference

Everything an operator looks up: requirements, installing, each command and
flag, what the deployment directory holds, and running, upgrading, backing up
and removing a deployment. For a walk-through, see the
[guide on docs.ihasmail.org](https://docs.ihasmail.org/install/oneshot/). Back
to the [README](../README.md).

## Requirements

**On the host:**

- Linux, amd64 or arm64.
- Docker Engine with the compose plugin (`docker compose version` works), run
  as a user allowed to use Docker.

**For a mail host, additionally:**

- A domain whose DNS you control.
- **Ports 25, 80, 443, 465, 993, 995 and 4190** free on the host and open in any
  firewall or cloud security group in front of it.
- **Outbound port 25.** Many cloud and VPS providers block it by default and
  unblock it on request. Without it you can receive mail but not send it.
- **Reverse DNS**: a PTR record for the host's IP address naming the mail host
  (`mail.example.com`). This is set at your hosting provider, not in your DNS
  zone. Many receiving servers reject mail from an address without one.
- A static public IP address.

**Resources:** ihasmail on its own needs 256 MiB of RAM at minimum. Stalwart's
needs grow with the mail it stores and the number of people using it; size the
host for Stalwart.

## Installing

### From a release

Download the archive for your architecture and its checksums, verify, and
unpack:

```bash
ARCH=amd64   # or arm64
curl -fsSLO https://github.com/Coffey-Labs/ihasmail-oneshot/releases/latest/download/ihasmail-oneshot-linux-$ARCH.tar.gz
curl -fsSLO https://github.com/Coffey-Labs/ihasmail-oneshot/releases/latest/download/SHA256SUMS
sha256sum --ignore-missing -c SHA256SUMS
tar -xzf ihasmail-oneshot-linux-$ARCH.tar.gz
sudo install -m 0755 ihasmail-oneshot /usr/local/bin/
ihasmail-oneshot version
```

Every release is built by the [release workflow](../.github/workflows/release.yml)
from a tagged commit on `main`, after the tests and a known-vulnerabilities
check pass. The archives are reproducible: `scripts/build-release.sh` builds the
same bytes from the same commit.

### From source

With Go 1.26.8 or newer:

```bash
git clone https://github.com/Coffey-Labs/ihasmail-oneshot.git
cd ihasmail-oneshot
go build -o ihasmail-oneshot ./cmd/ihasmail-oneshot
```

## Commands

### `deploy`

```text
ihasmail-oneshot deploy --domain example.com [flags]
ihasmail-oneshot deploy --local [flags]
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--domain` | required, or `example.test` with `--local` | The mail domain this server receives for |
| `--local` | off | The loopback-only shape: no mail ports, no Caddy, no certificates |
| `--mail-host` | `mail.DOMAIN` | Stalwart's hostname. Must be one label under the domain, e.g. `mx.example.com` |
| `--webmail-host` | `webmail.DOMAIN` | The webmail's hostname. Any name except Stalwart's |
| `--email` | `postmaster@DOMAIN` | ACME contact address for both Caddy and Stalwart |
| `--user NAME` | none | Create a mailbox `NAME@DOMAIN` with a generated password. Repeat for more |
| `--dir` | `./PROJECT` | Deployment directory to write. Must be new or empty |
| `--project` | `ihasmail-DOMAIN` (dots as dashes) | Compose project name, which prefixes containers, network and volumes |
| `--stalwart-image` | `stalwartlabs/stalwart:v0.16.22` | Stalwart image |
| `--ihasmail-image` | the newest release | ihasmail image. By default the tool looks up ihasmail's newest release and writes it into `compose.yaml` by its dated tag; name an image to use a particular one |
| `--caddy-image` | `caddy:2.11.4` | Caddy image |
| `--webmail-bind` | `127.0.0.1:8080` | Host address for ihasmail's own port, for reaching it without Caddy |
| `--stalwart-bind` | `127.0.0.1:8081` | Host address for Stalwart's plain-HTTP port. The tool configures Stalwart through it |
| `--subnet` | `172.31.253.0/24` | The stack's private network. Change it if it overlaps a network you already have |
| `--acme-directory` | Let's Encrypt | ACME directory URL of a private CA, for both Caddy and Stalwart |
| `--acme-ca-root` | none | PEM file of the root the private CA's HTTPS endpoint is signed by |
| `--yes` | off | Don't ask for confirmation. Required without a terminal |

Stalwart's and Caddy's defaults are the versions tested together for this release. ihasmail's default is its newest release, looked up when the tool runs and written into `compose.yaml` by its dated tag.

### `certs`

```text
ihasmail-oneshot certs --dir DIR
```

Starts a new certificate order in Stalwart for the deployment in `DIR` and waits
for it, typically after fixing DNS. If Stalwart already holds a valid
certificate for the mail host, it reports that and does nothing.

### `destroy`

```text
ihasmail-oneshot destroy --dir DIR [--yes]
```

Removes the deployment in `DIR` with all of its data. See
[Removing a deployment](#removing-a-deployment).

### `version`

Prints the tool's version.

## The deployment directory

The deploy writes a directory named after the domain, `./ihasmail-example-com`
by default (`--dir` to choose):

| File | Mode | What it is |
| --- | --- | --- |
| `compose.yaml` | 0644 | The whole deployment: three services, a private network, named volumes. Commented |
| `Caddyfile` | 0644 | Caddy's configuration: the webmail, Stalwart's web names, and port 80's challenge forwarding |
| `.env` | 0600 | `APP_SECRET`, which seals ihasmail's session cookies. Read by compose |
| `credentials.txt` | 0600 | The administrator's and each mailbox's generated password, plus what `certs` needs to reach Stalwart |
| `dns-records.zone` | 0644 | The DNS records to publish |
| `acme-ca-root.pem`, `ca-bundle.crt` | 0644 | Only with `--acme-ca-root`: the private CA, and the system roots with it added |

Data lives in Docker named volumes, prefixed with the project name:

| Volume | Holds |
| --- | --- |
| `…_stalwart-data` | All mail, accounts, calendars, contacts, files, settings, DKIM keys, Stalwart's certificates |
| `…_stalwart-etc` | Stalwart's store location file |
| `…_caddy-data` | Caddy's certificates and ACME account |
| `…_caddy-config` | Caddy's autosaved configuration |

ihasmail has no volume. It runs read-only and keeps sessions in memory.

## Running a deployment

The directory is a standard compose project. From inside it:

```bash
docker compose ps                      # what's running
docker compose logs -f stalwart        # follow Stalwart's log
docker compose logs caddy | grep -i error
docker compose restart ihasmail        # signs everyone out of the webmail; nothing else is lost
docker compose down                    # stop everything (data stays in the volumes)
docker compose up -d                   # start it again
```

The containers restart by themselves after a crash or a reboot
(`restart: unless-stopped`).

### Upgrading

Nothing upgrades on its own: every image in `compose.yaml` is a fixed version,
ihasmail included. To upgrade, change the image tag there and apply it:

```bash
docker compose pull && docker compose up -d
```

- **ihasmail** is safe to move to any newer release that supports your
  Stalwart version. Its release notes say which. The newest is on
  [ihasmail's releases](https://github.com/Coffey-Labs/ihasmail/releases); its
  image tag is the version with `+` written as `-`, e.g. `2026.9.13-pr344`.
- **Stalwart**: read its upgrade notes before changing versions. Check that the
  ihasmail version you run supports the new Stalwart release first, since
  ihasmail validates against one Stalwart release at a time. Back up
  `stalwart-data` first.
- **Caddy** minor releases are routine.

### Backing up

Everything that can't be recreated is in the `stalwart-data` volume. For a
consistent copy, stop Stalwart briefly:

```bash
docker compose stop stalwart
docker run --rm -v ihasmail-example-com_stalwart-data:/data -v "$PWD":/backup alpine \
  tar -czf /backup/stalwart-data-$(date +%F).tar.gz -C /data .
docker compose start stalwart
```

Keep `caddy-data` too, or certificates are requested again on a rebuild. That's
harmless unless it happens often enough to meet Let's Encrypt's rate limits. Keep
the deployment directory itself, which is small and contains your secrets.

### Removing a deployment

```bash
ihasmail-oneshot destroy --dir ihasmail-example-com
```

This removes the containers, the network, **the volumes with every message and
account**, and the files the tool wrote. It asks first unless given `--yes`.
The directory goes too, unless it holds files you added yourself; those are left
in place, and so is the directory around them.
