// SPDX-FileCopyrightText: 2026 Coffey Labs
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package deploy is the one shot: preflight, write the directory, bootstrap
// Stalwart, bring the stack up, link and verify it.
package deploy

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Coffey-Labs/ihasmail-oneshot/internal/config"
	"github.com/Coffey-Labs/ihasmail-oneshot/internal/docker"
	"github.com/Coffey-Labs/ihasmail-oneshot/internal/render"
	"github.com/Coffey-Labs/ihasmail-oneshot/internal/stalwart"
	"github.com/Coffey-Labs/ihasmail-oneshot/internal/webmail"
)

// Log is where progress goes. Each step says what it is doing before it does
// it, and every wait longer than a few seconds says so while it waits.
type Log struct{ W io.Writer }

func (l Log) Step(format string, a ...any) { fmt.Fprintf(l.W, "==> "+format+"\n", a...) }
func (l Log) Info(format string, a ...any) { fmt.Fprintf(l.W, "    "+format+"\n", a...) }
func (l Log) Warn(format string, a ...any) { fmt.Fprintf(l.W, "!!  "+format+"\n", a...) }

// Preflight checks everything that can be checked without changing anything.
// Warnings are things that will not stop the deployment but will stop it
// being useful until they are fixed, like DNS that does not point here yet.
func Preflight(ctx context.Context, p config.Plan, log Log) (warnings []string, err error) {
	engine, compose, err := docker.Versions(ctx)
	if err != nil {
		return nil, err
	}
	log.Info("docker %s, compose %s", engine, compose)

	var problems []error
	if leftovers, err := docker.ProjectLeftovers(ctx, p.Project); err != nil {
		problems = append(problems, err)
	} else if len(leftovers) > 0 {
		problems = append(problems, fmt.Errorf("project %s already exists in Docker (%s); destroy it first or choose another --project", p.Project, strings.Join(leftovers, ", ")))
	}

	if entries, err := os.ReadDir(p.Dir); err == nil && len(entries) > 0 {
		problems = append(problems, fmt.Errorf("%s already has files in it; give --dir a new or empty directory", p.Dir))
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		problems = append(problems, err)
	}

	if p.ACMECARoot != "" {
		if _, err := os.Stat(p.ACMECARoot); err != nil {
			problems = append(problems, fmt.Errorf("--acme-ca-root: %w", err))
		}
	}

	addrs := []string{p.WebmailBind, p.StalwartBind}
	for _, port := range p.PublishedPorts() {
		addrs = append(addrs, fmt.Sprintf(":%d", port))
	}
	for _, a := range addrs {
		if err := portFree(a); err != nil {
			problems = append(problems, err)
		}
	}

	if !p.Local {
		for _, host := range []string{p.WebmailHost, p.MailHost} {
			if ips, err := net.DefaultResolver.LookupHost(ctx, host); err != nil || len(ips) == 0 {
				warnings = append(warnings, fmt.Sprintf("%s does not resolve yet: its certificate cannot be issued until it points at this host", host))
			}
		}
	}
	return warnings, errors.Join(problems...)
}

// portFree tries to bind an address. A permission error means an unprivileged
// user asking about a low port, which says nothing about whether Docker can
// have it, so it is not reported.
func portFree(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err == nil {
		return l.Close()
	}
	if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		return nil
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return fmt.Errorf("port %s is already in use on this host", strings.TrimPrefix(addr, ":"))
	}
	return fmt.Errorf("cannot bind %s: %w", addr, err)
}

// Result is what a successful deployment reports.
type Result struct {
	Admin           stalwart.Admin
	Mailboxes       map[string]string
	IhasmailVersion string
	AdminInWebmail  bool
	Certificate     *stalwart.Certificate
	// Caddy's certificates, by hostname, as the issuer's name. A name missing
	// here had none by the time the tool stopped waiting.
	CaddyCertificates map[string]string
}

// Deploy runs the whole thing. On an error after containers exist, it leaves
// them as they are for inspection and says how to start over.
func Deploy(ctx context.Context, p config.Plan, version string, log Log) (*Result, error) {
	log.Step("writing %s", p.Dir)
	if err := render.PrepareDir(p.Dir); err != nil {
		return nil, err
	}
	appSecret, err := randomBase64(48)
	if err != nil {
		return nil, err
	}
	bootPassword := randomPassword(32)

	caBundle := p.ACMECARoot != ""
	composeYAML, err := render.Compose(p, version, caBundle)
	if err != nil {
		return nil, err
	}
	if err := render.WriteFile(p.Dir, render.ComposeFile, composeYAML, false); err != nil {
		return nil, err
	}
	if err := render.WriteFile(p.Dir, render.EnvFile, render.Env(appSecret), true); err != nil {
		return nil, err
	}
	if !p.Local {
		caddyfile, err := render.Caddy(p, version)
		if err != nil {
			return nil, err
		}
		if err := render.WriteFile(p.Dir, render.Caddyfile, caddyfile, false); err != nil {
			return nil, err
		}
	}

	compose := docker.Compose{Dir: p.Dir, Out: indent(log.W)}

	log.Step("pulling images")
	if err := compose.Run(ctx, "pull", "--quiet"); err != nil {
		return nil, err
	}

	if caBundle {
		root, err := os.ReadFile(p.ACMECARoot)
		if err != nil {
			return nil, err
		}
		system, err := docker.SystemCABundle(ctx, p.StalwartImage)
		if err != nil {
			return nil, fmt.Errorf("reading the CA bundle out of %s: %w", p.StalwartImage, err)
		}
		if err := render.WriteFile(p.Dir, render.CARootFile, root, false); err != nil {
			return nil, err
		}
		if err := render.WriteFile(p.Dir, render.CABundleFile, append(system, root...), false); err != nil {
			return nil, err
		}
	}

	// From here on there are containers, so a failure says how to clear them.
	res, err := bringUp(ctx, p, compose, bootPassword, log)
	if err != nil {
		return res, fmt.Errorf("%w\n\nThe stack is left as it is, to look at. To start again from nothing:\n    ihasmail-oneshot destroy --dir %s --yes", err, p.Dir)
	}
	return res, nil
}

func bringUp(ctx context.Context, p config.Plan, compose docker.Compose, bootPassword string, log Log) (*Result, error) {
	stalwartURL := "http://" + p.StalwartBind
	res := &Result{Mailboxes: map[string]string{}, CaddyCertificates: map[string]string{}}

	// --- bootstrap -----------------------------------------------------------
	// The bootstrap account comes from an override file that lives only for
	// this step, and its password only in this process's environment. Bringing
	// the stack up afterwards without the override recreates Stalwart without
	// the variable, so no fixed recovery credential outlives the setup.
	override, err := os.CreateTemp("", "ihasmail-oneshot-bootstrap-*.yaml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(override.Name())
	if _, err := override.WriteString("services:\n  stalwart:\n    environment:\n      STALWART_RECOVERY_ADMIN: ${ONESHOT_BOOTSTRAP_ADMIN:?}\n"); err != nil {
		return nil, err
	}
	override.Close()

	log.Step("starting Stalwart in bootstrap mode")
	boot := compose
	boot.Files = []string{override.Name()}
	boot.Env = []string{"ONESHOT_BOOTSTRAP_ADMIN=admin:" + bootPassword}
	if err := boot.Run(ctx, "up", "-d", "stalwart"); err != nil {
		return nil, err
	}
	if err := waitFor(ctx, log, "Stalwart", 90*time.Second, func(ctx context.Context) error {
		return stalwart.Live(ctx, stalwartURL)
	}); err != nil {
		return nil, withLogs(ctx, err, compose, "stalwart")
	}

	bootClient := &stalwart.Client{BaseURL: stalwartURL, Username: "admin", Password: bootPassword}
	if err := bootClient.CheckBootstrapMode(ctx); err != nil {
		return nil, err
	}
	log.Step("setting up Stalwart for %s (hostname %s)", p.Domain, p.MailHost)
	admin, err := bootClient.Bootstrap(ctx, p.MailHost, p.Domain)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: %w", err)
	}
	res.Admin = admin
	// Written now, not at the end: from this moment the password exists nowhere
	// else, and a failure in a later step must not lose it.
	if err := writeCredentials(p, admin); err != nil {
		return res, err
	}
	log.Info("administrator %s, password in %s", admin.Username, render.CredentialsFile)

	// --- the whole stack -----------------------------------------------------
	log.Step("starting the stack")
	if err := compose.Run(ctx, "up", "-d"); err != nil {
		return res, err
	}
	sw := &stalwart.Client{BaseURL: stalwartURL, Username: admin.Username, Password: admin.Secret}
	if err := waitFor(ctx, log, "Stalwart to restart configured", 90*time.Second, func(ctx context.Context) error {
		_, err := sw.DomainID(ctx, p.Domain)
		return err
	}); err != nil {
		return res, withLogs(ctx, err, compose, "stalwart")
	}
	domainID, err := sw.DomainID(ctx, p.Domain)
	if err != nil {
		return res, err
	}

	// --- linking -------------------------------------------------------------
	log.Step("linking ihasmail and Stalwart")
	// Every request the webmail makes reaches Stalwart from ihasmail's one
	// address -- every sign-in, every push stream opened and dropped as tabs
	// come and go. Stalwart bans per address, so a ban on that one would be a
	// ban on everybody's webmail. ihasmail rate-limits sign-ins per real client
	// itself. (Checked on 0.16.22: failed sign-ins were refused per session but
	// never became an address ban; scans and dropped connections are counted,
	// and this is not a limit worth finding by losing the webmail to it.)
	if err := sw.AllowIP(ctx, p.IhasmailIP.String(), "ihasmail: every webmail request arrives from this address"); err != nil {
		return res, fmt.Errorf("exempting ihasmail from the auto-ban: %w", err)
	}
	log.Info("ihasmail (%s) exempt from Stalwart's auto-ban", p.IhasmailIP)
	if !p.Local {
		if err := sw.TrustForwardedFor(ctx); err != nil {
			return res, fmt.Errorf("trusting Caddy's X-Forwarded-For: %w", err)
		}
		log.Info("Stalwart takes client addresses from Caddy's X-Forwarded-For")
	}
	// Neither setting takes effect on a running Stalwart: on 0.16.22 a scan
	// through Caddy straight after setting them still banned Caddy, and the
	// same scan after a restart banned the scanner. Nor does lifting a ban.
	log.Info("restarting Stalwart to apply them")
	if err := compose.Run(ctx, "restart", "stalwart"); err != nil {
		return res, err
	}
	if err := waitFor(ctx, log, "Stalwart to restart", 90*time.Second, func(ctx context.Context) error {
		_, err := sw.DomainID(ctx, p.Domain)
		return err
	}); err != nil {
		return res, withLogs(ctx, err, compose, "stalwart")
	}

	for _, name := range p.Users {
		password := randomPassword(24)
		if _, err := sw.CreateUser(ctx, name, domainID, password); err != nil {
			return res, fmt.Errorf("creating mailbox %s@%s: %w", name, p.Domain, err)
		}
		address := name + "@" + p.Domain
		res.Mailboxes[address] = password
		if err := render.AppendFile(p.Dir, render.CredentialsFile, []byte(fmt.Sprintf("mailbox %s = %s\n", address, password))); err != nil {
			return res, err
		}
		log.Info("mailbox %s created", address)
	}

	webmailURL := "http://" + p.WebmailBind
	if err := waitFor(ctx, log, "ihasmail", 60*time.Second, func(ctx context.Context) error {
		h, err := webmail.CheckHealth(ctx, webmailURL)
		res.IhasmailVersion = h.Version
		return err
	}); err != nil {
		return res, withLogs(ctx, err, compose, "ihasmail")
	}
	// Retried briefly: ihasmail can report healthy a moment before its first
	// session discovery against a Stalwart that has only just restarted.
	if err := waitFor(ctx, log, "a sign-in through ihasmail", 30*time.Second, func(ctx context.Context) error {
		ok, err := webmail.SignIn(ctx, webmailURL, admin.Username, admin.Secret)
		res.AdminInWebmail = ok
		return err
	}); err != nil {
		return res, withLogs(ctx, err, compose, "ihasmail")
	}
	log.Info("signed in to ihasmail %s as %s: linked", res.IhasmailVersion, admin.Username)

	if p.Local {
		return res, nil
	}

	// --- certificates and DNS -----------------------------------------------
	log.Step("requesting Stalwart's certificate")
	if err := waitFor(ctx, log, "Caddy", 30*time.Second, func(ctx context.Context) error {
		if !compose.Running(ctx, "caddy") {
			return errors.New("not running")
		}
		return nil
	}); err != nil {
		return res, withLogs(ctx, err, compose, "caddy")
	}
	if _, err := sw.EnableACME(ctx, domainID, p.ACMEDirectory, p.Email); err != nil {
		return res, fmt.Errorf("enabling ACME: %w", err)
	}
	// Not an error if it does not arrive: DNS that does not point here yet is
	// the usual reason, and the fix is DNS and then `certs`, not a redeploy.
	_ = waitFor(ctx, log, "the certificate", 90*time.Second, func(ctx context.Context) error {
		certs, err := sw.Certificates(ctx)
		if err != nil {
			return err
		}
		for _, c := range certs {
			if c.SubjectAlternativeNames[p.MailHost] {
				res.Certificate = &c
				return nil
			}
		}
		return errors.New("not issued yet")
	})

	// Caddy obtains its own in the background. Waited for, so that "done"
	// means the HTTPS it prints works -- and, like Stalwart's, not an error
	// when it does not arrive.
	for _, host := range []string{p.WebmailHost, p.MailHost} {
		_ = waitFor(ctx, log, "Caddy's certificate for "+host, 60*time.Second, func(ctx context.Context) error {
			issuer, err := servedCertificate(ctx, host)
			if err == nil {
				res.CaddyCertificates[host] = issuer
			}
			return err
		})
	}

	zone, err := sw.DNSZone(ctx, domainID)
	if err != nil {
		return res, fmt.Errorf("reading the DNS records: %w", err)
	}
	if err := render.WriteFile(p.Dir, render.DNSFile, []byte(dnsFile(p, zone)), false); err != nil {
		return res, err
	}
	return res, nil
}

// servedCertificate reports the issuer of the certificate Caddy presents for
// host on this machine's port 443, without trusting it: the question is
// whether Caddy has one yet, not whether this host trusts the CA. Until it has
// one the handshake fails outright.
func servedCertificate(ctx context.Context, host string) (string, error) {
	d := tls.Dialer{Config: &tls.Config{ServerName: host, InsecureSkipVerify: true}}
	conn, err := d.DialContext(ctx, "tcp", "127.0.0.1:443")
	if err != nil {
		return "", err
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("no certificate presented")
	}
	if err := certs[0].VerifyHostname(host); err != nil {
		return "", err
	}
	return certs[0].Issuer.String(), nil
}

// waitFor retries check until it passes or timeout passes, saying every ten
// seconds that it is still waiting and what the last answer was.
func waitFor(ctx context.Context, log Log, what string, timeout time.Duration, check func(context.Context) error) error {
	start := time.Now()
	deadline := start.Add(timeout)
	lastReport := start
	for {
		attempt, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := check(attempt)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("gave up waiting for %s after %s: %w", what, timeout, err)
		}
		if time.Since(lastReport) >= 10*time.Second {
			log.Info("still waiting for %s (%s): %v", what, time.Since(start).Round(time.Second), err)
			lastReport = time.Now()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func withLogs(ctx context.Context, err error, compose docker.Compose, service string) error {
	return fmt.Errorf("%w\n\nlast lines of %s's log:\n%s", err, service, compose.Logs(ctx, service, 25))
}

func writeCredentials(p config.Plan, a stalwart.Admin) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# ihasmail-oneshot credentials for %s, written %s.\n", p.Domain, time.Now().UTC().Format(time.RFC3339))
	b.WriteString("# Keep this file private. The administrator can sign in to the webmail too,\n")
	b.WriteString("# and to Stalwart's own admin UI. Change the passwords after first sign-in.\n")
	fmt.Fprintf(&b, "domain = %s\n", p.Domain)
	fmt.Fprintf(&b, "mail_host = %s\n", p.MailHost)
	fmt.Fprintf(&b, "stalwart_url = http://%s\n", p.StalwartBind)
	fmt.Fprintf(&b, "admin = %s\n", a.Username)
	fmt.Fprintf(&b, "admin_password = %s\n", a.Secret)
	return render.WriteFile(p.Dir, render.CredentialsFile, []byte(b.String()), true)
}

func dnsFile(p config.Plan, zone string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "; DNS records for %s, from Stalwart. Publish all of them.\n", p.Domain)
	b.WriteString(";\n; Two more that Stalwart cannot know, because they are this host's address:\n")
	for _, h := range []string{p.MailHost, p.WebmailHost} {
		fmt.Fprintf(&b, ";   %s. IN A     <this host's IPv4 address>\n", h)
		fmt.Fprintf(&b, ";   %s. IN AAAA  <this host's IPv6 address, if it has one>\n", h)
	}
	fmt.Fprintf(&b, ";\n; And one at your hosting provider rather than in this zone: reverse DNS (PTR)\n; for this host's address, pointing at %s.\n\n", p.MailHost)
	b.WriteString(zone)
	if !strings.HasSuffix(zone, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// Destroy removes the stack, its volumes, and the files the tool wrote. Mail,
// accounts and certificates go with the volumes; that is the point of it.
func Destroy(ctx context.Context, dir string, log Log) error {
	if _, err := os.Stat(filepath.Join(dir, render.ComposeFile)); err != nil {
		return fmt.Errorf("%s is not a deployment directory: %w", dir, err)
	}
	log.Step("removing containers, networks and volumes")
	compose := docker.Compose{Dir: dir, Out: indent(log.W)}
	// APP_SECRET is required by compose.yaml's interpolation, and a missing
	// .env must not make the stack impossible to remove.
	compose.Env = []string{"APP_SECRET=unused-by-down"}
	if err := compose.Run(ctx, "down", "--volumes", "--remove-orphans"); err != nil {
		return err
	}
	log.Step("removing the files it wrote")
	for _, name := range render.Written {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Remove(dir); err != nil {
		log.Info("left %s in place: it holds files this tool did not write", dir)
	}
	return nil
}

// Certs starts a new certificate order, for after DNS has been fixed.
func Certs(ctx context.Context, dir string, log Log) error {
	creds, err := ReadCredentials(filepath.Join(dir, render.CredentialsFile))
	if err != nil {
		return err
	}
	sw := &stalwart.Client{BaseURL: creds["stalwart_url"], Username: creds["admin"], Password: creds["admin_password"]}
	domainID, err := sw.DomainID(ctx, creds["domain"])
	if err != nil {
		return err
	}
	covering := func(ctx context.Context) (*stalwart.Certificate, error) {
		certs, err := sw.Certificates(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range certs {
			if c.SubjectAlternativeNames[creds["mail_host"]] {
				return &c, nil
			}
		}
		return nil, nil
	}
	// A new order while a certificate is still valid is refused by Stalwart as
	// "renewal not due", so asking would only look like it had done something.
	if c, err := covering(ctx); err != nil {
		return err
	} else if c != nil {
		log.Info("Stalwart already holds a certificate for %s, issued by %s, valid until %s", creds["mail_host"], c.Issuer, c.NotValidAfter)
		return nil
	}
	log.Step("starting a new certificate order for %s", creds["domain"])
	if err := sw.RetryCertificates(ctx, domainID); err != nil {
		return err
	}
	var got *stalwart.Certificate
	err = waitFor(ctx, log, "the certificate", 90*time.Second, func(ctx context.Context) error {
		c, err := covering(ctx)
		if err == nil && c == nil {
			err = errors.New("not issued yet")
		}
		got = c
		return err
	})
	if err != nil {
		return fmt.Errorf("%w\n    Stalwart's log says why: docker compose --project-directory %s logs stalwart | grep -i acme", err, dir)
	}
	log.Info("issued by %s, valid until %s", got.Issuer, got.NotValidAfter)
	return nil
}

// ReadCredentials parses credentials.txt's "key = value" lines.
func ReadCredentials(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, " = "); ok {
			out[k] = v
		}
	}
	for _, k := range []string{"domain", "mail_host", "stalwart_url", "admin", "admin_password"} {
		if out[k] == "" {
			return nil, fmt.Errorf("%s has no %s", path, k)
		}
	}
	return out, nil
}

func randomBase64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// randomPassword is letters and digits only, so it survives YAML, an env file,
// a shell and being read aloud without any quoting.
func randomPassword(n int) string {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, n)
	buf := make([]byte, 1)
	for i := 0; i < n; {
		if _, err := rand.Read(buf); err != nil {
			panic(err) // crypto/rand.Read does not fail on supported platforms
		}
		// Rejection sampling keeps every character equally likely.
		if int(buf[0]) < 256-256%len(alphabet) {
			out[i] = alphabet[int(buf[0])%len(alphabet)]
			i++
		}
	}
	return string(out)
}

type indentWriter struct {
	w   io.Writer
	bol bool
}

func (iw *indentWriter) Write(p []byte) (int, error) {
	for _, c := range p {
		if iw.bol {
			if _, err := iw.w.Write([]byte("    ")); err != nil {
				return 0, err
			}
		}
		if _, err := iw.w.Write([]byte{c}); err != nil {
			return 0, err
		}
		iw.bol = c == '\n'
	}
	return len(p), nil
}

// indent passes docker's own output through, indented under the step it
// belongs to.
func indent(w io.Writer) io.Writer { return &indentWriter{w: w, bol: true} }
