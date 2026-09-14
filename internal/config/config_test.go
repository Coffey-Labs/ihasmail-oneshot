// SPDX-FileCopyrightText: 2026 Coffey Labs
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"slices"
	"strings"
	"testing"
)

func TestDefaultsFollowTheDomain(t *testing.T) {
	p, err := Options{Domain: "Example.COM.", Dir: "/srv/mail"}.Validate()
	if err != nil {
		t.Fatal(err)
	}
	for got, want := range map[string]string{
		p.Domain:      "example.com",
		p.MailHost:    "mail.example.com",
		p.WebmailHost: "webmail.example.com",
		p.Email:       "postmaster@example.com",
		p.Project:     "ihasmail-example-com",
		p.Dir:         "/srv/mail",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if p.CaddyIP.String() != "172.31.253.10" || p.IhasmailIP.String() != "172.31.253.11" || p.StalwartIP.String() != "172.31.253.12" {
		t.Errorf("addresses %s %s %s", p.CaddyIP, p.IhasmailIP, p.StalwartIP)
	}
	want := []string{"mail.example.com", "autoconfig.example.com", "autodiscover.example.com", "mta-sts.example.com", "ua-auto-config.example.com"}
	if !slices.Equal(p.StalwartNames(), want) {
		t.Errorf("StalwartNames = %v", p.StalwartNames())
	}
}

func TestLocalNeedsNoDomainAndPublishesNothing(t *testing.T) {
	p, err := Options{Local: true}.Validate()
	if err != nil {
		t.Fatal(err)
	}
	if p.Domain != "example.test" || len(p.PublishedPorts()) != 0 {
		t.Errorf("domain %q, ports %v", p.Domain, p.PublishedPorts())
	}
}

func TestRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		o    Options
		want string
	}{
		"no domain":              {Options{}, "--domain is required"},
		"bad domain":             {Options{Domain: "not a domain"}, `--domain "not a domain"`},
		"mail host elsewhere":    {Options{Domain: "example.com", MailHost: "mx.other.net"}, "one label under the domain"},
		"mail host two deep":     {Options{Domain: "example.com", MailHost: "a.b.example.com"}, "one label under the domain"},
		"webmail is autoconfig":  {Options{Domain: "example.com", WebmailHost: "autoconfig.example.com"}, "already one of Stalwart's names"},
		"webmail is mail host":   {Options{Domain: "example.com", WebmailHost: "mail.example.com"}, "already one of Stalwart's names"},
		"user in another domain": {Options{Domain: "example.com", Users: []string{"bob@example.net"}}, "not in example.com"},
		"user is admin":          {Options{Domain: "example.com", Users: []string{"admin"}}, "or is the administrator"},
		"duplicate user":         {Options{Domain: "example.com", Users: []string{"bob", "BOB@example.com"}}, "given twice"},
		"bad user":               {Options{Domain: "example.com", Users: []string{"bob smith"}}, "not a valid mailbox name"},
		"bind not host:port":     {Options{Domain: "example.com", WebmailBind: "8080"}, "--webmail-bind"},
		"bind on a mail port":    {Options{Domain: "example.com", StalwartBind: "127.0.0.1:443"}, "collides with port 443"},
		"same binds":             {Options{Domain: "example.com", WebmailBind: "127.0.0.1:9000", StalwartBind: "127.0.0.1:9000"}, "are both"},
		"subnet too small":       {Options{Domain: "example.com", Subnet: "10.0.0.0/29"}, "--subnet"},
		"subnet not a network":   {Options{Domain: "example.com", Subnet: "10.0.0.5/24"}, "--subnet"},
		"acme flags with local":  {Options{Local: true, ACMEDirectory: "https://ca.internal/dir"}, "no effect with --local"},
		"acme over http":         {Options{Domain: "example.com", ACMEDirectory: "http://ca.internal/dir"}, "must be an https URL"},
		"image with a space":     {Options{Domain: "example.com", CaddyImage: "caddy 2"}, "--caddy-image"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tc.o.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A bad domain is one mistake, not a cascade of defaults built from it.
func TestBadDomainIsReportedOnce(t *testing.T) {
	_, err := Options{Domain: "bad domain"}.Validate()
	if err == nil {
		t.Fatal("no error")
	}
	if lines := strings.Split(err.Error(), "\n"); len(lines) != 1 {
		t.Errorf("got %d errors: %v", len(lines), err)
	}
}

func TestEveryProblemAtOnce(t *testing.T) {
	_, err := Options{Domain: "example.com", MailHost: "mx.other.net", Users: []string{"bob smith"}, WebmailBind: "x"}.Validate()
	if err == nil {
		t.Fatal("no error")
	}
	if lines := strings.Split(err.Error(), "\n"); len(lines) != 3 {
		t.Errorf("got %d errors, want 3: %v", len(lines), err)
	}
}
