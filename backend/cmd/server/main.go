package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/websocket/v2"
	_ "github.com/lib/pq"

	"fluxio-backend/internal/collector"
	"fluxio-backend/internal/processor"
	"fluxio-backend/internal/settings"
	"fluxio-backend/internal/sources"
	"fluxio-backend/internal/storage"
)

// noopSwitcher satisfies the settings route's modeSwitcher without acting on a
// live listener. The global DPI mode is superseded by per-source dpi_mode; this
// keeps the legacy /api/settings route compiling until B2 removes it.
type noopSwitcher struct{}

func (noopSwitcher) SetMode(context.Context, string) error { return nil }

func main() {
	app := fiber.New()

	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "Origin, Content-Type, Accept",
	}))

	// Setup websocket route for alerts
	app.Use("/ws", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			c.Locals("allowed", true)
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})

	app.Get("/ws/alerts", websocket.New(func(c *websocket.Conn) {
		log.Println("WebSocket client connected")
		for {
			// This is a stub for the websocket connection
			// In a real app, you would read from a channel or a message broker
			messageType, msg, err := c.ReadMessage()
			if err != nil {
				log.Println("Read err:", err)
				break
			}
			log.Printf("Received: %s", msg)
			if err = c.WriteMessage(messageType, msg); err != nil {
				log.Println("Write err:", err)
				break
			}
		}
	}))

	api := app.Group("/api")

	pgDB, err := sql.Open("postgres", os.Getenv("POSTGRES_DSN"))
	if err != nil {
		log.Fatalf("Failed to open Postgres connection: %v", err)
	}
	defer pgDB.Close()
	settingsRepo := settings.NewRepository(pgDB)

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
	// Sources API without a restart. This supersedes the old global dpi_mode.
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

	// The global /api/settings route is retained but inert (its switch is a
	// no-op); per-source DPI mode supersedes it and B2 removes it entirely.
	registerSettingsRoutes(api, settingsRepo, noopSwitcher{})

	wazuhIP := os.Getenv("WAZUH_MANAGER_IP")
	wazuhPort := os.Getenv("WAZUH_MANAGER_PORT")
	if wazuhPort == "" {
		wazuhPort = "1514"
	}
	go collector.RunWazuhForwarder(pipelineCtx, eveLogPath, wazuhIP, wazuhPort)

	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	api.Post("/auth/login", func(c *fiber.Ctx) error {
		// Stub para gerar JWT. Em produção, buscar o hash Argon2id do Postgres.
		type LoginReq struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		var req LoginReq
		if err := c.BodyParser(&req); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid request"})
		}
		
		if req.Username == "admin" && req.Password == "admin" {
			// Retornar um token JWT mockado para cumprir o stub da interface
			return c.JSON(fiber.Map{
				"token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.mock.token",
				"role":  "Admin",
			})
		}
		return c.Status(401).JSON(fiber.Map{"error": "Unauthorized"})
	})

	// Servir o Frontend Estático construído pelo React (SPA)
	app.Static("/", "./public")

	// Rota de Fallback para o React Router lidar com a navegação interna
	app.Use(func(c *fiber.Ctx) error {
		// Se não for rota da API ou WS, retorna o index.html do frontend
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
