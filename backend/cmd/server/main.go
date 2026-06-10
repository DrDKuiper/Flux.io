package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"log"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	_ "github.com/lib/pq"

	"fluxio-backend/internal/api"
	"fluxio-backend/internal/auth"
	"fluxio-backend/internal/collector"
	"fluxio-backend/internal/processor"
	"fluxio-backend/internal/sources"
	"fluxio-backend/internal/storage"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	app := fiber.New()

	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
	}))

	pgDB, err := sql.Open("postgres", os.Getenv("POSTGRES_DSN"))
	if err != nil {
		log.Fatalf("Failed to open Postgres connection: %v", err)
	}
	defer pgDB.Close()

	pipelineCtx, cancelPipeline := context.WithCancel(context.Background())
	defer cancelPipeline()

	// Source registry: auto-discovers exporters/sensors, gates ingestion, and
	// holds per-host DPI mode. Warm the cache from Postgres at startup.
	sourceRepo := sources.NewRepository(pgDB)
	sourceReg := sources.NewRegistry(sourceRepo)
	if err := sourceReg.Load(context.Background()); err != nil {
		log.Printf("sources: failed to warm cache: %v", err)
	}
	// Seed the local Suricata sensor as its own source so DPI/alert capture is
	// on by default from boot (independent of any NetFlow exporter's dpi_mode)
	// and so the sensor is independently configurable on the Sources screen.
	sourceReg.Observe(context.Background(), "127.0.0.1", "suricata")
	sourceStats := sources.NewStats()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-pipelineCtx.Done():
				return
			case <-t.C:
				sourceStats.Roll()
			}
		}
	}()

	correlationCache := processor.NewCorrelationCache(30 * time.Second)
	go correlationCache.CleanupLoop(pipelineCtx, 10*time.Second)

	eveLogPath := os.Getenv("SURICATA_EVE_LOG_PATH")
	if eveLogPath == "" {
		eveLogPath = "/var/log/suricata/eve.json"
	}
	tzspPort := os.Getenv("TZSP_PORT")
	if tzspPort == "" {
		tzspPort = "37008"
	}

	store, err := storage.NewClickHouseStore(os.Getenv("CLICKHOUSE_DSN"))
	if err != nil {
		log.Fatalf("Failed to connect to ClickHouse: %v", err)
	}

	writer := storage.NewBatchWriter(store, 1000, 5*time.Second)
	go writer.Run(pipelineCtx)

	dpiManager := collector.NewDPIManager(correlationCache, collector.DPIManagerSources{
		Suricata: func(ctx context.Context) {
			collector.RunSuricataCorrelator(ctx, collector.NewFileTailer(eveLogPath), correlationCache, writer)
		},
		TZSP: func(ctx context.Context) {
			if err := collector.StartTZSPListener(ctx, tzspPort, correlationCache); err != nil {
				log.Printf("tzsp: listener stopped: %v", err)
			}
		},
	})

	// The DPI manager runs the union of mechanisms the enabled sources request
	// (per-source dpi_mode). A reconcile loop applies changes made via the
	// Sources API without a restart.
	reconcile := func() {
		suri, tzsp := sourceReg.RequestedMechanisms()
		dpiManager.Reconcile(pipelineCtx, suri, tzsp)
	}
	reconcile()
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-pipelineCtx.Done():
				return
			case <-t.C:
				reconcile()
			}
		}
	}()

	// Auth: JWT signer + user repo + admin seed.
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		// Generate a random secret rather than fall back to a known constant
		// (a public default would let anyone forge admin tokens). This secret is
		// ephemeral: tokens are invalidated on restart, so set JWT_SECRET in .env
		// for stable sessions across restarts.
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			log.Fatalf("auth: failed to generate a random JWT secret: %v", err)
		}
		jwtSecret = base64.RawURLEncoding.EncodeToString(buf)
		log.Println("WARNING: JWT_SECRET not set; generated an ephemeral one. Tokens reset on restart — set JWT_SECRET in .env to persist sessions.")
	}
	signer := auth.NewJWT(jwtSecret, 24*time.Hour)
	userRepo := auth.NewRepository(pgDB)
	adminUser := envOr("ADMIN_USERNAME", "admin")
	if pw, err := auth.SeedAdmin(context.Background(), userRepo, adminUser, os.Getenv("ADMIN_PASSWORD")); err != nil {
		log.Printf("auth: admin seed failed: %v", err)
	} else if pw != "" {
		log.Printf("auth: created admin user %q with generated password: %s", adminUser, pw)
	}

	// WebSocket hub + live producers.
	hub := api.NewHub()
	go hub.Run()
	go api.RunMetricsBroadcaster(pipelineCtx, hub, store)
	collector.AlertHook = func(a processor.SuricataAlert) {
		api.BroadcastAlert(hub, storage.AlertRow{
			TS: a.Timestamp, Source: "127.0.0.1", SrcIP: a.SourceIP, DstIP: a.DestinationIP,
			Signature: a.Signature, Category: a.Category, Severity: a.Severity,
		})
	}

	api.RegisterRoutes(app, api.Deps{
		Reader: store, Signer: signer, UserRepo: userRepo, Hub: hub,
		SourceReg: sourceReg, SourceRepo: sourceRepo, SourceStats: sourceStats,
	})

	wazuhIP := os.Getenv("WAZUH_MANAGER_IP")
	wazuhPort := os.Getenv("WAZUH_MANAGER_PORT")
	if wazuhPort == "" {
		wazuhPort = "1514"
	}
	go collector.RunWazuhForwarder(pipelineCtx, eveLogPath, wazuhIP, wazuhPort)

	// Serve the React SPA built into ./public, with a fallback to index.html so
	// client-side routing works. Registered after the API/WS routes so they win.
	app.Static("/", "./public")
	app.Use(func(c *fiber.Ctx) error {
		return c.SendFile("./public/index.html")
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	geoIP, err := processor.NewGeoIPEnricher(os.Getenv("GEOIP_CITY_DB"), os.Getenv("GEOIP_ASN_DB"))
	if err != nil {
		log.Fatalf("Failed to initialize GeoIP enrichment: %v", err)
	}
	defer geoIP.Close()

	flowCh := make(chan processor.FlowRecord, 10000)
	go func() {
		for flow := range flowCh {
			geoIP.EnrichFlow(&flow)

			// Apply DPI metadata per the source's configured dpi_mode.
			dpiMode := sourceReg.Observe(context.Background(), flow.Source, "netflow").DPIMode
			tuple := processor.FiveTuple{
				SrcIP: flow.SourceIP, DstIP: flow.DestinationIP,
				SrcPort: flow.SourcePort, DstPort: flow.DestinationPort, Protocol: flow.Protocol,
			}
			if meta, ok := correlationCache.GetForMode(tuple, dpiMode); ok {
				flow.Application = meta.Application
				flow.SNI = meta.SNI
				flow.HTTPHost = meta.HTTPHost
				flow.HTTPURL = meta.HTTPURL
			}

			sourceStats.Record(flow.Source, flow.Bytes)
			writer.WriteFlow(flow)
		}
	}()

	netflowPort := os.Getenv("NETFLOW_PORT")
	if netflowPort == "" {
		netflowPort = "2055"
	}
	go collector.StartNetFlowListener(netflowPort, flowCh, func(addr string) (bool, bool) {
		d := sourceReg.Observe(context.Background(), addr, "netflow")
		return d.Enabled, true
	})

	log.Printf("Starting Flux.io Backend on :%s", port)
	if err := app.Listen(":" + port); err != nil {
		log.Fatal(err)
	}
}
