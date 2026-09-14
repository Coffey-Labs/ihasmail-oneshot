# ihasmail-oneshot

One command that stands up a fresh [Stalwart](https://stalw.art) mail server
and a fresh [ihasmail](https://github.com/Coffey-Labs/ihasmail) webmail on a
Docker host, already linked to each other:

```bash
ihasmail-oneshot deploy --domain example.com --user alice
```

It writes a deployment directory, starts Stalwart, completes Stalwart's setup
wizard over its API, brings up ihasmail and Caddy, wires them together, requests
certificates, signs in through the webmail to prove the link, and hands you the
administrator password and the DNS records to publish. Afterwards it is an
ordinary `docker compose` project you manage with the usual commands.

It only ever deploys **fresh**: it refuses a directory with files in it, and a
compose project that already has containers or volumes.

## Two shapes

| | `deploy --domain example.com` | `deploy --local` |
| --- | --- | --- |
| For | A mail host on the internet | Trying ihasmail against a real Stalwart |
| Containers | Stalwart, ihasmail, Caddy | Stalwart, ihasmail |
| Published | 25, 465, 993, 995, 4190, 80, 443 on every interface | Nothing but two loopback ports |
| Certificates | Let's Encrypt, for Caddy and for Stalwart | None |
| Webmail | `https://webmail.example.com` | `http://127.0.0.1:8080` |
| Stalwart admin | `https://mail.example.com/admin` | `http://127.0.0.1:8081/admin` |

Both also bind ihasmail to `127.0.0.1:8080` and Stalwart's plain-HTTP port to
`127.0.0.1:8081`, for looking at either directly from the host.

## Requirements

- Linux with Docker Engine and the compose plugin, as a user allowed to run
  `docker`.
- Go 1.26 or newer to build it (there are no release binaries yet).

For a mail host, also:

- A domain whose DNS you control.
- Ports 25, 80, 443, 465, 993, 995 and 4190 free on the host and open in any
  firewall in front of it.
- **Outbound port 25.** Many hosting providers block it until you ask.
- **Reverse DNS** (a PTR record, set at the hosting provider) for the host's
  address, naming the mail host.

## Install

```bash
git clone https://github.com/Coffey-Labs/ihasmail-oneshot.git
cd ihasmail-oneshot
go build -o ihasmail-oneshot ./cmd/ihasmail-oneshot
```

Copy the binary to the Docker host and run it there. It needs no other files.

## Deploying a mail host

Point DNS at the host first, so certificates can be issued on the first run:

```
mail.example.com.     A   <the host's IPv4 address>
webmail.example.com.  A   <the host's IPv4 address>
```

(AAAA records too, if the host has IPv6.) Then:

```bash
ihasmail-oneshot deploy --domain example.com --email you@example.net --user alice --user bob
```

It lists what it will do, checks the host, and asks before changing anything;
`--yes` skips the question, and is required when there is no terminal to ask
on. At the end:

- **`credentials.txt`** holds the Stalwart administrator (`admin@example.com`)
  and a generated password for each `--user`. Readable only by you. The
  administrator can sign in to the webmail, where it gets the Administration
  menu, and to Stalwart's own admin UI.
- **`dns-records.zone`** holds every record Stalwart wants published: MX, SPF,
  DKIM, DMARC, the SRV records, MTA-STS, and the autoconfig names. Publish all
  of them.

If DNS did not point at the host yet, the webmail still comes up and Caddy
keeps retrying its certificates by itself. Stalwart does not retry a failed
order, so once DNS is right:

```bash
ihasmail-oneshot certs --dir ihasmail-example-com
```

## Trying it locally

```bash
ihasmail-oneshot deploy --local --user alice
```

Open `http://127.0.0.1:8080` and sign in with a mailbox from
`ihasmail-example-test/credentials.txt`. Nothing outside the host can reach
it, and no mail can be delivered to it.

## What it sets up, and why

```
                       ┌──────────── private network (172.31.253.0/24) ────────────┐
 443, 80 ──► Caddy .10 ─┼─► ihasmail .11 ──http://stalwart:8080──► Stalwart .12     │
                        │                                              ▲             │
                        └─── Stalwart's web names ─────────────────────┘             │
 25, 465, 993, 995, 4190 ────────────────────────────────────────────► Stalwart     │
                       └───────────────────────────────────────────────────────────┘
```

**ihasmail reaches Stalwart over plain HTTP on the private network.** The leg
never leaves the host, and the private route is about a third of the memory per
signed-in tab of going back out through HTTPS. Stalwart's HTTP port is published
on loopback only.

**ihasmail runs immutable**: read-only root filesystem, no volume, sessions in
memory. A restart signs everyone out and loses nothing else, because everything
durable, including each user's settings, lives in Stalwart.

**Stalwart is set up through its bootstrap API, not a template.** Stalwart 0.16
keeps its configuration in its data store, and a server with an empty
configuration starts in bootstrap mode. The tool starts it with a one-off
recovery administrator, completes setup with `x:Bootstrap/set`, and restarts it
*without* that credential, so no fixed administrator password outlives the
setup. Logging goes to stdout rather than the default log directory, which does
not exist in the container.

**Caddy and Stalwart share ports 80 and 443 without competing.** Both need
certificates for Stalwart's names: Caddy to serve its web side over HTTPS,
Stalwart for IMAP and SMTP. They are separated by challenge type. Caddy uses
TLS-ALPN-01 on 443 for those names and never port 80; Stalwart uses HTTP-01,
and Caddy forwards `/.well-known/acme-challenge/` on port 80 to it untouched.
Stalwart's certificate covers the mail host plus `autoconfig`, `autodiscover`,
`mta-sts` and `ua-auto-config` under the domain, and Caddy fronts all five.

**Stalwart's auto-ban is made safe for a proxy in front of it.** Stalwart bans
an address that probes scanner paths such as `/wp-admin.php`, and counts other
abuse per address too. Behind a proxy, that address is the proxy's. So:

- Stalwart takes client addresses from Caddy's `X-Forwarded-For`. Without it,
  one scanner bans Caddy, and with it every autoconfig lookup, calendar client
  and certificate renewal. With it, the scanner is banned and nobody else is.
  This is safe only because nothing untrusted can reach Stalwart's HTTP port.
- Every request the webmail makes arrives from ihasmail's fixed address, which
  is added to Stalwart's allowed addresses, so no ban can take the webmail down
  for everybody. ihasmail rate-limits sign-ins per real client itself.

Neither setting applies to a running Stalwart, so the tool restarts it after
making them.

**Push by subscription.** ihasmail is given `PUSH_URL`, so Stalwart posts
changes to it instead of holding a connection open per browser tab. If Stalwart
cannot reach that URL, every tab falls back to the relay and nothing breaks;
`/api/health` shows which is in use.

## Afterwards

The deployment directory is a compose project:

```bash
cd ihasmail-example-com
docker compose ps
docker compose logs -f stalwart
docker compose restart ihasmail
```

To upgrade, change an image tag in `compose.yaml` and run `docker compose up -d`.
Read ihasmail's release notes for the Stalwart version it supports before
moving Stalwart.

Data lives in the named volumes `stalwart-etc` and `stalwart-data` (mail,
accounts, configuration) and `caddy-data` (certificates and the ACME account).
Back those up. `APP_SECRET` in `.env` seals ihasmail's sessions; changing it
signs everyone out.

To remove a deployment and **everything in it**, mail included:

```bash
ihasmail-oneshot destroy --dir ihasmail-example-com
```

It removes the containers, network and volumes, and the files the tool wrote. A
file you added to the directory yourself is left, and so is the directory.

## Flags

`ihasmail-oneshot deploy -h` lists them all.

| Flag | Default | |
| --- | --- | --- |
| `--domain` | required; `example.test` with `--local` | The mail domain |
| `--mail-host` | `mail.DOMAIN` | Stalwart's hostname: one label under the domain |
| `--webmail-host` | `webmail.DOMAIN` | The webmail's hostname |
| `--email` | `postmaster@DOMAIN` | ACME contact address. Use one that does not depend on this server |
| `--user` | none | Create a mailbox with a generated password. Repeat for more |
| `--local` | off | The loopback-only shape |
| `--dir` | `./PROJECT` | Deployment directory: new or empty |
| `--project` | `ihasmail-DOMAIN` | Compose project name, dots as dashes |
| `--stalwart-image` | `stalwartlabs/stalwart:v0.16.22` | |
| `--ihasmail-image` | `ghcr.io/coffey-labs/ihasmail:2026.9.10-pr328` | |
| `--caddy-image` | `caddy:2.11.4` | |
| `--webmail-bind` | `127.0.0.1:8080` | Host address for ihasmail's own port |
| `--stalwart-bind` | `127.0.0.1:8081` | Host address for Stalwart's plain-HTTP port |
| `--subnet` | `172.31.253.0/24` | Private network. Change it if it overlaps one you have |
| `--acme-directory` | Let's Encrypt | ACME directory of a private CA |
| `--acme-ca-root` | none | PEM root the private CA's own HTTPS is signed by |
| `--yes` | off | Do not ask for confirmation |

## Known limits

- **One domain.** More can be added afterwards in Stalwart or in ihasmail's
  Administration; their certificates and DNS are then yours to arrange.
- **IPv4 on the private network.** Ports are published on IPv6 too wherever
  Docker does so on the host.
- **Stalwart does not retry a certificate order that fails**, including on a
  transient error from the CA. `certs` starts a new one.
- **Push by subscription is not covered by the end-to-end test**, which has no
  public DNS for Stalwart to resolve the webmail's name with. Without it the
  relay is used, which is the documented fallback.

## Testing

```bash
go test ./...
e2e/public.sh
```

`e2e/public.sh` deploys a full mail host on the machine it runs on with no
internet involved: [Pebble](https://github.com/letsencrypt/pebble) stands in for
Let's Encrypt and a DNS stub answers every name with the host's own address. It
checks that Stalwart's IMAPS and submissions ports and Caddy's HTTPS all present
verified certificates, that users sign in through the webmail over HTTPS, that
autoconfig is served, that a scan through Caddy bans the scanner while other
clients still get through, and that ihasmail is exempt from bans. It
publishes the mail ports while it runs and removes everything when it ends;
`KEEP=1` leaves it up to look at.

## License

GPL-3.0-or-later. See [LICENSE](LICENSE).
