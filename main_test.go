package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- fakes ---

type fakeSource struct {
	records []DesiredRecord
	err     error
}

func (f *fakeSource) DesiredRecords(context.Context) ([]DesiredRecord, error) {
	return f.records, f.err
}

type fakeManager struct {
	records   []DNSRecord
	listErr   error
	createErr error
	deleteErr error

	created []createCall
	deleted []string
}

type createCall struct {
	key, recordType, value string
}

func (f *fakeManager) ListRecords(context.Context) ([]DNSRecord, error) {
	return f.records, f.listErr
}

func (f *fakeManager) CreateRecord(_ context.Context, key, recordType, value string) error {
	f.created = append(f.created, createCall{key, recordType, value})
	return f.createErr
}

func (f *fakeManager) DeleteRecord(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return f.deleteErr
}

type fakeNodeSource struct {
	ips []string
	err error
}

func (f *fakeNodeSource) TraefikIPs(context.Context) ([]string, error) {
	return f.ips, f.err
}

func testCfg(dryRun bool) *Config {
	return &Config{
		TraefikService: "traefik",
		DryRun:         dryRun,
	}
}

func defaultNodeSource() *fakeNodeSource {
	return &fakeNodeSource{ips: []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}}
}

// --- run() tests ---

func TestRun_BadConfig(t *testing.T) {
	t.Setenv("UNIFI_HOST", "")
	t.Setenv("TRAEFIK_SERVICE", "")
	t.Setenv("DNS_ZONE", "")
	t.Setenv("UNIFI_API_KEY", "")
	t.Setenv("UNIFI_USER", "")
	t.Setenv("UNIFI_PASS", "")

	err := run(context.Background(), slog.Default())
	if err == nil {
		t.Fatal("expected error for bad config")
	}
	if !strings.Contains(err.Error(), "loading config") {
		t.Errorf("error = %q, want it to mention config", err)
	}
}

func TestRun_LoginError(t *testing.T) {
	t.Setenv("CONSUL_HTTP_ADDR", "http://127.0.0.1:1")
	t.Setenv("UNIFI_HOST", "http://127.0.0.1:1")
	t.Setenv("UNIFI_API_KEY", "")
	t.Setenv("UNIFI_USER", "admin")
	t.Setenv("UNIFI_PASS", "pass")
	t.Setenv("TRAEFIK_SERVICE", "traefik")
	t.Setenv("DNS_ZONE", "home.test.com")
	t.Setenv("POLL_INTERVAL", "1h")
	t.Setenv("HEALTH_ADDR", "127.0.0.1:0")

	err := run(context.Background(), slog.Default())
	if err == nil {
		t.Fatal("expected login error")
	}
	if !strings.Contains(err.Error(), "authenticating") {
		t.Errorf("error = %q, want it to mention authenticating", err)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func TestRun_GracefulShutdown(t *testing.T) {
	t.Setenv("CONSUL_HTTP_ADDR", "http://127.0.0.1:1")
	t.Setenv("UNIFI_HOST", "https://127.0.0.1:1")
	t.Setenv("UNIFI_API_KEY", "test-key")
	t.Setenv("TRAEFIK_SERVICE", "traefik")
	t.Setenv("DNS_ZONE", "home.test.com")
	t.Setenv("POLL_INTERVAL", "1h")
	t.Setenv("HEALTH_ADDR", "127.0.0.1:0")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := run(ctx, slog.Default())
	if err != nil {
		t.Fatalf("expected clean shutdown, got: %v", err)
	}
}

func TestRun_HealthEndpointsAndTicker(t *testing.T) {
	addr := freeAddr(t)

	t.Setenv("CONSUL_HTTP_ADDR", "http://127.0.0.1:1")
	t.Setenv("UNIFI_HOST", "https://127.0.0.1:1")
	t.Setenv("UNIFI_API_KEY", "test-key")
	t.Setenv("TRAEFIK_SERVICE", "traefik")
	t.Setenv("DNS_ZONE", "home.test.com")
	t.Setenv("POLL_INTERVAL", "50ms")
	t.Setenv("HEALTH_ADDR", addr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, slog.Default())
	}()

	// Wait for health server to be ready
	base := fmt.Sprintf("http://%s", addr)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// /health should return 200
	resp, err := http.Get(base + "/health")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/health status = %d, want 200", resp.StatusCode)
	}
	if body["status"] != "ok" {
		t.Errorf("/health status = %q, want %q", body["status"], "ok")
	}

	// /ready should return 503 (consul is unreachable, so ready=false)
	resp, err = http.Get(base + "/ready")
	if err != nil {
		t.Fatalf("ready request failed: %v", err)
	}
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("/ready status = %d, want 503", resp.StatusCode)
	}
	if body["status"] != "not ready" {
		t.Errorf("/ready status = %q, want %q", body["status"], "not ready")
	}

	// Wait long enough for at least one ticker reconcile (50ms interval)
	time.Sleep(150 * time.Millisecond)

	cancel()

	if err := <-errCh; err != nil {
		t.Fatalf("expected clean shutdown, got: %v", err)
	}
}

func TestRun_ReadyAfterSuccessfulReconcile(t *testing.T) {
	// Stand up fake consul and unifi servers so reconcile succeeds
	consulMux := http.NewServeMux()
	consulMux.HandleFunc("/v1/catalog/services", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Consul-Index", "1")
		w.Header().Set("X-Consul-LastContact", "0")
		w.Header().Set("X-Consul-KnownLeader", "true")
		json.NewEncoder(w).Encode(map[string][]string{})
	})
	consulMux.HandleFunc("/v1/health/service/traefik", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Consul-Index", "1")
		w.Header().Set("X-Consul-LastContact", "0")
		w.Header().Set("X-Consul-KnownLeader", "true")
		json.NewEncoder(w).Encode([]map[string]any{
			{
				"Node":    map[string]any{"Node": "node1", "Address": "10.0.0.1"},
				"Service": map[string]any{"Service": "traefik", "Address": ""},
			},
		})
	})
	consulSrv := httptest.NewServer(consulMux)
	t.Cleanup(consulSrv.Close)

	unifiMux := http.NewServeMux()
	unifiMux.HandleFunc("/proxy/network/v2/api/site/default/static-dns", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]DNSRecord{})
	})
	unifiSrv := httptest.NewServer(unifiMux)
	t.Cleanup(unifiSrv.Close)

	healthAddr := freeAddr(t)

	t.Setenv("CONSUL_HTTP_ADDR", consulSrv.URL)
	t.Setenv("UNIFI_HOST", unifiSrv.URL)
	t.Setenv("UNIFI_API_KEY", "test-key")
	t.Setenv("TRAEFIK_SERVICE", "traefik")
	t.Setenv("DNS_ZONE", "home.test.com")
	t.Setenv("POLL_INTERVAL", "1h")
	t.Setenv("HEALTH_ADDR", healthAddr)
	t.Setenv("UNIFI_SKIP_TLS", "true")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, slog.Default())
	}()

	// Wait for health server
	base := fmt.Sprintf("http://%s", healthAddr)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Poll /ready — reconcileOnce may still be in flight
	var readyStatus int
	var readyBody map[string]string
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/ready")
		if err != nil {
			t.Fatalf("ready request failed: %v", err)
		}
		readyBody = nil
		json.NewDecoder(resp.Body).Decode(&readyBody)
		resp.Body.Close()
		readyStatus = resp.StatusCode
		if readyStatus == http.StatusOK {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if readyStatus != http.StatusOK {
		t.Errorf("/ready status = %d, want 200", readyStatus)
	}
	if readyBody["status"] != "ready" {
		t.Errorf("/ready body = %q, want %q", readyBody["status"], "ready")
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("expected clean shutdown, got: %v", err)
	}
}

// --- reconcileOnce tests ---

func TestReconcileOnce_HappyPath(t *testing.T) {
	src := &fakeSource{records: []DesiredRecord{
		{FQDN: "new.home.jpopa.com", ServiceName: "new"},
	}}
	mgr := &fakeManager{records: []DNSRecord{
		{ID: "txt-old", Key: "_managed.old.home.jpopa.com", RecordType: "TXT", Value: OwnerValue},
		{ID: "r-old", Key: "old.home.jpopa.com", RecordType: "A", Value: "10.0.0.1"},
	}}

	var ready atomic.Bool
	reconcileOnce(context.Background(), slog.Default(), src, defaultNodeSource(), mgr, testCfg(false), &ready)

	if !ready.Load() {
		t.Error("expected ready=true after successful reconcile")
	}
	// 4 creates (1 TXT + 3 A) for the new service
	if len(mgr.created) != 4 {
		t.Errorf("expected 4 creates, got %d: %+v", len(mgr.created), mgr.created)
	}
	// 2 deletes (1 A + 1 TXT) for the orphaned service
	if len(mgr.deleted) != 2 {
		t.Errorf("expected 2 deletes, got %d: %+v", len(mgr.deleted), mgr.deleted)
	}
}

func TestReconcileOnce_DryRun(t *testing.T) {
	src := &fakeSource{records: []DesiredRecord{
		{FQDN: "new.home.jpopa.com", ServiceName: "new"},
	}}
	mgr := &fakeManager{records: []DNSRecord{
		{ID: "txt-old", Key: "_managed.old.home.jpopa.com", RecordType: "TXT", Value: OwnerValue},
		{ID: "r-old", Key: "old.home.jpopa.com", RecordType: "A", Value: "10.0.0.1"},
	}}

	var ready atomic.Bool
	reconcileOnce(context.Background(), slog.Default(), src, defaultNodeSource(), mgr, testCfg(true), &ready)

	if len(mgr.created) != 0 {
		t.Errorf("dry-run should not create, got %+v", mgr.created)
	}
	if len(mgr.deleted) != 0 {
		t.Errorf("dry-run should not delete, got %+v", mgr.deleted)
	}
}

func TestReconcileOnce_ConsulError(t *testing.T) {
	src := &fakeSource{err: errors.New("consul down")}
	mgr := &fakeManager{}

	var ready atomic.Bool
	ready.Store(true)
	reconcileOnce(context.Background(), slog.Default(), src, defaultNodeSource(), mgr, testCfg(false), &ready)

	if ready.Load() {
		t.Error("expected ready=false after consul error")
	}
	if len(mgr.created) != 0 || len(mgr.deleted) != 0 {
		t.Error("no unifi calls expected when consul fails")
	}
}

func TestReconcileOnce_UnifiListError(t *testing.T) {
	src := &fakeSource{records: []DesiredRecord{{FQDN: "a.home.jpopa.com"}}}
	mgr := &fakeManager{listErr: errors.New("unifi down")}

	var ready atomic.Bool
	ready.Store(true)
	reconcileOnce(context.Background(), slog.Default(), src, defaultNodeSource(), mgr, testCfg(false), &ready)

	if ready.Load() {
		t.Error("expected ready=false after unifi list error")
	}
}

func TestReconcileOnce_CreateError(t *testing.T) {
	src := &fakeSource{records: []DesiredRecord{
		{FQDN: "a.home.jpopa.com", ServiceName: "a"},
		{FQDN: "b.home.jpopa.com", ServiceName: "b"},
	}}
	mgr := &fakeManager{createErr: errors.New("create fail")}

	var ready atomic.Bool
	reconcileOnce(context.Background(), slog.Default(), src, defaultNodeSource(), mgr, testCfg(false), &ready)

	// 2 FQDNs * (1 TXT + 3 A) = 8 create attempts even though they fail
	if len(mgr.created) != 8 {
		t.Errorf("expected 8 create attempts, got %d", len(mgr.created))
	}
}

func TestReconcileOnce_DeleteError(t *testing.T) {
	src := &fakeSource{records: nil}
	mgr := &fakeManager{
		records: []DNSRecord{
			{ID: "txt-a", Key: "_managed.a.home.jpopa.com", RecordType: "TXT", Value: OwnerValue},
			{ID: "r1", Key: "a.home.jpopa.com", RecordType: "A", Value: "10.0.0.1"},
			{ID: "txt-b", Key: "_managed.b.home.jpopa.com", RecordType: "TXT", Value: OwnerValue},
			{ID: "r2", Key: "b.home.jpopa.com", RecordType: "A", Value: "10.0.0.1"},
		},
		deleteErr: errors.New("delete fail"),
	}

	var ready atomic.Bool
	reconcileOnce(context.Background(), slog.Default(), src, defaultNodeSource(), mgr, testCfg(false), &ready)

	// 2 A deletes + 2 TXT deletes = 4 attempts even though they fail
	if len(mgr.deleted) != 4 {
		t.Errorf("expected 4 delete attempts, got %d", len(mgr.deleted))
	}
}

func TestReconcileOnce_NoChanges(t *testing.T) {
	src := &fakeSource{records: []DesiredRecord{
		{FQDN: "web.home.jpopa.com", ServiceName: "web"},
	}}
	mgr := &fakeManager{records: []DNSRecord{
		{ID: "txt-web", Key: "_managed.web.home.jpopa.com", RecordType: "TXT", Value: OwnerValue},
		{ID: "r1", Key: "web.home.jpopa.com", RecordType: "A", Value: "10.0.0.1"},
		{ID: "r2", Key: "web.home.jpopa.com", RecordType: "A", Value: "10.0.0.2"},
		{ID: "r3", Key: "web.home.jpopa.com", RecordType: "A", Value: "10.0.0.3"},
	}}

	var ready atomic.Bool
	reconcileOnce(context.Background(), slog.Default(), src, defaultNodeSource(), mgr, testCfg(false), &ready)

	if !ready.Load() {
		t.Error("expected ready=true")
	}
	if len(mgr.created) != 0 {
		t.Errorf("no creates expected, got %+v", mgr.created)
	}
	if len(mgr.deleted) != 0 {
		t.Errorf("no deletes expected, got %+v", mgr.deleted)
	}
}

func TestReconcileOnce_WarnUnmanaged(t *testing.T) {
	src := &fakeSource{records: []DesiredRecord{
		{FQDN: "manual.home.jpopa.com", ServiceName: "svc"},
	}}
	mgr := &fakeManager{records: []DNSRecord{
		{ID: "r1", Key: "manual.home.jpopa.com", RecordType: "A", Value: "10.0.0.50"},
	}}

	var ready atomic.Bool
	reconcileOnce(context.Background(), slog.Default(), src, defaultNodeSource(), mgr, testCfg(false), &ready)

	// Warn action should be logged but no create/delete calls made
	if len(mgr.created) != 0 {
		t.Errorf("expected no creates for warn, got %+v", mgr.created)
	}
	if len(mgr.deleted) != 0 {
		t.Errorf("expected no deletes for warn, got %+v", mgr.deleted)
	}
}

func TestReconcileOnce_DryRunWithDeletes(t *testing.T) {
	src := &fakeSource{records: nil}
	mgr := &fakeManager{records: []DNSRecord{
		{ID: "txt-old", Key: "_managed.old.home.jpopa.com", RecordType: "TXT", Value: OwnerValue},
		{ID: "r1", Key: "old.home.jpopa.com", RecordType: "A", Value: "10.0.0.1"},
	}}

	var ready atomic.Bool
	reconcileOnce(context.Background(), slog.Default(), src, defaultNodeSource(), mgr, testCfg(true), &ready)

	if len(mgr.deleted) != 0 {
		t.Errorf("dry-run should not delete, got %+v", mgr.deleted)
	}
}

func TestReconcileOnce_ReadyFlag(t *testing.T) {
	// Starts false, becomes true after success
	src := &fakeSource{records: nil}
	mgr := &fakeManager{records: nil}

	var ready atomic.Bool
	if ready.Load() {
		t.Fatal("ready should start false")
	}

	reconcileOnce(context.Background(), slog.Default(), src, defaultNodeSource(), mgr, testCfg(false), &ready)
	if !ready.Load() {
		t.Error("expected ready=true after successful reconcile")
	}

	// Now fail — should flip back to false
	src.err = errors.New("fail")
	reconcileOnce(context.Background(), slog.Default(), src, defaultNodeSource(), mgr, testCfg(false), &ready)
	if ready.Load() {
		t.Error("expected ready=false after failed reconcile")
	}
}
