package main

import (
	"flag"
	"log"
	"time"

	"github.com/wraplink/dns-control/internal/agentregistry"
	"github.com/wraplink/dns-control/internal/allocation"
	"github.com/wraplink/dns-control/internal/api"
	"github.com/wraplink/dns-control/internal/config"
	"github.com/wraplink/dns-control/internal/dns"
	"github.com/wraplink/dns-control/internal/domain"
)

func main() {
	configFile := flag.String("config", "config.yaml", "path to configuration file")
	flag.Parse()

	cfg, err := config.Load(*configFile)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	log.Printf("dns-control starting")
	log.Printf("dns: listen %s upstream=%v ttl=%d", cfg.DNS.Listen, cfg.DNS.Upstream, cfg.DNS.TTL)
	log.Printf("api: listen %s", cfg.API.Listen)

	domainStore, err := domain.NewStore(cfg.Domains)
	if err != nil {
		log.Fatalf("create domain store: %v", err)
	}

	allocationStore := allocation.NewStore(cfg.Agent.LeaseDuration)
	agentStore := agentregistry.NewStore()

	for _, rule := range cfg.Domains {
		switch rule.Mode {
		case domain.ModeHijack:
			allocationStore.EnsurePending(rule.Domain, allocation.TypeSNI)
		case domain.ModeRoute:
			allocationStore.EnsurePending(rule.Domain, allocation.TypeRoute)
		}
	}

	resolver := dns.NewResolver(cfg.DNS.Upstream)
	dnsHandler := dns.NewHandler(resolver, domainStore, allocationStore, cfg.DNS.TTL)
	dnsServer := dns.NewServer(cfg.DNS.Listen, dnsHandler)

	apiHandler := api.NewHandler(domainStore, allocationStore, agentStore, cfg.Agent.Token)
	apiServer := api.NewServer(cfg.API.Listen, apiHandler)

	go runReconciler(allocationStore, agentStore, cfg.Agent.OfflineAfter, cfg.Agent.ReconcileInterval)

	errCh := make(chan error, 2)
	go func() { errCh <- dnsServer.Start() }()
	go func() { errCh <- apiServer.Start() }()

	if err := <-errCh; err != nil {
		log.Fatal(err)
	}
}

func runReconciler(
	allocations *allocation.Store,
	agents *agentregistry.Store,
	offlineAfter time.Duration,
	interval time.Duration,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	reconcile := func() {
		requeued := allocations.ReconcileExpired()
		offline := agents.MarkOfflineBefore(time.Now().UTC().Add(-offlineAfter))
		if requeued > 0 || offline > 0 {
			log.Printf("control-plane: reconciled expired_leases=%d offline_agents=%d", requeued, offline)
		}
	}

	reconcile()
	for range ticker.C {
		reconcile()
	}
}
