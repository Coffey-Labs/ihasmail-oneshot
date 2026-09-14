#!/bin/bash
# SPDX-FileCopyrightText: 2026 Coffey Labs
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# End-to-end test of a public deployment, with no internet involved: Pebble
# stands in for Let's Encrypt and a DNS stub answers every name with this
# host's address, so both Caddy and Stalwart really get certificates, over the
# real ports, through the real Caddyfile.
#
# It publishes 25, 80, 443, 465, 993, 995 and 4190 on this machine while it
# runs, and removes everything it created when it ends, pass or fail.
#
# Usage: e2e/public.sh            (from the repository root; needs docker, curl, openssl, python3)
#        KEEP=1 e2e/public.sh     leave the stack up afterwards, to look at
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$ROOT/e2e/work"
BIN="$WORK/ihasmail-oneshot"
DOMAIN=lab.test
MAIL=mx.lab.test         # not "mail": proves the names follow --mail-host
WEBMAIL=webmail.lab.test
LABNET=ihasmail-oneshot-e2e
LABSUBNET=172.31.254.0/24
HOSTIP=172.31.254.1      # this host, as seen from the lab network
DEPLOY="$WORK/deployment"

pass=0
ok()   { echo "  ok   $*"; pass=$((pass + 1)); }
die()  { echo "  FAIL $*" >&2; exit 1; }

cleanup() {
  status=$?
  if [ "${KEEP:-}" = 1 ]; then
    echo "==> KEEP=1: leaving the stack and the lab up"
    return
  fi
  echo "==> cleaning up"
  [ -f "$DEPLOY/compose.yaml" ] && "$BIN" destroy --dir "$DEPLOY" --yes >/dev/null 2>&1 || true
  docker rm -f oneshot-e2e-pebble oneshot-e2e-dns >/dev/null 2>&1 || true
  docker network rm "$LABNET" >/dev/null 2>&1 || true
  rm -rf "$WORK"
  [ "$status" -eq 0 ] && echo "==> passed: $pass checks" || echo "==> failed after $pass checks" >&2
}
trap cleanup EXIT

rm -rf "$WORK" && mkdir -p "$WORK"
echo "==> building"
(cd "$ROOT" && go build -o "$BIN" ./cmd/ihasmail-oneshot)

# --- the lab: an ACME CA and a DNS stub ---------------------------------------
echo "==> starting Pebble and the DNS stub"
docker network create --subnet "$LABSUBNET" "$LABNET" >/dev/null
# Pebble's own HTTPS certificate names only localhost and "pebble". Stalwart
# and Caddy reach it at this host's address, from another network, so it gets
# a certificate for that address, signed by the test root that ships with it.
docker create --name oneshot-e2e-extract ghcr.io/letsencrypt/pebble:latest >/dev/null
docker cp -q oneshot-e2e-extract:/test/certs "$WORK/pebble-certs"
docker cp -q oneshot-e2e-extract:/test/config/pebble-config.json "$WORK/pebble-config.json"
docker rm oneshot-e2e-extract >/dev/null
openssl req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=pebble" \
  -keyout "$WORK/pebble-key.pem" -out "$WORK/pebble.csr" 2>/dev/null
openssl x509 -req -in "$WORK/pebble.csr" -days 2 -CA "$WORK/pebble-certs/pebble.minica.pem" \
  -CAkey "$WORK/pebble-certs/pebble.minica.key.pem" -CAcreateserial \
  -extfile <(printf 'subjectAltName=IP:%s\nextendedKeyUsage=serverAuth\n' "$HOSTIP") -out "$WORK/pebble-cert.pem" 2>/dev/null
python3 - "$WORK/pebble-config.json" <<'EOF'
import json, sys
path = sys.argv[1]
c = json.load(open(path))
c["pebble"].update(httpPort=80, tlsPort=443, certificate="/work/pebble-cert.pem", privateKey="/work/pebble-key.pem")
json.dump(c, open(path, "w"))
EOF
docker run -d --name oneshot-e2e-dns --network "$LABNET" --ip 172.31.254.3 \
  ghcr.io/letsencrypt/pebble-challtestsrv:latest \
  -defaultIPv4 "$HOSTIP" -defaultIPv6 "" -http01 "" -https01 "" -tlsalpn01 "" -doh "" >/dev/null
# Nonce rejection off: Pebble refuses 5% of nonces on purpose, and Stalwart
# 0.16.22 gives up an order on the first one instead of retrying.
docker run -d --name oneshot-e2e-pebble --network "$LABNET" --ip 172.31.254.2 \
  -p 14000:14000 -p 15000:15000 -v "$WORK:/work:ro" \
  -e PEBBLE_VA_NOSLEEP=1 -e PEBBLE_WFE_NONCEREJECT=0 \
  ghcr.io/letsencrypt/pebble:latest -config /work/pebble-config.json -dnsserver 172.31.254.3:8053 >/dev/null
for _ in $(seq 1 30); do
  curl -sf --cacert "$WORK/pebble-certs/pebble.minica.pem" "https://$HOSTIP:14000/dir" >/dev/null && break
  sleep 1
done
curl -sf --cacert "$WORK/pebble-certs/pebble.minica.pem" "https://$HOSTIP:14000/dir" >/dev/null || die "Pebble did not come up"
ok "Pebble answers at https://$HOSTIP:14000/dir"

# --- the deployment -------------------------------------------------------------
echo "==> deploying"
"$BIN" deploy --domain "$DOMAIN" --mail-host "$MAIL" --webmail-host "$WEBMAIL" --user alice \
  --acme-directory "https://$HOSTIP:14000/dir" --acme-ca-root "$WORK/pebble-certs/pebble.minica.pem" \
  --dir "$DEPLOY" --yes | tee "$WORK/deploy.log"
grep -q "linked" "$WORK/deploy.log" || die "deploy did not report the link"
ok "deploy completed and signed in through ihasmail"
grep -q "certificate  issued by CN=Pebble" "$WORK/deploy.log" || die "deploy did not report Stalwart's certificate"
ok "deploy reported Stalwart's certificate"

# expect CODE curl-args...: retry for up to 30s until curl gets CODE. Caddy
# obtains certificates for its names in parallel and in the background, so the
# first handshake for any one of them can come a few seconds after deploy.
expect() {
  local want=$1 got=; shift
  for _ in $(seq 1 30); do
    got=$(curl -s -o /dev/null -w '%{http_code}' "$@" 2>/dev/null || true)
    [ "$got" = "$want" ] && return 0
    sleep 1
  done
  echo "       got HTTP ${got:-nothing}, wanted $want" >&2
  return 1
}

cred() { sed -n "s/^$1 = //p" "$DEPLOY/credentials.txt"; }
ADMIN=$(cred admin); ADMIN_PW=$(cred admin_password); ALICE_PW=$(cred "mailbox alice@$DOMAIN")
[ "$(stat -c %a "$DEPLOY/credentials.txt")" = 600 ] || die "credentials.txt is not 0600"
[ "$(stat -c %a "$DEPLOY/.env")" = 600 ] || die ".env is not 0600"
ok "credentials.txt and .env are private"

stalwart_env=$(docker compose --project-directory "$DEPLOY" ps -q stalwart | xargs docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}')
grep -q STALWART_RECOVERY_ADMIN <<<"$stalwart_env" && die "the bootstrap credential outlived the setup"
ok "no bootstrap credential left on the Stalwart container"

# Pebble issues from a root it generates at startup.
curl -sf --cacert "$WORK/pebble-certs/pebble.minica.pem" "https://$HOSTIP:15000/roots/0" > "$WORK/issuer-root.pem"
curl -sf --cacert "$WORK/pebble-certs/pebble.minica.pem" "https://$HOSTIP:15000/intermediates/0" > "$WORK/issuer-int.pem"
cat "$WORK/issuer-int.pem" "$WORK/issuer-root.pem" > "$WORK/issuer-chain.pem"

# --- Stalwart's own TLS: IMAPS and submissions --------------------------------
for port in 993 465; do
  out=$(openssl s_client -connect "127.0.0.1:$port" -servername "$MAIL" -verify_hostname "$MAIL" \
    -CAfile "$WORK/issuer-chain.pem" </dev/null 2>&1 || true)
  grep -q "Verify return code: 0 (ok)" <<<"$out" || { echo "$out" | tail -20; die "port $port does not present a valid certificate for $MAIL"; }
  ok "port $port presents a verified certificate for $MAIL"
done
# The Pebble CA's own HTTPS cert is signed by minica, but what Stalwart holds
# came through the ACME order -- so the issuer check above is the real test.

# --- Caddy: the webmail and Stalwart's web side --------------------------------
resolve=(--resolve "$WEBMAIL:443:127.0.0.1" --resolve "$MAIL:443:127.0.0.1" --resolve "autoconfig.$DOMAIN:443:127.0.0.1")
# Captured rather than piped: grep -q exits at the first match, curl takes a
# SIGPIPE, and pipefail reports a pass as a failure.
# Retried: deploy waits for Stalwart's certificate, not Caddy's, and Caddy
# obtains its own in the background.
for i in $(seq 1 60); do
  health=$(curl -sS -f "${resolve[@]}" --cacert "$WORK/issuer-chain.pem" "https://$WEBMAIL/api/health" 2>&1 || true)
  grep -q '"ok":true' <<<"$health" && break
  sleep 1
done
grep -q '"ok":true' <<<"$health" || die "the webmail is not served over verified HTTPS: $health"
[ "$i" -gt 1 ] && echo "       (Caddy's certificate took ${i}s after deploy finished)"
ok "https://$WEBMAIL serves ihasmail with a verified certificate"

expect 200 "${resolve[@]}" --cacert "$WORK/issuer-chain.pem" -H 'Content-Type: application/json' -H 'X-Requested-With: ihasmail' \
  "https://$WEBMAIL/api/auth/login" -d "{\"username\":\"alice@$DOMAIN\",\"password\":\"$ALICE_PW\"}" \
  || die "alice cannot sign in through https://$WEBMAIL"
ok "alice signs in through https://$WEBMAIL"

expect 200 "${resolve[@]}" --cacert "$WORK/issuer-chain.pem" -u "$ADMIN:$ADMIN_PW" "https://$MAIL/jmap/session" \
  || die "Stalwart's JMAP is not reachable at https://$MAIL"
ok "https://$MAIL reaches Stalwart with a verified certificate"

expect 200 "${resolve[@]}" --cacert "$WORK/issuer-chain.pem" "https://autoconfig.$DOMAIN/mail/config-v1.1.xml?emailaddress=alice@$DOMAIN" \
  || die "autoconfig is not served at https://autoconfig.$DOMAIN"
ok "https://autoconfig.$DOMAIN serves Thunderbird autoconfig"

expect 308 --resolve "$MAIL:80:127.0.0.1" "http://$MAIL/" || die "port 80 for $MAIL does not redirect"
ok "http://$MAIL redirects to HTTPS"

# --- DNS records and the certs command ----------------------------------------
grep -q "^$DOMAIN\. IN MX 10 $MAIL\." "$DEPLOY/dns-records.zone" || die "dns-records.zone has no MX for $MAIL"
grep -q "_domainkey\.$DOMAIN\. IN TXT" "$DEPLOY/dns-records.zone" || die "dns-records.zone has no DKIM record"
ok "dns-records.zone has the MX and DKIM records"

certs_out=$("$BIN" certs --dir "$DEPLOY" 2>&1 || true)
grep -q "already holds a certificate for $MAIL" <<<"$certs_out" || die "certs did not see the existing certificate: $certs_out"
ok "certs recognises the certificate already issued"

# --- the auto-ban -------------------------------------------------------------
# Last, so nothing earlier can be affected by a ban. Scans come from throwaway
# containers at fixed addresses; queries go through the Stalwart container
# itself, which no ban applies to.
# Called from inside the Stalwart container, so a ban on any outside address
# cannot lock the test out of checking it.
jmap() {
  docker compose --project-directory "$DEPLOY" exec -T stalwart curl -s -u "$ADMIN:$ADMIN_PW" \
    -H 'Content-Type: application/json' http://127.0.0.1:8080/jmap/ \
    -d "{\"using\":[\"urn:ietf:params:jmap:core\",\"urn:stalwart:jmap\"],\"methodCalls\":[$1]}"
}
blocked() { jmap '["x:BlockedIp/get",{"ids":null,"properties":["address"]},"0"]'; }
SUBNET_PREFIX=172.31.253

# A scanner through Caddy is banned by its own address, not Caddy's. The path
# has to be a scanner path that Stalwart answers 404 -- it redirects
# /wp-login.php, which is never counted -- and it takes 30 of them by default.
scanner=172.31.253.81
docker run --rm --network ihasmail-lab-test_stack --ip "$scanner" curlimages/curl -s -o /dev/null \
  --resolve "$MAIL:443:$SUBNET_PREFIX.10" -k "https://$MAIL/probe/wp-admin.php?[1-35]" || true
sleep 1
bl=$(blocked)
grep -q "\"$SUBNET_PREFIX.10\"" <<<"$bl" && die "a scan through Caddy banned Caddy itself: $bl"
grep -q "\"$scanner\"" <<<"$bl" || die "a scan through Caddy did not ban the scanner: $bl"
ok "a scan through Caddy bans the scanner ($scanner), not Caddy"
code=$(docker run --rm --network ihasmail-lab-test_stack --ip 172.31.253.82 curlimages/curl -s -o /dev/null -w '%{http_code}' \
  --resolve "autoconfig.$DOMAIN:443:$SUBNET_PREFIX.10" -k "https://autoconfig.$DOMAIN/mail/config-v1.1.xml?emailaddress=alice@$DOMAIN" || true)
[ "$code" = 200 ] || die "another client is refused through Caddy after the scan (HTTP $code)"
ok "other clients still reach Stalwart through Caddy"

allowed=$(jmap '["x:AllowedIp/get",{"ids":null,"properties":["address"]},"0"]')
grep -q "\"$SUBNET_PREFIX.11\"" <<<"$allowed" || die "ihasmail is not exempt from the auto-ban: $allowed"
ok "ihasmail's address is exempt from the auto-ban"
for _ in 1 2 3 4 5; do
  curl -s -o /dev/null -H 'Content-Type: application/json' -H 'X-Requested-With: ihasmail' \
    http://127.0.0.1:8080/api/auth/login -d "{\"username\":\"alice@$DOMAIN\",\"password\":\"wrong\"}"
done
expect 200 -H 'Content-Type: application/json' -H 'X-Requested-With: ihasmail' \
  http://127.0.0.1:8080/api/auth/login -d "{\"username\":\"alice@$DOMAIN\",\"password\":\"$ALICE_PW\"}" \
  || die "alice cannot sign in through ihasmail after failed attempts"
ok "alice still signs in through ihasmail after failed attempts"
