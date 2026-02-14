package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ConsulAddr     string
	ConsulTag      string
	UnifiHost      string
	UnifiUser      string
	UnifiPass      string
	UnifiAPIKey    string
	UnifiSite      string
	UnifiSkipTLS   bool
	TraefikService string
	DNSZone        string
	PollInterval   time.Duration
	DryRun         bool
	HealthAddr     string
}

func LoadConfig() (*Config, error) {
	c := &Config{
		ConsulAddr:   envOrDefault("CONSUL_HTTP_ADDR", "http://localhost:8500"),
		ConsulTag:    envOrDefault("CONSUL_SERVICE_TAG", "dns-register"),
		UnifiHost:    os.Getenv("UNIFI_HOST"),
		UnifiUser:    os.Getenv("UNIFI_USER"),
		UnifiPass:    os.Getenv("UNIFI_PASS"),
		UnifiAPIKey:    os.Getenv("UNIFI_API_KEY"),
		UnifiSite:      envOrDefault("UNIFI_SITE", "default"),
		TraefikService: envOrDefault("TRAEFIK_SERVICE", "traefik"),
		DNSZone:        os.Getenv("DNS_ZONE"),
		HealthAddr:   envOrDefault("HEALTH_ADDR", ":8080"),
	}

	skipTLS := envOrDefault("UNIFI_SKIP_TLS", "false")
	v, err := strconv.ParseBool(skipTLS)
	if err != nil {
		return nil, fmt.Errorf("invalid UNIFI_SKIP_TLS value %q: %w", skipTLS, err)
	}
	c.UnifiSkipTLS = v

	interval := envOrDefault("POLL_INTERVAL", "10s")
	c.PollInterval, err = time.ParseDuration(interval)
	if err != nil {
		return nil, fmt.Errorf("invalid POLL_INTERVAL value %q: %w", interval, err)
	}

	dryRun := envOrDefault("DRY_RUN", "false")
	c.DryRun, err = strconv.ParseBool(dryRun)
	if err != nil {
		return nil, fmt.Errorf("invalid DRY_RUN value %q: %w", dryRun, err)
	}

	if c.UnifiHost == "" {
		return nil, fmt.Errorf("UNIFI_HOST is required")
	}
	if c.DNSZone == "" {
		return nil, fmt.Errorf("DNS_ZONE is required")
	}
	if c.UnifiAPIKey == "" && (c.UnifiUser == "" || c.UnifiPass == "") {
		return nil, fmt.Errorf("either UNIFI_API_KEY or UNIFI_USER+UNIFI_PASS is required")
	}
	if strings.ContainsAny(c.UnifiSite, "/.\\") {
		return nil, fmt.Errorf("invalid UNIFI_SITE value %q: must not contain path separators", c.UnifiSite)
	}

	return c, nil
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
