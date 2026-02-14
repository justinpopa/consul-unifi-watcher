package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

const (
	maxErrorBodyBytes = 4096
)

type DNSRecord struct {
	ID         string `json:"_id,omitempty"`
	Key        string `json:"key"`
	RecordType string `json:"record_type"`
	Value      string `json:"value"`
	Enabled    bool   `json:"enabled"`
}

type UnifiClient struct {
	client     *http.Client
	baseURL    string
	pathPrefix string
	site       string
	apiKey     string
	user       string
	pass       string
	csrfToken  string
	log        *slog.Logger
}

func NewUnifiClient(cfg *Config, log *slog.Logger) (*UnifiClient, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("creating cookie jar: %w", err)
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.UnifiSkipTLS,
		},
	}

	uc := &UnifiClient{
		client: &http.Client{
			Jar:       jar,
			Transport: transport,
			Timeout:   30 * time.Second,
		},
		baseURL:    strings.TrimRight(cfg.UnifiHost, "/"),
		pathPrefix: "/proxy/network",
		site:       cfg.UnifiSite,
		apiKey:     cfg.UnifiAPIKey,
		user:       cfg.UnifiUser,
		pass:       cfg.UnifiPass,
		log:        log,
	}

	return uc, nil
}

// Login performs the initial authentication if using user/pass auth.
// Must be called before making API requests when not using API key auth.
func (u *UnifiClient) Login(ctx context.Context) error {
	if u.apiKey != "" {
		return nil
	}
	return u.login(ctx)
}

func (u *UnifiClient) dnsURL() string {
	return fmt.Sprintf("%s%s/v2/api/site/%s/static-dns", u.baseURL, u.pathPrefix, u.site)
}

func (u *UnifiClient) login(ctx context.Context) error {
	payload, err := json.Marshal(map[string]any{
		"username": u.user,
		"password": u.pass,
		"remember": true,
	})
	if err != nil {
		return fmt.Errorf("marshaling login payload: %w", err)
	}

	loginURL := fmt.Sprintf("%s/api/auth/login", u.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return fmt.Errorf("login returned status %d: %s", resp.StatusCode, body)
	}
	io.Copy(io.Discard, resp.Body)

	if token := resp.Header.Get("X-Csrf-Token"); token != "" {
		u.csrfToken = token
	}

	u.log.Info("authenticated to unifi controller")
	return nil
}

func (u *UnifiClient) doRequest(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	resp, err := u.executeRequest(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized && u.apiKey == "" {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		u.log.Info("received 401, re-authenticating")
		if err := u.login(ctx); err != nil {
			return nil, fmt.Errorf("re-login: %w", err)
		}
		return u.executeRequest(ctx, method, url, body)
	}

	return resp, nil
}

func (u *UnifiClient) executeRequest(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if u.apiKey != "" {
		req.Header.Set("X-Api-Key", u.apiKey)
	} else if u.csrfToken != "" {
		req.Header.Set("X-Csrf-Token", u.csrfToken)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}

	if token := resp.Header.Get("X-Csrf-Token"); token != "" {
		u.csrfToken = token
	}

	return resp, nil
}

func (u *UnifiClient) ListRecords(ctx context.Context) ([]DNSRecord, error) {
	resp, err := u.doRequest(ctx, http.MethodGet, u.dnsURL(), nil)
	if err != nil {
		return nil, fmt.Errorf("listing DNS records: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return nil, fmt.Errorf("list records returned status %d: %s", resp.StatusCode, body)
	}

	var records []DNSRecord
	if err := json.NewDecoder(resp.Body).Decode(&records); err != nil {
		return nil, fmt.Errorf("decoding DNS records: %w", err)
	}
	return records, nil
}

func (u *UnifiClient) CreateRecord(ctx context.Context, fqdn, ip string) error {
	record := DNSRecord{
		Key:        fqdn,
		RecordType: "A",
		Value:      ip,
		Enabled:    true,
	}

	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshaling record: %w", err)
	}

	resp, err := u.doRequest(ctx, http.MethodPost, u.dnsURL(), payload)
	if err != nil {
		return fmt.Errorf("creating DNS record: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return fmt.Errorf("create record returned status %d: %s", resp.StatusCode, body)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

func (u *UnifiClient) DeleteRecord(ctx context.Context, id string) error {
	url := fmt.Sprintf("%s/%s", u.dnsURL(), url.PathEscape(id))
	resp, err := u.doRequest(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("deleting DNS record: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return fmt.Errorf("delete record returned status %d: %s", resp.StatusCode, body)
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}
