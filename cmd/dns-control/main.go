package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/wraplink/dns-control/internal/agentregistry"
	"github.com/wraplink/dns-control/internal/allocation"
	"github.com/wraplink/dns-control/internal/api"
	"github.com/wraplink/dns-control/internal/backendregistry"
	"github.com/wraplink/dns-control/internal/config"
	"github.com/wraplink/dns-control/internal/dns"
	"github.com/wraplink/dns-control/internal/domain"
	"github.com/wraplink/dns-control/internal/entitlement"
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

	domainStore, err := domain.NewStore(nil)
	if err != nil {
		log.Fatalf("create domain store: %v", err)
	}

	allocationStore := allocation.NewStore(cfg.Agent.LeaseDuration)
	agentStore := agentregistry.NewStore()
	syncStatus := &api.DomainSyncStatus{Enabled: cfg.Backend.Enabled() && cfg.Node.Enabled()}

	var entitlementStore *entitlement.Store
	if cfg.Backend.Enabled() {
		entitlementStore = entitlement.NewStore(cfg.Backend.EntitlementMaxStale)
		backendClient := entitlement.NewClient(cfg.Backend.URL, cfg.Backend.ControlKey)

		refreshEntitlements(backendClient, entitlementStore)
		go runEntitlementSync(
			backendClient,
			entitlementStore,
			cfg.Backend.EntitlementRefreshInterval,
		)

		log.Printf(
			"backend entitlement sync enabled url=%s interval=%s max_stale=%s",
			cfg.Backend.URL,
			cfg.Backend.EntitlementRefreshInterval,
			cfg.Backend.EntitlementMaxStale,
		)

		if cfg.Node.Enabled() {
			registryClient := backendregistry.NewClient(
				cfg.Backend.URL,
				cfg.Backend.ControlKey,
			)
			registration := backendregistry.RegisterRequest{
				ID:        cfg.Node.ID,
				Region:    cfg.Node.Region,
				Addresses: append([]string(nil), cfg.Node.PublicDNS...),
			}
			registerDNSControl(registryClient, registration, domainStore, allocationStore, syncStatus)
			go runDNSControlRegistration(
				registryClient,
				registration,
				cfg.Node.RegisterInterval,
				domainStore,
				allocationStore,
				syncStatus,
			)
			log.Printf(
				"backend DNS registration enabled id=%s region=%s addresses=%v interval=%s",
				cfg.Node.ID,
				cfg.Node.Region,
				cfg.Node.PublicDNS,
				cfg.Node.RegisterInterval,
			)
		} else {
			log.Printf("backend DNS registration disabled; node.public_dns is empty")
		}
	} else {
		log.Printf("backend entitlement sync disabled; managed DNS policies are not client-gated")
	}

	resolver := dns.NewResolver(cfg.DNS.Upstream)
	if cfg.DNS.ParentURL != "" {
		resolver.SetParent(cfg.DNS.ParentURL, cfg.DNS.ParentToken)
	}
	dnsHandler := dns.NewHandler(
		resolver,
		domainStore,
		allocationStore,
		entitlementStore,
		cfg.DNS.TTL,
		agentStore,
	)
	dnsHandler.SetAllowLoopbackQueries(cfg.DNS.AllowLoopbackQueries)
	dnsServer := dns.NewServer(cfg.DNS.Listen, dnsHandler)

	var routeReporter *backendregistry.Client
	if cfg.Backend.Enabled() && cfg.Node.Enabled() {
		routeReporter = backendregistry.NewClient(cfg.Backend.URL, cfg.Backend.ControlKey)
	}
	apiHandler := api.NewHandlerWithRouteReporter(
		context.Background(),
		domainStore,
		allocationStore,
		agentStore,
		entitlementStore,
		cfg.Agent.Token,
		syncStatus,
		routeReporter,
		resolver,
	)
	apiServer := api.NewServer(cfg.API.Listen, apiHandler)

	go runReconciler(
		allocationStore,
		agentStore,
		cfg.Agent.OfflineAfter,
		cfg.Agent.ReconcileInterval,
	)

	errCh := make(chan error, 2)
	go func() { errCh <- dnsServer.Start() }()
	go func() { errCh <- apiServer.Start() }()

	if err := <-errCh; err != nil {
		log.Fatal(err)
	}
}

func runEntitlementSync(
	client *entitlement.Client,
	store *entitlement.Store,
	interval time.Duration,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		refreshEntitlements(client, store)
	}
}

func refreshEntitlements(
	client *entitlement.Client,
	store *entitlement.Store,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	snapshot, err := client.Fetch(ctx)
	if err != nil {
		log.Printf("backend entitlement refresh failed: %v", err)
		return
	}

	store.Replace(snapshot)
	log.Printf(
		"backend entitlements refreshed count=%d generated_at=%s",
		store.Count(),
		snapshot.GeneratedAt.Format(time.RFC3339),
	)
}

func runDNSControlRegistration(
	client *backendregistry.Client,
	registration backendregistry.RegisterRequest,
	interval time.Duration,
	domains *domain.Store,
	allocations *allocation.Store,
	status *api.DomainSyncStatus,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		registerDNSControl(client, registration, domains, allocations, status)
	}
}

func registerDNSControl(
	client *backendregistry.Client,
	registration backendregistry.RegisterRequest,
	domains *domain.Store,
	allocations *allocation.Store,
	status *api.DomainSyncStatus,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Register(ctx, registration); err != nil {
		log.Printf("backend DNS registration failed: %v", err)
		status.Record(0, 0, err)
		return
	}
	refreshDomainPolicies(client, domains, allocations, status)
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
		offline := agents.MarkOfflineBefore(
			time.Now().UTC().Add(-offlineAfter),
		)

		if requeued > 0 || offline > 0 {
			log.Printf(
				"control-plane: reconciled expired_leases=%d offline_agents=%d",
				requeued,
				offline,
			)
		}
	}

	reconcile()
	for range ticker.C {
		reconcile()
	}
}

func refreshDomainPolicies(client *backendregistry.Client, domains *domain.Store, allocations *allocation.Store, status *api.DomainSyncStatus) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snapshot, err := client.FetchDomains(ctx)
	if err != nil {
		log.Printf("backend domain policy sync failed: %v", err)
		status.Record(0, 0, err)
		return
	}
	if err := domains.Replace(snapshot.Domains); err != nil {
		log.Printf("backend domain policy validation failed: %v", err)
		status.Record(0, 0, err)
		return
	}
	allocations.ReconcilePolicies(snapshot.Domains)
	status.Record(snapshot.Revision, len(snapshot.Domains), nil)
	log.Printf("backend domain policies synchronized revision=%d count=%d", snapshot.Revision, len(snapshot.Domains))
}
