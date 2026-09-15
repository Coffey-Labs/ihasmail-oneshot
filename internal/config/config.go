// SPDX-FileCopyrightText: 2026 Coffey Labs
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package config turns the command line into a Plan: every name, address and
// image the deployment uses, validated once, before anything touches Docker.
//
// Nothing downstream re-checks what is here. A plan that validates is one the
// templates can render without quoting surprises, so the rules are strict on
// purpose: a hostname is a hostname, a bind is host:port, a user name is the
// local part of an address and nothing else.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Stalwart is pinned to the release this version of the tool was tested with,
// because a Stalwart upgrade migrates its store with no way back and ihasmail
// validates against one Stalwart release at a time. Caddy is pinned so that a
// deploy months from now renders the same proxy.
//
// ihasmail is not pinned here. A pin went stale within days of each release,
// and ihasmail's image cleanup keeps ten releases, so an old default would in
// time stop pulling at all. The default is the newest release instead, looked
// up when the tool runs and written into compose.yaml as its dated tag -- see
// deploy.ResolveIhasmail -- so what a deployment runs is still recorded and
// nothing moves it afterwards.
const (
	DefaultStalwartImage = "stalwartlabs/stalwart:v0.16.22"
	DefaultCaddyImage    = "caddy:2.11.4"

	IhasmailRepository = "ghcr.io/coffey-labs/ihasmail"
	// NewestIhasmail is the default --ihasmail-image. Only full releases move
	// this tag; prereleases never do.
	NewestIhasmail = IhasmailRepository + ":latest"
)

// ihasmail's versions are the date of a commit and where it came from --
// 2026.9.13+pr344, or 2026.9.13+g1fa6578 for a commit that arrived without a
// pull request -- and its image tags are the same with the "+" as "-", since a
// Docker tag may not contain "+".
var ihasmailVersionRE = regexp.MustCompile(`^\d{4}\.\d{1,2}\.\d{1,2}\+(?:pr\d+|g[0-9a-f]{7,40})$`)

// IhasmailTag is the image tag an ihasmail version is published under. A
// version that is not a release's -- empty, or the 0.0.0 of a build nobody
// gave a version to -- has none.
func IhasmailTag(version string) (string, bool) {
	if !ihasmailVersionRE.MatchString(version) {
		return "", false
	}
	return strings.Replace(version, "+", "-", 1), true
}

// FollowsNewestIhasmail reports whether the plan still asks for the newest
// ihasmail release rather than a particular image.
func (p Plan) FollowsNewestIhasmail() bool { return p.IhasmailImage == NewestIhasmail }

// Stalwart's ACME order covers these next to the mail host, all under the mail
// domain: it is what its own DNS zone points at the mail host as CNAMEs, and
// Caddy has to answer for every one of them on port 80 or the order fails.
var stalwartServiceLabels = []string{"autoconfig", "autodiscover", "mta-sts", "ua-auto-config"}

// Options is the command line, as given.
type Options struct {
	Local bool

	Domain      string
	MailHost    string
	WebmailHost string
	Email       string

	Dir     string
	Project string
	Users   []string

	StalwartImage string
	IhasmailImage string
	CaddyImage    string

	WebmailBind  string
	StalwartBind string
	Subnet       string

	// A private ACME CA, instead of Let's Encrypt. Both are for an internal CA
	// (and for the end-to-end test, which runs one); neither is needed on the
	// open internet.
	ACMEDirectory string
	ACMECARoot    string
}

// Plan is Options after defaults and validation.
type Plan struct {
	Local bool

	Domain      string
	MailHost    string
	WebmailHost string
	Email       string

	Dir     string
	Project string
	Users   []string // local parts, lower-case, without the domain

	StalwartImage string
	IhasmailImage string
	CaddyImage    string

	WebmailBind  string
	StalwartBind string

	Subnet     netip.Prefix
	CaddyIP    netip.Addr
	IhasmailIP netip.Addr
	StalwartIP netip.Addr

	ACMEDirectory string
	ACMECARoot    string // absolute path, or empty
}

// StalwartNames is every hostname Caddy fronts for Stalwart: the mail host
// first, then the service names its ACME order includes.
func (p Plan) StalwartNames() []string {
	names := []string{p.MailHost}
	for _, l := range stalwartServiceLabels {
		names = append(names, l+"."+p.Domain)
	}
	return names
}

// PublishedPorts is every host port the stack binds on all interfaces. Local
// mode binds nothing but the two loopback addresses.
func (p Plan) PublishedPorts() []int {
	if p.Local {
		return nil
	}
	// No 587 or 143: Stalwart 0.16 opens no listener on either by default, and
	// its own DNS zone advertises 465 and 993. 995 is advertised too, so it is
	// published rather than left as an SRV record that points at nothing.
	return []int{25, 80, 443, 465, 993, 995, 4190}
}

var (
	hostnameRE = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$`)
	localRE    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$`)
	projectRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	imageRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*(?::[A-Za-z0-9._-]+)?(?:@sha256:[a-f0-9]{64})?$`)
)

func normalizeHost(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
}

// Validate applies defaults and checks everything, returning every problem at
// once rather than the first: a one-shot tool that makes you run it five times
// to find five typos is not one shot.
func (o Options) Validate() (Plan, error) {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	p := Plan{Local: o.Local}

	p.Domain = normalizeHost(o.Domain)
	switch {
	case p.Domain == "" && o.Local:
		p.Domain = "example.test"
	case p.Domain == "":
		fail("--domain is required: the mail domain this server receives for, e.g. example.com")
	case !hostnameRE.MatchString(p.Domain):
		fail("--domain %q is not a domain name", o.Domain)
	}
	// Defaults below are built from the domain. Past a bad one, report only
	// what was typed: "webmail.bad domain is not a hostname" is the same
	// mistake again, not a second one.
	domainOK := hostnameRE.MatchString(p.Domain)
	if !domainOK {
		p.Domain = "domain.invalid"
	}

	p.MailHost = normalizeHost(o.MailHost)
	if p.MailHost == "" {
		p.MailHost = "mail." + p.Domain
	}
	// Stalwart's ACME certificate and its DNS zone are both built per domain,
	// so a mail host outside the domain would get a certificate that does not
	// name it. One label under the domain is the shape both are sure to cover.
	if label, ok := strings.CutSuffix(p.MailHost, "."+p.Domain); (domainOK && (!ok || strings.Contains(label, "."))) || !hostnameRE.MatchString(p.MailHost) {
		fail("--mail-host %q must be one label under the domain, e.g. mail.%s", o.MailHost, p.Domain)
	}

	p.WebmailHost = normalizeHost(o.WebmailHost)
	if p.WebmailHost == "" {
		p.WebmailHost = "webmail." + p.Domain
	}
	if !hostnameRE.MatchString(p.WebmailHost) {
		fail("--webmail-host %q is not a hostname", o.WebmailHost)
	}
	for _, n := range p.StalwartNames() {
		if n == p.WebmailHost {
			fail("--webmail-host %q is already one of Stalwart's names; give the webmail a name of its own", p.WebmailHost)
		}
	}

	p.Email = strings.TrimSpace(o.Email)
	if p.Email == "" {
		p.Email = "postmaster@" + p.Domain
	}
	if a, err := mail.ParseAddress(p.Email); err != nil || a.Address != p.Email {
		fail("--email %q is not a plain email address", o.Email)
	}

	p.Project = strings.TrimSpace(o.Project)
	if p.Project == "" {
		p.Project = "ihasmail-" + strings.ReplaceAll(p.Domain, ".", "-")
	}
	if !projectRE.MatchString(p.Project) {
		fail("--project %q may hold only lower-case letters, digits, '-' and '_'", p.Project)
	}

	p.Dir = o.Dir
	if p.Dir == "" {
		p.Dir = p.Project
	}
	if abs, err := filepath.Abs(p.Dir); err != nil {
		fail("--dir %q: %v", o.Dir, err)
	} else {
		p.Dir = abs
	}

	seen := map[string]bool{"admin": true}
	for _, u := range o.Users {
		local := strings.ToLower(strings.TrimSpace(u))
		if at := strings.LastIndexByte(local, '@'); at >= 0 {
			if domainOK && local[at+1:] != p.Domain {
				fail("--user %q is not in %s, the only domain this deploys", u, p.Domain)
				continue
			}
			local = local[:at]
		}
		switch {
		case !localRE.MatchString(local):
			fail("--user %q is not a valid mailbox name", u)
		case seen[local]:
			fail("--user %q is given twice, or is the administrator", u)
		default:
			seen[local] = true
			p.Users = append(p.Users, local)
		}
	}

	p.StalwartImage = orDefault(o.StalwartImage, DefaultStalwartImage)
	p.IhasmailImage = orDefault(o.IhasmailImage, NewestIhasmail)
	p.CaddyImage = orDefault(o.CaddyImage, DefaultCaddyImage)
	for flag, img := range map[string]string{"--stalwart-image": p.StalwartImage, "--ihasmail-image": p.IhasmailImage, "--caddy-image": p.CaddyImage} {
		if !imageRE.MatchString(img) {
			fail("%s %q is not an image reference", flag, img)
		}
	}

	p.WebmailBind = orDefault(o.WebmailBind, "127.0.0.1:8080")
	p.StalwartBind = orDefault(o.StalwartBind, "127.0.0.1:8081")
	for flag, b := range map[string]string{"--webmail-bind": p.WebmailBind, "--stalwart-bind": p.StalwartBind} {
		if err := checkBind(b); err != nil {
			fail("%s %q: %v", flag, b, err)
		}
	}
	if p.WebmailBind == p.StalwartBind {
		fail("--webmail-bind and --stalwart-bind are both %s", p.WebmailBind)
	}
	if !p.Local {
		for _, port := range p.PublishedPorts() {
			for flag, b := range map[string]string{"--webmail-bind": p.WebmailBind, "--stalwart-bind": p.StalwartBind} {
				if _, bp, _ := net.SplitHostPort(b); bp == strconv.Itoa(port) {
					fail("%s %q collides with port %d, which the mail host publishes", flag, b, port)
				}
			}
		}
	}

	subnet := orDefault(o.Subnet, "172.31.253.0/24")
	if pfx, err := netip.ParsePrefix(subnet); err != nil || !pfx.Addr().Is4() || pfx.Bits() > 27 || pfx.Masked() != pfx {
		fail("--subnet %q must be an IPv4 network no smaller than a /27, e.g. 172.31.253.0/24", subnet)
	} else {
		p.Subnet = pfx
		// Fixed addresses, because Stalwart is told about two of them: Caddy's
		// forwarded-for header is believed, and ihasmail is exempt from the
		// auto-ban. An address Docker picks afresh on every recreate cannot be
		// written into either.
		base := pfx.Addr().As4()
		at := func(n byte) netip.Addr { b := base; b[3] += n; return netip.AddrFrom4(b) }
		p.CaddyIP, p.IhasmailIP, p.StalwartIP = at(10), at(11), at(12)
	}

	p.ACMEDirectory = strings.TrimSpace(o.ACMEDirectory)
	if o.ACMECARoot != "" {
		if abs, err := filepath.Abs(o.ACMECARoot); err != nil {
			fail("--acme-ca-root %q: %v", o.ACMECARoot, err)
		} else {
			p.ACMECARoot = abs
		}
	}
	if p.Local && (p.ACMEDirectory != "" || p.ACMECARoot != "" || o.Email != "") {
		fail("--acme-directory, --acme-ca-root and --email have no effect with --local, which requests no certificates")
	}
	if p.ACMEDirectory != "" && !strings.HasPrefix(p.ACMEDirectory, "https://") {
		fail("--acme-directory %q must be an https URL", p.ACMEDirectory)
	}

	if len(errs) > 0 {
		return Plan{}, errors.Join(errs...)
	}
	return p, nil
}

func orDefault(s, def string) string {
	if s = strings.TrimSpace(s); s == "" {
		return def
	}
	return s
}

func checkBind(b string) error {
	host, port, err := net.SplitHostPort(b)
	if err != nil {
		return errors.New("must be host:port, e.g. 127.0.0.1:8080")
	}
	if _, err := netip.ParseAddr(host); err != nil {
		return errors.New("the host part must be an IP address")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return errors.New("the port must be 1-65535")
	}
	return nil
}
