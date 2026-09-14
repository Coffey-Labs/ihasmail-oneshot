// SPDX-FileCopyrightText: 2026 Coffey Labs
// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Coffey-Labs/ihasmail-oneshot/internal/config"
)

func plan(t *testing.T, o config.Options) config.Plan {
	t.Helper()
	p, err := o.Validate()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPublicCompose(t *testing.T) {
	p := plan(t, config.Options{Domain: "example.com"})
	out, err := Compose(p, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"name: ihasmail-example-com",
		"hostname: mail.example.com",
		`- "127.0.0.1:8081:8080"`,
		`- "25:25"`, `- "465:465"`, `- "993:993"`, `- "995:995"`, `- "4190:4190"`,
		`- "80:80"`, `- "443:443"`, `- "443:443/udp"`,
		"STALWART_URL: http://stalwart:8080",
		"APP_SECRET: ${APP_SECRET:?",
		"PUSH_URL: https://webmail.example.com",
		"read_only: true",
		"ipv4_address: 172.31.253.11",
		"caddy-data:",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("compose.yaml lacks %q:\n%s", want, s)
		}
	}
	// Caddy's ports belong to Caddy; Stalwart must not also claim them.
	stalwart := s[strings.Index(s, "  stalwart:"):strings.Index(s, "  ihasmail:")]
	if strings.Contains(stalwart, `"80:80"`) || strings.Contains(stalwart, `"443:443"`) {
		t.Errorf("Stalwart publishes 80 or 443:\n%s", stalwart)
	}
	if strings.Contains(s, "ca-bundle.crt") || strings.Contains(s, "acme-ca-root.pem") {
		t.Error("CA files mounted with no private CA")
	}
}

func TestLocalComposeHasNoCaddyAndNoMailPorts(t *testing.T) {
	p := plan(t, config.Options{Local: true})
	out, err := Compose(p, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, unwanted := range []string{"caddy", `"25:25"`, "PUSH_URL"} {
		if strings.Contains(s, unwanted) {
			t.Errorf("local compose.yaml has %q:\n%s", unwanted, s)
		}
	}
}

func TestCaddyfile(t *testing.T) {
	p := plan(t, config.Options{Domain: "example.com", MailHost: "mx.example.com", Email: "ops@example.net"})
	out, err := Caddy(p, "test")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"email ops@example.net",
		"webmail.example.com {",
		"mx.example.com, autoconfig.example.com, autodiscover.example.com, mta-sts.example.com, ua-auto-config.example.com {",
		"disable_http_challenge",
		"http://mx.example.com, http://autoconfig.example.com, http://autodiscover.example.com, http://mta-sts.example.com, http://ua-auto-config.example.com {",
		"handle /.well-known/acme-challenge/* {",
		"flush_interval -1",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Caddyfile lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "acme_ca") {
		t.Error("Caddyfile names a CA without --acme-directory")
	}

	p = plan(t, config.Options{Domain: "example.com", ACMEDirectory: "https://ca.internal/dir", ACMECARoot: "/tmp/root.pem"})
	out, _ = Caddy(p, "test")
	for _, want := range []string{"acme_ca https://ca.internal/dir", "dir https://ca.internal/dir", "trusted_roots /etc/caddy/acme-ca-root.pem"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("Caddyfile with a private CA lacks %q", want)
		}
	}
}

func TestDirectoryIsNeverOverwritten(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "d")
	if err := PrepareDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(dir, EnvFile, []byte("x"), true); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, EnvFile)); fi.Mode().Perm() != 0o600 {
		t.Errorf("secret file mode %v", fi.Mode().Perm())
	}
	if err := WriteFile(dir, EnvFile, []byte("y"), true); err == nil {
		t.Error("WriteFile replaced an existing file")
	}
	if err := PrepareDir(dir); err == nil {
		t.Error("PrepareDir accepted a directory with files in it")
	}
}
