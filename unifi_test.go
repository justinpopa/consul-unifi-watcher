package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// newTestUnifiClient creates an httptest server with the given handler and
// returns a UnifiClient configured with API key auth (skipping the login flow).
func newTestUnifiClient(t *testing.T, handler http.Handler) (*UnifiClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("creating cookie jar: %v", err)
	}

	uc := &UnifiClient{
		client:     &http.Client{Jar: jar},
		baseURL:    srv.URL,
		pathPrefix: "/proxy/network",
		site:       "default",
		apiKey:     "test-api-key",
		log:        slog.Default(),
	}
	return uc, srv
}

// --- Constructor + Login tests ---

func TestNewUnifiClient(t *testing.T) {
	cfg := &Config{
		UnifiHost:   "https://unifi.local/",
		UnifiSite:   "default",
		UnifiAPIKey: "my-key",
	}
	uc, err := NewUnifiClient(cfg, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uc.baseURL != "https://unifi.local" {
		t.Errorf("baseURL = %q, want trailing slash trimmed", uc.baseURL)
	}
	if uc.site != "default" {
		t.Errorf("site = %q, want %q", uc.site, "default")
	}
	if uc.apiKey != "my-key" {
		t.Errorf("apiKey = %q, want %q", uc.apiKey, "my-key")
	}
	if uc.client == nil {
		t.Error("client should not be nil")
	}
	if uc.client.Jar == nil {
		t.Error("client should have a cookie jar")
	}
}

func TestLogin_APIKeyNoop(t *testing.T) {
	uc := &UnifiClient{apiKey: "some-key", log: slog.Default()}
	if err := uc.Login(context.Background()); err != nil {
		t.Fatalf("Login with apiKey should return nil, got: %v", err)
	}
}

func TestLogin_UserPass(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Csrf-Token", "tok-123")
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	uc := &UnifiClient{
		client:  &http.Client{Jar: jar},
		baseURL: srv.URL,
		user:    "admin",
		pass:    "secret",
		log:     slog.Default(),
	}
	if err := uc.Login(context.Background()); err != nil {
		t.Fatalf("Login should succeed: %v", err)
	}
	if uc.csrfToken != "tok-123" {
		t.Errorf("csrfToken = %q, want %q", uc.csrfToken, "tok-123")
	}
}

func TestLogin_UserPassFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	uc := &UnifiClient{
		client:  &http.Client{Jar: jar},
		baseURL: srv.URL,
		user:    "admin",
		pass:    "wrong",
		log:     slog.Default(),
	}
	if err := uc.Login(context.Background()); err == nil {
		t.Fatal("Login should fail with bad credentials")
	}
}

// --- ListRecords tests ---

func TestListRecords_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]DNSRecord{
			{ID: "r1", Key: "web.home.jpopa.com", RecordType: "A", Value: "10.0.0.1", Enabled: true},
			{ID: "r2", Key: "api.home.jpopa.com", RecordType: "A", Value: "10.0.0.2", Enabled: true},
		})
	})

	uc, _ := newTestUnifiClient(t, mux)
	records, err := uc.ListRecords(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].ID != "r1" || records[0].Key != "web.home.jpopa.com" {
		t.Errorf("record[0] = %+v, unexpected", records[0])
	}
	if records[1].ID != "r2" || records[1].Key != "api.home.jpopa.com" {
		t.Errorf("record[1] = %+v, unexpected", records[1])
	}
}

func TestListRecords_Empty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]DNSRecord{})
	})

	uc, _ := newTestUnifiClient(t, mux)
	records, err := uc.ListRecords(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestListRecords_ServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	})

	uc, _ := newTestUnifiClient(t, mux)
	_, err := uc.ListRecords(context.Background())
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}

// --- CreateRecord tests ---

func TestCreateRecord_Success(t *testing.T) {
	var gotBody DNSRecord
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
	})

	uc, _ := newTestUnifiClient(t, mux)
	err := uc.CreateRecord(context.Background(), "new.home.jpopa.com", "A", "10.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotBody.Key != "new.home.jpopa.com" {
		t.Errorf("Key = %q, want %q", gotBody.Key, "new.home.jpopa.com")
	}
	if gotBody.RecordType != "A" {
		t.Errorf("RecordType = %q, want %q", gotBody.RecordType, "A")
	}
	if gotBody.Value != "10.0.0.1" {
		t.Errorf("Value = %q, want %q", gotBody.Value, "10.0.0.1")
	}
	if !gotBody.Enabled {
		t.Error("Enabled = false, want true")
	}
}

func TestCreateRecord_TXT(t *testing.T) {
	var gotBody DNSRecord
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
	})

	uc, _ := newTestUnifiClient(t, mux)
	err := uc.CreateRecord(context.Background(), "_managed.web.home.jpopa.com", "TXT", "consul-dns-watcher")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotBody.Key != "_managed.web.home.jpopa.com" {
		t.Errorf("Key = %q, want %q", gotBody.Key, "_managed.web.home.jpopa.com")
	}
	if gotBody.RecordType != "TXT" {
		t.Errorf("RecordType = %q, want %q", gotBody.RecordType, "TXT")
	}
	if gotBody.Value != "consul-dns-watcher" {
		t.Errorf("Value = %q, want %q", gotBody.Value, "consul-dns-watcher")
	}
	if !gotBody.Enabled {
		t.Error("Enabled = false, want true")
	}
}

func TestCreateRecord_201Created(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	uc, _ := newTestUnifiClient(t, mux)
	err := uc.CreateRecord(context.Background(), "new.home.jpopa.com", "A", "10.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateRecord_ServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fail", http.StatusInternalServerError)
	})

	uc, _ := newTestUnifiClient(t, mux)
	err := uc.CreateRecord(context.Background(), "new.home.jpopa.com", "A", "10.0.0.1")
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}

// --- DeleteRecord tests ---

func TestDeleteRecord_Success(t *testing.T) {
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns/", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	uc, _ := newTestUnifiClient(t, mux)
	err := uc.DeleteRecord(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/abc123") {
		t.Errorf("path = %q, want suffix /abc123", gotPath)
	}
}

func TestDeleteRecord_NoContent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	uc, _ := newTestUnifiClient(t, mux)
	err := uc.DeleteRecord(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteRecord_ServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fail", http.StatusInternalServerError)
	})

	uc, _ := newTestUnifiClient(t, mux)
	err := uc.DeleteRecord(context.Background(), "abc123")
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}

// --- Auth tests ---

func TestAuth_APIKeyHeader(t *testing.T) {
	var gotHeader string
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns", func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Api-Key")
		json.NewEncoder(w).Encode([]DNSRecord{})
	})

	uc, _ := newTestUnifiClient(t, mux)
	_, err := uc.ListRecords(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHeader != "test-api-key" {
		t.Errorf("X-Api-Key = %q, want %q", gotHeader, "test-api-key")
	}
}

func TestAuth_LoginFlow(t *testing.T) {
	var gotCSRF string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Csrf-Token", "csrf-token-abc")
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns", func(w http.ResponseWriter, r *http.Request) {
		gotCSRF = r.Header.Get("X-Csrf-Token")
		json.NewEncoder(w).Encode([]DNSRecord{})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	uc := &UnifiClient{
		client:     &http.Client{Jar: jar},
		baseURL:    srv.URL,
		pathPrefix: "/proxy/network",
		site:       "default",
		user:       "admin",
		pass:       "password",
		log:        slog.Default(),
	}

	// Perform login
	err := uc.login(context.Background())
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if uc.csrfToken != "csrf-token-abc" {
		t.Fatalf("csrfToken = %q, want %q", uc.csrfToken, "csrf-token-abc")
	}

	// Subsequent request should carry CSRF token
	_, err = uc.ListRecords(context.Background())
	if err != nil {
		t.Fatalf("ListRecords failed: %v", err)
	}
	if gotCSRF != "csrf-token-abc" {
		t.Errorf("X-Csrf-Token = %q, want %q", gotCSRF, "csrf-token-abc")
	}
}

func TestAuth_401Retry(t *testing.T) {
	var callCount atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Csrf-Token", "new-csrf")
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns", func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode([]DNSRecord{})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	uc := &UnifiClient{
		client:     &http.Client{Jar: jar},
		baseURL:    srv.URL,
		pathPrefix: "/proxy/network",
		site:       "default",
		user:       "admin",
		pass:       "password",
		log:        slog.Default(),
	}

	records, err := uc.ListRecords(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
	if callCount.Load() != 2 {
		t.Errorf("expected 2 calls to DNS endpoint, got %d", callCount.Load())
	}
}

func TestAuth_401RetryWithBody(t *testing.T) {
	var callCount atomic.Int32
	var retryBody DNSRecord
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Csrf-Token", "new-csrf")
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns", func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n == 1 {
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// On retry, the body should be replayed in full
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &retryBody)
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	uc := &UnifiClient{
		client:     &http.Client{Jar: jar},
		baseURL:    srv.URL,
		pathPrefix: "/proxy/network",
		site:       "default",
		user:       "admin",
		pass:       "password",
		log:        slog.Default(),
	}

	err := uc.CreateRecord(context.Background(), "web.home.jpopa.com", "A", "10.0.0.1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount.Load() != 2 {
		t.Errorf("expected 2 calls to DNS endpoint, got %d", callCount.Load())
	}
	if retryBody.Key != "web.home.jpopa.com" {
		t.Errorf("retry body Key = %q, want %q", retryBody.Key, "web.home.jpopa.com")
	}
	if retryBody.Value != "10.0.0.1" {
		t.Errorf("retry body Value = %q, want %q", retryBody.Value, "10.0.0.1")
	}
}

func TestAuth_401RetryLoginFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	uc := &UnifiClient{
		client:     &http.Client{Jar: jar},
		baseURL:    srv.URL,
		pathPrefix: "/proxy/network",
		site:       "default",
		user:       "admin",
		pass:       "password",
		log:        slog.Default(),
	}

	_, err := uc.ListRecords(context.Background())
	if err == nil {
		t.Fatal("expected error when re-login fails after 401")
	}
}

func TestLogin_ConnectionError(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	uc := &UnifiClient{
		client:  &http.Client{Jar: jar},
		baseURL: "http://127.0.0.1:1",
		user:    "admin",
		pass:    "pass",
		log:     slog.Default(),
	}
	err := uc.Login(context.Background())
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func TestListRecords_InvalidJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	})

	uc, _ := newTestUnifiClient(t, mux)
	_, err := uc.ListRecords(context.Background())
	if err == nil {
		t.Fatal("expected decode error for invalid JSON")
	}
}

func TestListRecords_ConnectionError(t *testing.T) {
	uc := &UnifiClient{
		client:     &http.Client{},
		baseURL:    "http://127.0.0.1:1",
		pathPrefix: "/proxy/network",
		site:       "default",
		apiKey:     "key",
		log:        slog.Default(),
	}
	_, err := uc.ListRecords(context.Background())
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func TestCreateRecord_ConnectionError(t *testing.T) {
	uc := &UnifiClient{
		client:     &http.Client{},
		baseURL:    "http://127.0.0.1:1",
		pathPrefix: "/proxy/network",
		site:       "default",
		apiKey:     "key",
		log:        slog.Default(),
	}
	err := uc.CreateRecord(context.Background(), "test.com", "A", "1.2.3.4")
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func TestDeleteRecord_ConnectionError(t *testing.T) {
	uc := &UnifiClient{
		client:     &http.Client{},
		baseURL:    "http://127.0.0.1:1",
		pathPrefix: "/proxy/network",
		site:       "default",
		apiKey:     "key",
		log:        slog.Default(),
	}
	err := uc.DeleteRecord(context.Background(), "abc123")
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func TestAuth_CSRFTokenUpdated(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy/network/v2/api/site/default/static-dns", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Csrf-Token", "updated-csrf-token")
		json.NewEncoder(w).Encode([]DNSRecord{})
	})

	uc, _ := newTestUnifiClient(t, mux)
	uc.csrfToken = "old-csrf"

	_, err := uc.ListRecords(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uc.csrfToken != "updated-csrf-token" {
		t.Errorf("csrfToken = %q, want %q", uc.csrfToken, "updated-csrf-token")
	}
}
