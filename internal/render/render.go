// SPDX-FileCopyrightText: 2026 Coffey Labs
// SPDX-License-Identifier: GPL-3.0-or-later

// Package render writes the deployment directory: compose.yaml, the Caddyfile,
// and the files holding secrets.
package render

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Coffey-Labs/ihasmail-oneshot/internal/config"
)

//go:embed templates/*.tmpl
var templates embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"join": strings.Join,
}).ParseFS(templates, "templates/*.tmpl"))

// Files the tool writes into a deployment directory, and nothing else. Destroy
// removes exactly these, so a file the operator added survives it.
const (
	ComposeFile     = "compose.yaml"
	Caddyfile       = "Caddyfile"
	EnvFile         = ".env"
	CredentialsFile = "credentials.txt"
	DNSFile         = "dns-records.zone"
	CARootFile      = "acme-ca-root.pem"
	CABundleFile    = "ca-bundle.crt"
)

// Written lists every file name Destroy may remove.
var Written = []string{ComposeFile, Caddyfile, EnvFile, CredentialsFile, DNSFile, CARootFile, CABundleFile}

type data struct {
	Version  string
	Plan     config.Plan
	CABundle bool
}

// Compose renders compose.yaml.
func Compose(p config.Plan, version string, caBundle bool) ([]byte, error) {
	return execute("compose.yaml.tmpl", data{Version: version, Plan: p, CABundle: caBundle})
}

// Caddy renders the Caddyfile.
func Caddy(p config.Plan, version string) ([]byte, error) {
	return execute("Caddyfile.tmpl", data{Version: version, Plan: p})
}

func execute(name string, d data) ([]byte, error) {
	var b bytes.Buffer
	if err := tmpl.ExecuteTemplate(&b, name, d); err != nil {
		return nil, fmt.Errorf("render %s: %w", name, err)
	}
	return b.Bytes(), nil
}

// Env renders .env. Only the app secret lives here: compose.yaml reads it by
// interpolation, so the file compose.yaml sits in can be shown to someone
// without showing them the key every session is sealed with.
func Env(appSecret string) []byte {
	return []byte("# Read by docker compose. Changing APP_SECRET signs everyone out.\nAPP_SECRET=" + appSecret + "\n")
}

// PrepareDir creates dir, or accepts it if it exists and is empty. Anything in
// it already is refused: the tool writes a fresh deployment, and silently
// replacing someone's compose.yaml is not a fresh deployment.
func PrepareDir(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return os.MkdirAll(dir, 0o750)
	case err != nil:
		return err
	case len(entries) > 0:
		return fmt.Errorf("%s already has files in it; give --dir a new or empty directory", dir)
	}
	return nil
}

// WriteFile writes one file into dir, private if it holds a secret. It refuses
// to replace an existing file, for the same reason PrepareDir refuses a full
// directory.
func WriteFile(dir, name string, content []byte, secret bool) error {
	mode := os.FileMode(0o644)
	if secret {
		mode = 0o600
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// AppendFile adds to a file WriteFile created, keeping its mode.
func AppendFile(dir, name string, content []byte) error {
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
