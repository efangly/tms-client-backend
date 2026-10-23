// Command parity-goapi serves the Go backend's HTTP routes without starting
// polling, archiving, MQTT or serial — only the database connection — so the
// C# port's responses can be diffed against the Go handlers on the same
// database without either side writing anything on its own.
//
// Usage (from the repo root): go run ./tools/parity-goapi -port 18081
//
// Only read-only endpoints are safe to compare. Note that report/temp-log
// requests that reach back past the archive retention window restore archive
// files into temp_log_archive (a write).
package main

import (
	"flag"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"

	"tms-backend/internal/database"
	"tms-backend/internal/handlers"
	"tms-backend/internal/services"
)

func main() {
	port := flag.String("port", "18081", "listen port")
	envFile := flag.String("env", ".env", "path to .env")
	flag.Parse()

	if err := godotenv.Load(*envFile); err != nil {
		log.Fatalf("load %s: %v", *envFile, err)
	}
	if err := database.Connect(); err != nil {
		log.Fatalf("db: %v", err)
	}

	// Constructed (needed by handlers) but never started.
	services.GlobalMQTTService = services.NewMQTTService()
	services.GlobalPollingService = services.NewPollingService()
	services.GlobalArchiveService = services.NewArchiveService()

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	handlers.RegisterRoutes(app)
	log.Printf("Go API (read-only parity harness) on :%s", *port)
	log.Fatal(app.Listen(":" + *port))
}
