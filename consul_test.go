package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/consul/api"
)

// consulHeaders sets the required Consul response headers that the client's
// parseQueryMeta expects. Without these, strconv.ParseUint("") fails.
func consulHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Consul-Index", "1")
	w.Header().Set("X-Consul-LastContact", "0")
	w.Header().Set("X-Consul-KnownLeader", "true")
}

// newTestConsulSource creates an httptest server with the given handler,
// builds a real Consul API client pointed at it, and returns a ConsulSource.
func newTestConsulSource(t *testing.T, handler http.Handler) *ConsulSource {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := api.NewClient(&api.Config{Address: srv.URL})
	if err != nil {
		t.Fatalf("creating consul client: %v", err)
	}

	return &ConsulSource{
		client: client,
		tag:    "dns-register",
		zone:   "home.jpopa.com.",
		log:    slog.Default(),
	}
}

func TestDesiredRecords_Basic(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/catalog/services", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode(map[string][]string{
			"web": {"dns-register"},
		})
	})
	mux.HandleFunc("/v1/catalog/service/web", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode([]api.CatalogService{{
			ServiceName: "web",
			ServiceMeta: map[string]string{"dns-name": "web.home.jpopa.com"},
			ServiceTags: []string{"dns-register"},
		}})
	})

	cs := newTestConsulSource(t, mux)
	records, err := cs.DesiredRecords(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].FQDN != "web.home.jpopa.com" {
		t.Errorf("FQDN = %q, want %q", records[0].FQDN, "web.home.jpopa.com")
	}
	if records[0].ServiceName != "web" {
		t.Errorf("ServiceName = %q, want %q", records[0].ServiceName, "web")
	}
}

func TestDesiredRecords_TagFallback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/catalog/services", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode(map[string][]string{
			"api": {"dns-register"},
		})
	})
	mux.HandleFunc("/v1/catalog/service/api", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode([]api.CatalogService{{
			ServiceName: "api",
			ServiceTags: []string{"dns-register", "dns-name=api.home.jpopa.com"},
		}})
	})

	cs := newTestConsulSource(t, mux)
	records, err := cs.DesiredRecords(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].FQDN != "api.home.jpopa.com" {
		t.Errorf("FQDN = %q, want %q", records[0].FQDN, "api.home.jpopa.com")
	}
}

func TestDesiredRecords_SkipsUntagged(t *testing.T) {
	serviceQueried := false
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/catalog/services", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode(map[string][]string{
			"untagged-svc": {"other-tag"},
		})
	})
	mux.HandleFunc("/v1/catalog/service/untagged-svc", func(w http.ResponseWriter, r *http.Request) {
		serviceQueried = true
		consulHeaders(w)
		json.NewEncoder(w).Encode([]api.CatalogService{})
	})

	cs := newTestConsulSource(t, mux)
	records, err := cs.DesiredRecords(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
	if serviceQueried {
		t.Error("untagged service should not have been queried")
	}
}

func TestDesiredRecords_SkipsMissingDNSName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/catalog/services", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode(map[string][]string{
			"nodns": {"dns-register"},
		})
	})
	mux.HandleFunc("/v1/catalog/service/nodns", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode([]api.CatalogService{{
			ServiceName: "nodns",
			ServiceTags: []string{"dns-register"},
		}})
	})

	cs := newTestConsulSource(t, mux)
	records, err := cs.DesiredRecords(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestDesiredRecords_EmptyEntriesSkipped(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/catalog/services", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode(map[string][]string{
			"ghost": {"dns-register"},
		})
	})
	mux.HandleFunc("/v1/catalog/service/ghost", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode([]api.CatalogService{})
	})

	cs := newTestConsulSource(t, mux)
	records, err := cs.DesiredRecords(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records for empty entries, got %d", len(records))
	}
}

func TestDesiredRecords_SkipsWrongZone(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/catalog/services", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode(map[string][]string{
			"external": {"dns-register"},
		})
	})
	mux.HandleFunc("/v1/catalog/service/external", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode([]api.CatalogService{{
			ServiceName: "external",
			ServiceMeta: map[string]string{"dns-name": "app.other-domain.com"},
			ServiceTags: []string{"dns-register"},
		}})
	})

	cs := newTestConsulSource(t, mux)
	records, err := cs.DesiredRecords(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestDesiredRecords_Deduplication(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/catalog/services", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode(map[string][]string{
			"web-a": {"dns-register"},
			"web-b": {"dns-register"},
		})
	})
	mux.HandleFunc("/v1/catalog/service/web-a", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode([]api.CatalogService{{
			ServiceName: "web-a",
			ServiceMeta: map[string]string{"dns-name": "web.home.jpopa.com"},
			ServiceTags: []string{"dns-register"},
		}})
	})
	mux.HandleFunc("/v1/catalog/service/web-b", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode([]api.CatalogService{{
			ServiceName: "web-b",
			ServiceMeta: map[string]string{"dns-name": "web.home.jpopa.com"},
			ServiceTags: []string{"dns-register"},
		}})
	})

	cs := newTestConsulSource(t, mux)
	records, err := cs.DesiredRecords(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 deduplicated record, got %d", len(records))
	}
}

func TestDesiredRecords_MultipleServices(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/catalog/services", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode(map[string][]string{
			"web": {"dns-register"},
			"api": {"dns-register"},
		})
	})
	mux.HandleFunc("/v1/catalog/service/web", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode([]api.CatalogService{{
			ServiceName: "web",
			ServiceMeta: map[string]string{"dns-name": "web.home.jpopa.com"},
			ServiceTags: []string{"dns-register"},
		}})
	})
	mux.HandleFunc("/v1/catalog/service/api", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode([]api.CatalogService{{
			ServiceName: "api",
			ServiceMeta: map[string]string{"dns-name": "api.home.jpopa.com"},
			ServiceTags: []string{"dns-register"},
		}})
	})

	cs := newTestConsulSource(t, mux)
	records, err := cs.DesiredRecords(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("expected 2 records, got %d", len(records))
	}
}

func TestDesiredRecords_EmptyServices(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/catalog/services", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode(map[string][]string{})
	})

	cs := newTestConsulSource(t, mux)
	records, err := cs.DesiredRecords(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestDesiredRecords_TrailingDotHandling(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/catalog/services", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode(map[string][]string{
			"web": {"dns-register"},
		})
	})
	mux.HandleFunc("/v1/catalog/service/web", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode([]api.CatalogService{{
			ServiceName: "web",
			ServiceMeta: map[string]string{"dns-name": "web.home.jpopa.com."},
			ServiceTags: []string{"dns-register"},
		}})
	})

	cs := newTestConsulSource(t, mux)
	records, err := cs.DesiredRecords(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	// Trailing dot should be stripped for Unifi storage
	if records[0].FQDN != "web.home.jpopa.com" {
		t.Errorf("FQDN = %q, want %q (trailing dot stripped)", records[0].FQDN, "web.home.jpopa.com")
	}
}

// --- Constructor test ---

func TestNewConsulSource(t *testing.T) {
	cfg := &Config{
		ConsulAddr: "http://127.0.0.1:8500",
		ConsulTag:  "dns-register",
		DNSZone:    "home.jpopa.com",
	}
	cs, err := NewConsulSource(cfg, slog.Default())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cs.tag != "dns-register" {
		t.Errorf("tag = %q, want %q", cs.tag, "dns-register")
	}
	if cs.zone != "home.jpopa.com." {
		t.Errorf("zone = %q, want %q (trailing dot)", cs.zone, "home.jpopa.com.")
	}
	if cs.client == nil {
		t.Error("client should not be nil")
	}
}

// --- Pure helper tests (no httptest) ---

func TestExtractFQDN_FromMeta(t *testing.T) {
	entry := &api.CatalogService{
		ServiceMeta: map[string]string{"dns-name": "web.home.jpopa.com"},
	}
	got := extractFQDN(entry)
	if got != "web.home.jpopa.com" {
		t.Errorf("extractFQDN = %q, want %q", got, "web.home.jpopa.com")
	}
}

func TestExtractFQDN_FromTag(t *testing.T) {
	entry := &api.CatalogService{
		ServiceTags: []string{"dns-name=api.home.jpopa.com"},
	}
	got := extractFQDN(entry)
	if got != "api.home.jpopa.com" {
		t.Errorf("extractFQDN = %q, want %q", got, "api.home.jpopa.com")
	}
}

func TestExtractFQDN_MetaTakesPrecedence(t *testing.T) {
	entry := &api.CatalogService{
		ServiceMeta: map[string]string{"dns-name": "from-meta.home.jpopa.com"},
		ServiceTags: []string{"dns-name=from-tag.home.jpopa.com"},
	}
	got := extractFQDN(entry)
	if got != "from-meta.home.jpopa.com" {
		t.Errorf("extractFQDN = %q, want %q (meta should take precedence)", got, "from-meta.home.jpopa.com")
	}
}

func TestExtractFQDN_NeitherPresent(t *testing.T) {
	entry := &api.CatalogService{
		ServiceTags: []string{"other-tag"},
	}
	got := extractFQDN(entry)
	if got != "" {
		t.Errorf("extractFQDN = %q, want empty string", got)
	}
}

func TestHasTag_Found(t *testing.T) {
	if !hasTag([]string{"a", "dns-register", "b"}, "dns-register") {
		t.Error("expected hasTag to return true")
	}
}

func TestHasTag_NotFound(t *testing.T) {
	if hasTag([]string{"a", "b"}, "dns-register") {
		t.Error("expected hasTag to return false")
	}
}

func TestHasTag_EmptySlice(t *testing.T) {
	if hasTag([]string{}, "dns-register") {
		t.Error("expected hasTag to return false for empty slice")
	}
}

func TestDesiredRecords_ServicesError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/catalog/services", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	cs := newTestConsulSource(t, mux)
	_, err := cs.DesiredRecords(context.Background())
	if err == nil {
		t.Fatal("expected error when catalog/services fails")
	}
}

func TestDesiredRecords_ServiceQueryFailsContinues(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/catalog/services", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode(map[string][]string{
			"broken": {"dns-register"},
			"good":   {"dns-register"},
		})
	})
	mux.HandleFunc("/v1/catalog/service/broken", func(w http.ResponseWriter, r *http.Request) {
		// Return invalid JSON to trigger a decode error in the consul client
		consulHeaders(w)
		w.Write([]byte("not json"))
	})
	mux.HandleFunc("/v1/catalog/service/good", func(w http.ResponseWriter, r *http.Request) {
		consulHeaders(w)
		json.NewEncoder(w).Encode([]api.CatalogService{{
			ServiceName: "good",
			ServiceMeta: map[string]string{"dns-name": "good.home.jpopa.com"},
			ServiceTags: []string{"dns-register"},
		}})
	})

	cs := newTestConsulSource(t, mux)
	records, err := cs.DesiredRecords(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "broken" should be skipped with a warning, "good" should succeed
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d: %+v", len(records), records)
	}
	if records[0].FQDN != "good.home.jpopa.com" {
		t.Errorf("FQDN = %q, want %q", records[0].FQDN, "good.home.jpopa.com")
	}
}
