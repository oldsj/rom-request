// Package romm asks RomM to scan a platform folder once a game lands in it, so the game shows up
// in RomM within a minute instead of at its next scheduled scan.
//
// It uses RomM's client tokens (5.4+): a token an admin makes in RomM (Profile → Client API
// tokens) with only the "tasks.run" scope, sent as a bearer token to POST /api/tasks/scan. RomM's
// own sign-in (OIDC only, say) is unchanged.
package romm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ErrBusy means RomM is already running or queueing a scan; ask again later.
var ErrBusy = errors.New("romm: a scan is already running")

// Client calls RomM's API.
type Client struct {
	base  *url.URL
	token string
	hc    *http.Client
}

// New makes a Client. baseURL has been validated by config.
func New(baseURL, token string, hc *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, errors.New("romm: invalid URL")
	}
	return &Client{base: u, token: token, hc: hc}, nil
}

type scanRequest struct {
	Type            string   `json:"type"`
	PlatformFolders []string `json:"platform_fs_slugs"`
}

// ScanPlatforms queues a quick scan (new files only) of the named library folders.
func (c *Client) ScanPlatforms(ctx context.Context, folders []string) error {
	body, err := json.Marshal(scanRequest{Type: "quick", PlatformFolders: folders})
	if err != nil {
		return fmt.Errorf("romm: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.JoinPath("api/tasks/scan").String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("romm: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("romm: scan: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	switch {
	case resp.StatusCode == http.StatusConflict:
		return ErrBusy
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("romm: scan: status %d", resp.StatusCode)
	}
	return nil
}
