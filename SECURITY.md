# Security Policy

## Supported Versions

Security fixes go into the latest release. Older releases do not receive
backported fixes; a fixed release is a download away, and the tool keeps no
state of its own between runs.

| Version | Supported |
| --- | --- |
| Latest release | :white_check_mark: |
| Older releases | :x: |

## Reporting a Vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.**

Report them privately by emailing **johnellisATlinuxDOTcom**, with:

- A description of the issue and its impact
- Steps to reproduce
- The ihasmail-oneshot version (`ihasmail-oneshot version`)
- Whether it is in the tool itself or in the deployment it writes

You should hear back within a few days. Once a fix is released, disclosure
timing and credit are coordinated with you.

## Scope

In scope:

- Secrets the tool generates or writes: file modes, where they end up, what
  outlives the setup
- The deployment it writes: what it publishes, and what it tells Stalwart to
  trust (forwarded client addresses, addresses exempt from bans)
- The Caddyfile and compose.yaml it renders

Out of scope, and best reported upstream:

- Vulnerabilities in Stalwart itself
- Vulnerabilities in ihasmail itself — see
  [its security policy](https://github.com/Coffey-Labs/ihasmail/security/policy)
- Vulnerabilities in Caddy or Docker
