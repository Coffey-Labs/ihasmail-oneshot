// SPDX-FileCopyrightText: 2026 Coffey Labs
// SPDX-License-Identifier: AGPL-3.0-or-later

// Command ihasmail-oneshot deploys a fresh Stalwart and a fresh ihasmail on
// one Docker host, linked, in one command.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Coffey-Labs/ihasmail-oneshot/internal/config"
	"github.com/Coffey-Labs/ihasmail-oneshot/internal/deploy"
)

// version is set at build time: -ldflags "-X main.version=...".
var version = "dev"

const usageText = `ihasmail-oneshot deploys a fresh Stalwart mail server and a fresh ihasmail
webmail on this Docker host, linked together, in one command.

Usage:
  ihasmail-oneshot deploy --domain example.com [flags]   a mail host on the internet
  ihasmail-oneshot deploy --local [flags]                a loopback-only pair to try it
  ihasmail-oneshot certs --dir DIR                       retry Stalwart's certificate after fixing DNS
  ihasmail-oneshot destroy --dir DIR [--yes]             remove a deployment and all its data
  ihasmail-oneshot version

Run a command with -h for its flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "deploy":
		err = runDeploy(ctx, os.Args[2:])
	case "certs":
		err = runCerts(ctx, os.Args[2:])
	case "destroy":
		err = runDestroy(ctx, os.Args[2:])
	case "version", "--version":
		fmt.Println("ihasmail-oneshot", version)
	case "help", "-h", "--help":
		fmt.Print(usageText)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usageText)
		os.Exit(2)
	}
	if errors.Is(err, flag.ErrHelp) {
		return // -h printed its usage, which is what was asked for
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "!!  %v\n", err)
		os.Exit(1)
	}
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func runDeploy(ctx context.Context, args []string) error {
	var o config.Options
	var yes bool
	var users stringList
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: ihasmail-oneshot deploy --domain example.com [flags]
       ihasmail-oneshot deploy --local [flags]

Without --local this publishes SMTP (25), submissions (465), IMAPS (993),
POP3S (995), ManageSieve (4190) and HTTP/HTTPS (80, 443) on every interface,
and asks Let's Encrypt for certificates. With --local it publishes nothing
but the two loopback addresses below and asks for no certificates.

Flags:
`)
		fs.PrintDefaults()
	}
	fs.BoolVar(&o.Local, "local", false, "loopback-only evaluation pair: no mail ports, no Caddy, no certificates")
	fs.StringVar(&o.Domain, "domain", "", "mail domain, e.g. example.com (default example.test with --local)")
	fs.StringVar(&o.MailHost, "mail-host", "", "Stalwart's hostname, one label under the domain (default mail.DOMAIN)")
	fs.StringVar(&o.WebmailHost, "webmail-host", "", "the webmail's hostname (default webmail.DOMAIN)")
	fs.StringVar(&o.Email, "email", "", "ACME contact address (default postmaster@DOMAIN)")
	fs.Var(&users, "user", "create a mailbox with a generated password; repeat for more (e.g. --user alice)")
	fs.StringVar(&o.Dir, "dir", "", "deployment directory to write, new or empty (default ./PROJECT)")
	fs.StringVar(&o.Project, "project", "", "compose project name (default ihasmail-DOMAIN, dots as dashes)")
	fs.StringVar(&o.StalwartImage, "stalwart-image", config.DefaultStalwartImage, "Stalwart image")
	fs.StringVar(&o.IhasmailImage, "ihasmail-image", config.DefaultIhasmailImage, "ihasmail image")
	fs.StringVar(&o.CaddyImage, "caddy-image", config.DefaultCaddyImage, "Caddy image")
	fs.StringVar(&o.WebmailBind, "webmail-bind", "127.0.0.1:8080", "host address for ihasmail's own port")
	fs.StringVar(&o.StalwartBind, "stalwart-bind", "127.0.0.1:8081", "host address for Stalwart's plain-HTTP port (admin UI)")
	fs.StringVar(&o.Subnet, "subnet", "172.31.253.0/24", "private network for the stack")
	fs.StringVar(&o.ACMEDirectory, "acme-directory", "", "ACME directory of a private CA, instead of Let's Encrypt")
	fs.StringVar(&o.ACMECARoot, "acme-ca-root", "", "PEM root that the private ACME directory's HTTPS is signed by")
	fs.BoolVar(&yes, "yes", false, "do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	o.Users = users

	plan, err := o.Validate()
	if err != nil {
		return err
	}
	log := deploy.Log{W: os.Stdout}

	describe(plan, log)
	log.Step("preflight")
	warnings, err := deploy.Preflight(ctx, plan, log)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		log.Warn("%s", w)
	}
	if !yes {
		if err := confirm("deploy this?"); err != nil {
			return err
		}
	}

	res, err := deploy.Deploy(ctx, plan, version, log)
	if err != nil {
		return err
	}
	summarise(plan, res, log)
	return nil
}

func describe(p config.Plan, log deploy.Log) {
	if p.Local {
		log.Step("a local pair for %s, in %s", p.Domain, p.Dir)
		log.Info("ihasmail  http://%s", p.WebmailBind)
		log.Info("Stalwart  http://%s  (admin UI; no mail ports published)", p.StalwartBind)
	} else {
		log.Step("a mail host for %s, in %s", p.Domain, p.Dir)
		log.Info("webmail   https://%s", p.WebmailHost)
		log.Info("Stalwart  %s  (admin UI at https://%s/admin)", p.MailHost, p.MailHost)
		log.Info("ports     %s on every interface", joinInts(p.PublishedPorts()))
		if p.ACMEDirectory != "" {
			log.Info("ACME      %s", p.ACMEDirectory)
		} else {
			log.Info("ACME      Let's Encrypt, contact %s", p.Email)
		}
	}
	log.Info("images    %s, %s", p.StalwartImage, p.IhasmailImage)
	if !p.Local {
		log.Info("          %s", p.CaddyImage)
	}
	if len(p.Users) > 0 {
		log.Info("mailboxes %s", strings.Join(p.Users, ", "))
	}
}

func summarise(p config.Plan, r *deploy.Result, log deploy.Log) {
	log.Step("done")
	if p.Local {
		log.Info("webmail      http://%s", p.WebmailBind)
		log.Info("Stalwart     http://%s/admin", p.StalwartBind)
	} else {
		log.Info("webmail      https://%s", p.WebmailHost)
		log.Info("Stalwart     https://%s/admin", p.MailHost)
	}
	log.Info("sign in as   %s (password in %s/credentials.txt)", r.Admin.Username, p.Dir)
	if r.AdminInWebmail {
		log.Info("             the administrator gets the webmail's Administration menu")
	}
	for addr := range r.Mailboxes {
		log.Info("mailbox      %s", addr)
	}
	if p.Local {
		log.Info("")
		log.Info("Nothing can reach this pair from outside the host, and no mail can be")
		log.Info("delivered to it. Remove it with: ihasmail-oneshot destroy --dir %s", p.Dir)
		return
	}
	log.Info("DNS          %s/dns-records.zone -- publish every record in it", p.Dir)
	if r.Certificate != nil {
		log.Info("certificate  issued by %s, valid until %s", r.Certificate.Issuer, r.Certificate.NotValidAfter)
	} else {
		log.Warn("Stalwart has no certificate yet, so IMAP and SMTP clients will refuse it.")
		log.Info("    Once %s resolves to this host: ihasmail-oneshot certs --dir %s", p.MailHost, p.Dir)
	}
	for _, host := range []string{p.WebmailHost, p.MailHost} {
		if issuer, ok := r.CaddyCertificates[host]; ok {
			log.Info("https        %s: issued by %s", host, issuer)
		} else {
			log.Warn("Caddy has no certificate for %s yet; it keeps retrying by itself once DNS points here.", host)
		}
	}
	log.Info("")
	log.Info("Before mail flows: reverse DNS for this host's address should name %s,", p.MailHost)
	log.Info("and outbound port 25 must be open -- many providers block it until asked.")
}

func runCerts(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("certs", flag.ContinueOnError)
	dir := fs.String("dir", "", "deployment directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return errors.New("--dir is required")
	}
	return deploy.Certs(ctx, *dir, deploy.Log{W: os.Stdout})
}

func runDestroy(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("destroy", flag.ContinueOnError)
	dir := fs.String("dir", "", "deployment directory")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return errors.New("--dir is required")
	}
	if !*yes {
		if err := confirm(fmt.Sprintf("destroy the deployment in %s, with every mailbox and message in it?", *dir)); err != nil {
			return err
		}
	}
	return deploy.Destroy(ctx, *dir, deploy.Log{W: os.Stdout})
}

// confirm asks on the terminal, and refuses outright without one: a script
// that forgot --yes should stop, not hang or guess.
func confirm(question string) error {
	if fi, err := os.Stdin.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return errors.New("no terminal to confirm on; pass --yes if this is what you mean")
	}
	fmt.Printf("%s [y/N] ", question)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	}
	return errors.New("aborted")
}

func joinInts(ns []int) string {
	s := make([]string, len(ns))
	for i, n := range ns {
		s[i] = fmt.Sprint(n)
	}
	return strings.Join(s, ", ")
}
