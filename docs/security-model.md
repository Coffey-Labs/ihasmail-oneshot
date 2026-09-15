# Security model

What a deployment exposes to the internet, where its secrets live, and the
trust decisions it makes on your behalf. The reasoning behind those decisions is
in [How it works](how-it-works.md). To report a vulnerability, see
[SECURITY.md](../SECURITY.md). Back to the [README](../README.md).

**What's reachable from outside** (mail host shape):

| Port | Service |
| --- | --- |
| 25 | SMTP, Stalwart (receiving mail; STARTTLS) |
| 80 | Caddy: redirects to HTTPS, and ACME HTTP-01 challenges for Stalwart |
| 443 (TCP, UDP) | Caddy: the webmail, and Stalwart's web side |
| 465 | SMTP submission with TLS, Stalwart |
| 993 | IMAP with TLS, Stalwart |
| 995 | POP3 with TLS, Stalwart |
| 4190 | ManageSieve, Stalwart |

Stalwart's plain-HTTP port (8080) and ihasmail's port are published on
`127.0.0.1` only. In `--local` mode, those two loopback ports are all that's
published.

**Secrets:**

- `APP_SECRET` is 48 random bytes, in `.env` (0600). It seals ihasmail's session
  cookies.
- Generated passwords come from the operating system's cryptographic random
  source, letters and digits only, in `credentials.txt` (0600).
- The one-time bootstrap password is never written to disk, and Stalwart is
  recreated without it once setup completes.

**Trust decisions the deployment makes**, each explained in [How it works](how-it-works.md):

- Stalwart believes `X-Forwarded-For` on its HTTP port, which only Caddy and
  ihasmail can reach.
- ihasmail's address is exempt from Stalwart's automatic bans.
- ihasmail believes forwarded headers from private-range peers, which here is
  Caddy.

**What the tool doesn't do:** configure a host firewall, harden the Docker
daemon, set up backups or monitoring, or turn on encryption at rest for
mailboxes. Encryption at rest can't be turned off again once on, which is not a
decision for a deploy tool to make.
