// SPDX-FileCopyrightText: 2026 Coffey Labs
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package webmail checks a running ihasmail from the outside, the way a
// browser would reach it.
package webmail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

// Health is the part of /api/health the tool reports.
type Health struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
}

// CheckHealth reads /api/health once.
func CheckHealth(ctx context.Context, baseURL string) (Health, error) {
	var h Health
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/health", nil)
	if err != nil {
		return h, err
	}
	res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return h, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return h, fmt.Errorf("/api/health answered HTTP %d", res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(&h); err != nil {
		return h, fmt.Errorf("/api/health: %w", err)
	}
	if !h.OK {
		return h, fmt.Errorf("/api/health reports not ok")
	}
	return h, nil
}

// SignIn signs in through ihasmail and straight back out. It is the proof the
// two are linked: ihasmail can only accept the credentials by presenting them
// to Stalwart over STALWART_URL and getting a JMAP session back.
//
// It returns whether the session carries Stalwart's own capability, which is
// what ihasmail keys its administration features on.
func SignIn(ctx context.Context, baseURL, username, password string) (stalwartCapability bool, err error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}

	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	res, err := post(ctx, client, baseURL+"/api/auth/login", body)
	if err != nil {
		return false, err
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return false, fmt.Errorf("sign-in as %s answered HTTP %d: %s", username, res.StatusCode, strings.TrimSpace(string(raw)))
	}
	var session struct {
		Accounts map[string]struct {
			AccountCapabilities map[string]json.RawMessage `json:"accountCapabilities"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &session); err != nil || len(session.Accounts) == 0 {
		return false, fmt.Errorf("sign-in as %s did not return a JMAP session", username)
	}
	for _, a := range session.Accounts {
		if _, ok := a.AccountCapabilities["urn:stalwart:jmap"]; ok {
			stalwartCapability = true
		}
	}

	// Not leaving a session behind matters little with sessions in memory, but
	// it costs one request.
	if res, err := post(ctx, client, baseURL+"/api/auth/logout", []byte("{}")); err == nil {
		res.Body.Close()
	}
	return stalwartCapability, nil
}

func post(ctx context.Context, client *http.Client, url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// ihasmail's CSRF check for a request that is not same-origin by
	// Sec-Fetch-Site.
	req.Header.Set("X-Requested-With", "ihasmail")
	return client.Do(req)
}
