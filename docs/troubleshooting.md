# Troubleshooting and known limits

Problems by the message or symptom you see, what causes each, and what to do;
then the limits of what the tool does. Back to the [README](../README.md), or
see the [guide on docs.ihasmail.org](https://docs.ihasmail.org/install/oneshot/).

## Problems

**`port 443 is already in use on this host`**
Something else, often an existing web server, holds a port the mail host needs.
Free it, or use a separate host. With `--local`, move `--webmail-bind` or
`--stalwart-bind` instead.

**`project ihasmail-example-com already exists in Docker`**
An earlier run left containers or volumes behind. If it's a failed attempt you
want to discard: `ihasmail-oneshot destroy --dir ihasmail-example-com --yes`,
then deploy again. To keep it and deploy another, pass a different `--project`
and `--dir`.

**`... already has files in it; give --dir a new or empty directory`**
The tool never writes over an existing deployment. Choose another directory, or
destroy the old deployment first.

**A deploy stopped partway.**
The error says which step failed and shows the last lines of the relevant
container's log. The stack is left as it was, for you to inspect. To start
again from nothing: `ihasmail-oneshot destroy --dir DIR --yes`, fix the cause,
and deploy again.

**`Pool overlaps with other one on this address space`**
The default private network `172.31.253.0/24` collides with a Docker network you
already have. Destroy the partial deployment and deploy again with, for
example, `--subnet 172.31.200.0/24`.

**`Stalwart has no certificate yet`**
Usually DNS: the mail host or one of the `autoconfig`, `autodiscover`,
`mta-sts`, `ua-auto-config` names doesn't resolve to this host yet, or port 80
isn't reachable from the internet. Check with `dig +short mail.example.com` from
somewhere else, fix it, then run `ihasmail-oneshot certs --dir DIR`. Stalwart's
reasons are in its log:

```bash
docker compose logs stalwart | grep -i acme
```

**`Caddy has no certificate for webmail.example.com yet`**
Same causes. Caddy retries by itself with increasing delays. See
`docker compose logs caddy | grep -i error`.

**Mail arrives but sent mail never does.**
Outbound port 25 is almost always the cause. Find the mail server of a domain
you send to with `dig +short MX example.net`, then test from the host with
`nc -vz <that mail server> 25`. Stalwart's log names each delivery
failure (`docker compose logs stalwart`). Also check that reverse DNS for the host's address
names the mail host.

**Sent mail lands in spam.**
Check that the SPF, DKIM and DMARC records from `dns-records.zone` are
published exactly, and that reverse DNS is set. New addresses need a little time
to build a sending reputation.

**The webmail signs everyone out.**
Expected after `docker compose restart ihasmail`, any ihasmail upgrade, or a
reboot: sessions live in memory.

**`/api/health` shows push accounts `pending` that never become `verified`.**
Stalwart can't reach `https://webmail.example.com` from inside its container.
Mail still updates in real time over the relay. The usual causes are a host
firewall that drops traffic from Docker's bridge to the host's own public
address, or the webmail name not resolving from the host.

## Known limits

- **One mail domain per deploy.** More can be added afterwards in Stalwart or in
  ihasmail's Administration. Their certificates and DNS records are then yours
  to arrange.
- **Linux Docker hosts only.** The tool drives the local Docker daemon and
  publishes ports on the host it runs on.
- **IPv4 on the private network.** Ports are also published on IPv6 wherever
  Docker does so on the host.
- **Fresh deployments only.** It doesn't import an existing Stalwart, or adopt a
  deployment it didn't write.
- **Stalwart doesn't retry a failed certificate order** by itself, on a
  transient CA error either. `certs` starts a new one.
- **Push by subscription isn't covered by the end-to-end test**, whose lab has no
  public DNS for Stalwart to resolve the webmail's name through. Its fallback,
  the relay, is what the test exercises.
- Validated against **Stalwart 0.16.22**. Other Stalwart versions may change the
  registry objects the setup uses.
