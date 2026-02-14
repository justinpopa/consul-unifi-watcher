package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hashicorp/consul/api"
)

type DesiredRecord struct {
	FQDN        string
	ServiceName string
}

type ConsulSource struct {
	client         *api.Client
	tag            string
	zone           string
	traefikService string
	log            *slog.Logger
}

func NewConsulSource(cfg *Config, log *slog.Logger) (*ConsulSource, error) {
	client, err := api.NewClient(&api.Config{
		Address: cfg.ConsulAddr,
	})
	if err != nil {
		return nil, fmt.Errorf("creating consul client: %w", err)
	}

	zone := cfg.DNSZone
	if !strings.HasSuffix(zone, ".") {
		zone += "."
	}

	return &ConsulSource{
		client:         client,
		tag:            cfg.ConsulTag,
		zone:           zone,
		traefikService: cfg.TraefikService,
		log:            log,
	}, nil
}

func (cs *ConsulSource) DesiredRecords(ctx context.Context) ([]DesiredRecord, error) {
	opts := (&api.QueryOptions{}).WithContext(ctx)
	services, _, err := cs.client.Catalog().Services(opts)
	if err != nil {
		return nil, fmt.Errorf("listing consul services: %w", err)
	}

	seen := make(map[string]bool)
	var records []DesiredRecord

	for name, tags := range services {
		if !hasTag(tags, cs.tag) {
			continue
		}

		entries, _, err := cs.client.Catalog().Service(name, cs.tag, opts)
		if err != nil {
			cs.log.Warn("failed to query consul service", "service", name, "error", err)
			continue
		}

		if len(entries) == 0 {
			continue
		}

		fqdn := extractFQDN(entries[0])
		if fqdn == "" {
			cs.log.Warn("service tagged for DNS but missing dns-name", "service", name)
			continue
		}

		zoneSuffix := cs.zone
		if !strings.HasSuffix(fqdn, ".") {
			fqdn += "."
		}
		if !strings.HasSuffix(fqdn, zoneSuffix) {
			cs.log.Warn("FQDN not in managed zone, skipping",
				"service", name, "fqdn", fqdn, "zone", zoneSuffix)
			continue
		}

		// Store without trailing dot for Unifi
		fqdn = strings.TrimSuffix(fqdn, ".")

		if seen[fqdn] {
			continue
		}
		seen[fqdn] = true

		records = append(records, DesiredRecord{
			FQDN:        fqdn,
			ServiceName: name,
		})
	}

	return records, nil
}

func extractFQDN(entry *api.CatalogService) string {
	// Check ServiceMeta first
	if v, ok := entry.ServiceMeta["dns-name"]; ok && v != "" {
		return v
	}

	// Fall back to tags: dns-name=foo.example.com
	for _, tag := range entry.ServiceTags {
		if v, ok := strings.CutPrefix(tag, "dns-name="); ok {
			return v
		}
	}

	return ""
}

func hasTag(tags []string, target string) bool {
	for _, t := range tags {
		if t == target {
			return true
		}
	}
	return false
}

// TraefikIPs returns the addresses of all healthy instances of the
// configured Traefik service in Consul.
func (cs *ConsulSource) TraefikIPs(ctx context.Context) ([]string, error) {
	opts := (&api.QueryOptions{}).WithContext(ctx)
	entries, _, err := cs.client.Health().Service(cs.traefikService, "", true, opts)
	if err != nil {
		return nil, fmt.Errorf("querying traefik service %q: %w", cs.traefikService, err)
	}

	seen := make(map[string]bool)
	var ips []string
	for _, entry := range entries {
		ip := entry.Service.Address
		if ip == "" {
			ip = entry.Node.Address
		}
		if !seen[ip] {
			seen[ip] = true
			ips = append(ips, ip)
		}
	}
	return ips, nil
}
