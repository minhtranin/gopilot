package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Same client id + token file path the JS copilot-api uses, so a device
// login already done for that tool works here with zero re-auth.
const githubClientID = "Iv1.b507a08c87ecfe98"

func githubTokenPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "copilot-api", "github_token")
}

func loadGitHubToken() (string, error) {
	b, err := os.ReadFile(githubTokenPath())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func saveGitHubToken(tok string) error {
	p := githubTokenPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(tok), 0o600)
}

type deviceCodeResp struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int    `json:"interval"`
}

func deviceLogin() (string, error) {
	form := url.Values{"client_id": {githubClientID}, "scope": {"read:user"}}
	req, _ := http.NewRequest("POST", "https://github.com/login/device/code", strings.NewReader(form.Encode()))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var dc deviceCodeResp
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		return "", err
	}
	fmt.Printf("Open %s and enter code: %s\n", dc.VerificationURI, dc.UserCode)

	interval := time.Duration(dc.Interval+1) * time.Second
	for {
		time.Sleep(interval)
		form := url.Values{
			"client_id":   {githubClientID},
			"device_code": {dc.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}
		req, _ := http.NewRequest("POST", "https://github.com/login/oauth/access_token", strings.NewReader(form.Encode()))
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}
		var body struct {
			AccessToken string `json:"access_token"`
		}
		json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if body.AccessToken != "" {
			return body.AccessToken, nil
		}
	}
}

func ensureGitHubToken() (string, error) {
	if tok, err := loadGitHubToken(); err == nil && tok != "" {
		return tok, nil
	}
	tok, err := deviceLogin()
	if err != nil {
		return "", err
	}
	if err := saveGitHubToken(tok); err != nil {
		return "", err
	}
	return tok, nil
}

type copilotTokenResp struct {
	Token     string `json:"token"`
	RefreshIn int    `json:"refresh_in"`
}

func fetchCopilotToken(githubToken string) (copilotTokenResp, error) {
	var out copilotTokenResp
	req, _ := http.NewRequest("GET", "https://api.github.com/copilot_internal/v2/token", nil)
	for k, v := range githubHeaders(githubToken) {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return out, fmt.Errorf("copilot token exchange: http %d", resp.StatusCode)
	}
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

// refreshLoop keeps state.copilotToken alive for the process's whole life —
// it expires in ~25-30min, mid-conversation expiry would 401 a live request.
func refreshLoop(st *State) error {
	tok, err := fetchCopilotToken(st.githubToken)
	if err != nil {
		return err
	}
	st.setCopilotToken(tok.Token)

	go func() {
		for {
			wait := time.Duration(tok.RefreshIn-60) * time.Second
			if wait <= 0 {
				wait = 60 * time.Second
			}
			time.Sleep(wait)
			t, err := fetchCopilotToken(st.githubToken)
			if err != nil {
				fmt.Println("[gopilot] copilot token refresh failed:", err)
				continue
			}
			st.setCopilotToken(t.Token)
			tok = t
		}
	}()
	return nil
}
