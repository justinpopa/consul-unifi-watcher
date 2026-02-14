package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

type dnsSource interface {
	DesiredRecords(ctx context.Context) ([]DesiredRecord, error)
}

type nodeIPSource interface {
	TraefikIPs(ctx context.Context) ([]string, error)
}

type dnsManager interface {
	ListRecords(ctx context.Context) ([]DNSRecord, error)
	CreateRecord(ctx context.Context, fqdn, ip string) error
	DeleteRecord(ctx context.Context, id string) error
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := run(ctx, log); err != nil {
		log.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	cfg, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	log.Info("starting consul-unifi-watcher",
		"consul_addr", cfg.ConsulAddr,
		"consul_tag", cfg.ConsulTag,
		"unifi_host", cfg.UnifiHost,
		"dns_zone", cfg.DNSZone,
		"traefik_service", cfg.TraefikService,
		"poll_interval", cfg.PollInterval,
		"dry_run", cfg.DryRun,
	)

	consul, err := NewConsulSource(cfg, log)
	if err != nil {
		return fmt.Errorf("creating consul source: %w", err)
	}

	unifi, err := NewUnifiClient(cfg, log)
	if err != nil {
		return fmt.Errorf("creating unifi client: %w", err)
	}

	if err := unifi.Login(ctx); err != nil {
		return fmt.Errorf("authenticating to unifi: %w", err)
	}

	// Health/readiness server
	var ready atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "not ready"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	})
	healthServer := &http.Server{
		Addr:              cfg.HealthAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("health server listening", "addr", cfg.HealthAddr)
		if err := healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("health server error", "error", err)
		}
	}()

	// Run immediately, then on ticker
	reconcileOnce(ctx, log, consul, consul, unifi, cfg, &ready)

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("shutting down")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			healthServer.Shutdown(shutdownCtx)
			return nil
		case <-ticker.C:
			reconcileOnce(ctx, log, consul, consul, unifi, cfg, &ready)
		}
	}
}

func reconcileOnce(ctx context.Context, log *slog.Logger, consul dnsSource, nodes nodeIPSource, unifi dnsManager, cfg *Config, ready *atomic.Bool) {
	desired, err := consul.DesiredRecords(ctx)
	if err != nil {
		log.Error("failed to fetch desired records from consul", "error", err)
		ready.Store(false)
		return
	}
	log.Info("fetched desired records from consul", "count", len(desired))

	nodeIPs, err := nodes.TraefikIPs(ctx)
	if err != nil {
		log.Error("failed to fetch traefik node IPs from consul", "error", err)
		ready.Store(false)
		return
	}
	if len(nodeIPs) == 0 {
		log.Warn("no healthy traefik instances found in consul")
		ready.Store(false)
		return
	}
	log.Info("fetched traefik node IPs", "ips", nodeIPs)

	existing, err := unifi.ListRecords(ctx)
	if err != nil {
		log.Error("failed to fetch existing records from unifi", "error", err)
		ready.Store(false)
		return
	}
	log.Info("fetched existing records from unifi", "count", len(existing))
	ready.Store(true)

	actions := Reconcile(desired, existing, nodeIPs)
	if len(actions) == 0 {
		log.Info("no changes needed")
		return
	}

	for _, action := range actions {
		switch action.Type {
		case ActionWarn:
			log.Warn("reconcile conflict", "fqdn", action.FQDN, "reason", action.Reason)

		case ActionCreate:
			log.Info("creating DNS record", "fqdn", action.FQDN, "ip", action.IP, "reason", action.Reason)
			if !cfg.DryRun {
				if err := unifi.CreateRecord(ctx, action.FQDN, action.IP); err != nil {
					log.Error("failed to create DNS record", "fqdn", action.FQDN, "error", err)
				}
			}

		case ActionDelete:
			log.Info("deleting DNS record", "fqdn", action.FQDN, "id", action.ID, "reason", action.Reason)
			if !cfg.DryRun {
				if err := unifi.DeleteRecord(ctx, action.ID); err != nil {
					log.Error("failed to delete DNS record", "fqdn", action.FQDN, "error", err)
				}
			}
		}
	}
}
