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
	"fluxio-backend/internal/storage"
)

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
	registerSettingsRoutes(api, settingsRepo)

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

	pipelineCtx, cancelPipeline := context.WithCancel(context.Background())
	defer cancelPipeline()

	store, err := storage.NewClickHouseStore(os.Getenv("CLICKHOUSE_DSN"))
	if err != nil {
		log.Fatalf("Failed to connect to ClickHouse: %v", err)
	}

	writer := storage.NewBatchWriter(store, 1000, 5*time.Second)
	go writer.Run(pipelineCtx)

	geoIP, err := processor.NewGeoIPEnricher(os.Getenv("GEOIP_CITY_DB"), os.Getenv("GEOIP_ASN_DB"))
	if err != nil {
		log.Fatalf("Failed to initialize GeoIP enrichment: %v", err)
	}
	defer geoIP.Close()

	flowCh := make(chan processor.FlowRecord, 10000)
	go func() {
		for flow := range flowCh {
			geoIP.EnrichFlow(&flow)
			writer.WriteFlow(flow)
		}
	}()

	netflowPort := os.Getenv("NETFLOW_PORT")
	if netflowPort == "" {
		netflowPort = "2055"
	}
	go collector.StartNetFlowListener(netflowPort, flowCh)

	log.Printf("Starting Flux.io Backend on :%s", port)
	if err := app.Listen(":" + port); err != nil {
		log.Fatal(err)
	}
}
