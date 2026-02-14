package main

import (
	"testing"
	"time"
)

// setRequiredEnv sets the minimum required env vars for LoadConfig to succeed.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("UNIFI_HOST", "https://unifi.example.com")
	t.Setenv("TRAEFIK_SERVICE", "traefik")
	t.Setenv("UNIFI_API_KEY", "test-api-key")
	t.Setenv("DNS_ZONE", "home.jpopa.com")
}

func TestLoadConfig_Defaults(t *testing.T) {
	setRequiredEnv(t)

	// Clear optional vars to prevent host env interference
	t.Setenv("CONSUL_HTTP_ADDR", "")
	t.Setenv("CONSUL_SERVICE_TAG", "")
	t.Setenv("UNIFI_SITE", "")
	t.Setenv("HEALTH_ADDR", "")
	t.Setenv("POLL_INTERVAL", "")
	t.Setenv("UNIFI_SKIP_TLS", "")
	t.Setenv("DRY_RUN", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ConsulAddr != "http://localhost:8500" {
		t.Errorf("ConsulAddr = %q, want %q", cfg.ConsulAddr, "http://localhost:8500")
	}
	if cfg.ConsulTag != "dns-register" {
		t.Errorf("ConsulTag = %q, want %q", cfg.ConsulTag, "dns-register")
	}
	if cfg.UnifiSite != "default" {
		t.Errorf("UnifiSite = %q, want %q", cfg.UnifiSite, "default")
	}
	if cfg.DNSZone != "home.jpopa.com" {
		t.Errorf("DNSZone = %q, want %q", cfg.DNSZone, "home.jpopa.com") // set by setRequiredEnv
	}
	if cfg.HealthAddr != ":8080" {
		t.Errorf("HealthAddr = %q, want %q", cfg.HealthAddr, ":8080")
	}
	if cfg.PollInterval != 10*time.Second {
		t.Errorf("PollInterval = %v, want %v", cfg.PollInterval, 10*time.Second)
	}
	if cfg.UnifiSkipTLS != false {
		t.Errorf("UnifiSkipTLS = %v, want false", cfg.UnifiSkipTLS)
	}
	if cfg.DryRun != false {
		t.Errorf("DryRun = %v, want false", cfg.DryRun)
	}
}

func TestLoadConfig_AllOverrides(t *testing.T) {
	t.Setenv("CONSUL_HTTP_ADDR", "http://consul:9500")
	t.Setenv("CONSUL_SERVICE_TAG", "my-tag")
	t.Setenv("UNIFI_HOST", "https://unifi.local")
	t.Setenv("UNIFI_USER", "admin")
	t.Setenv("UNIFI_PASS", "secret")
	t.Setenv("UNIFI_API_KEY", "my-key")
	t.Setenv("UNIFI_SITE", "lab")
	t.Setenv("UNIFI_SKIP_TLS", "false")
	t.Setenv("TRAEFIK_SERVICE", "my-traefik")
	t.Setenv("DNS_ZONE", "lab.example.com")
	t.Setenv("HEALTH_ADDR", ":9090")
	t.Setenv("POLL_INTERVAL", "30s")
	t.Setenv("DRY_RUN", "true")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ConsulAddr != "http://consul:9500" {
		t.Errorf("ConsulAddr = %q, want %q", cfg.ConsulAddr, "http://consul:9500")
	}
	if cfg.ConsulTag != "my-tag" {
		t.Errorf("ConsulTag = %q, want %q", cfg.ConsulTag, "my-tag")
	}
	if cfg.UnifiHost != "https://unifi.local" {
		t.Errorf("UnifiHost = %q, want %q", cfg.UnifiHost, "https://unifi.local")
	}
	if cfg.UnifiUser != "admin" {
		t.Errorf("UnifiUser = %q, want %q", cfg.UnifiUser, "admin")
	}
	if cfg.UnifiPass != "secret" {
		t.Errorf("UnifiPass = %q, want %q", cfg.UnifiPass, "secret")
	}
	if cfg.UnifiAPIKey != "my-key" {
		t.Errorf("UnifiAPIKey = %q, want %q", cfg.UnifiAPIKey, "my-key")
	}
	if cfg.UnifiSite != "lab" {
		t.Errorf("UnifiSite = %q, want %q", cfg.UnifiSite, "lab")
	}
	if cfg.UnifiSkipTLS != false {
		t.Errorf("UnifiSkipTLS = %v, want false", cfg.UnifiSkipTLS)
	}
	if cfg.TraefikService != "my-traefik" {
		t.Errorf("TraefikService = %q, want %q", cfg.TraefikService, "my-traefik")
	}
	if cfg.DNSZone != "lab.example.com" {
		t.Errorf("DNSZone = %q, want %q", cfg.DNSZone, "lab.example.com")
	}
	if cfg.HealthAddr != ":9090" {
		t.Errorf("HealthAddr = %q, want %q", cfg.HealthAddr, ":9090")
	}
	if cfg.PollInterval != 30*time.Second {
		t.Errorf("PollInterval = %v, want %v", cfg.PollInterval, 30*time.Second)
	}
	if cfg.DryRun != true {
		t.Errorf("DryRun = %v, want true", cfg.DryRun)
	}
}

func TestLoadConfig_MissingDNSZone(t *testing.T) {
	t.Setenv("UNIFI_HOST", "https://unifi.example.com")
	t.Setenv("TRAEFIK_SERVICE", "traefik")
	t.Setenv("UNIFI_API_KEY", "key")
	t.Setenv("DNS_ZONE", "")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for missing DNS_ZONE")
	}
}

func TestLoadConfig_InvalidUnifiSite(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("UNIFI_SITE", "../../etc")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for UNIFI_SITE with path separators")
	}
}

func TestLoadConfig_MissingUnifiHost(t *testing.T) {
	t.Setenv("UNIFI_HOST", "")
	t.Setenv("TRAEFIK_SERVICE", "traefik")
	t.Setenv("UNIFI_API_KEY", "key")
	t.Setenv("DNS_ZONE", "example.com")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for missing UNIFI_HOST")
	}
}

func TestLoadConfig_MissingAuth(t *testing.T) {
	t.Setenv("UNIFI_HOST", "https://unifi.example.com")
	t.Setenv("TRAEFIK_SERVICE", "traefik")
	t.Setenv("DNS_ZONE", "example.com")
	t.Setenv("UNIFI_API_KEY", "")
	t.Setenv("UNIFI_USER", "")
	t.Setenv("UNIFI_PASS", "")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for missing auth credentials")
	}
}

func TestLoadConfig_UserWithoutPass(t *testing.T) {
	t.Setenv("UNIFI_HOST", "https://unifi.example.com")
	t.Setenv("TRAEFIK_SERVICE", "traefik")
	t.Setenv("DNS_ZONE", "example.com")
	t.Setenv("UNIFI_API_KEY", "")
	t.Setenv("UNIFI_USER", "admin")
	t.Setenv("UNIFI_PASS", "")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for user without pass")
	}
}

func TestLoadConfig_PassWithoutUser(t *testing.T) {
	t.Setenv("UNIFI_HOST", "https://unifi.example.com")
	t.Setenv("TRAEFIK_SERVICE", "traefik")
	t.Setenv("DNS_ZONE", "example.com")
	t.Setenv("UNIFI_API_KEY", "")
	t.Setenv("UNIFI_USER", "")
	t.Setenv("UNIFI_PASS", "secret")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for pass without user")
	}
}

func TestLoadConfig_UserPassAuth(t *testing.T) {
	t.Setenv("UNIFI_HOST", "https://unifi.example.com")
	t.Setenv("TRAEFIK_SERVICE", "traefik")
	t.Setenv("DNS_ZONE", "example.com")
	t.Setenv("UNIFI_API_KEY", "")
	t.Setenv("UNIFI_USER", "admin")
	t.Setenv("UNIFI_PASS", "password")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.UnifiUser != "admin" {
		t.Errorf("UnifiUser = %q, want %q", cfg.UnifiUser, "admin")
	}
	if cfg.UnifiPass != "password" {
		t.Errorf("UnifiPass = %q, want %q", cfg.UnifiPass, "password")
	}
}

func TestLoadConfig_InvalidPollInterval(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("POLL_INTERVAL", "garbage")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for invalid POLL_INTERVAL")
	}
}

func TestLoadConfig_InvalidSkipTLS(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("UNIFI_SKIP_TLS", "notabool")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for invalid UNIFI_SKIP_TLS")
	}
}

func TestLoadConfig_InvalidDryRun(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("DRY_RUN", "notabool")

	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error for invalid DRY_RUN")
	}
}
