package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/joho/godotenv"

	"tms-backend/internal/database"
	"tms-backend/internal/handlers"
	"tms-backend/internal/services"
	"tms-backend/internal/utils"
)

func waitForEnter() {
	fmt.Println("\n🔴 Press Enter to exit...")
	bufio.NewReader(os.Stdin).ReadBytes('\n')
}

func main() {
	// Recover from any panic
	defer func() {
		if r := recover(); r != nil {
			log.Printf("\n❌ PANIC: %v", r)
			log.Println("\n📋 Stack trace:")
			log.Printf("%v", r)
			waitForEnter()
			os.Exit(1)
		}
	}()

	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("⚠️  No .env file found, using environment variables")
		log.Println("💡 Make sure .env file exists in the same folder as the executable")
	}

	// Initialize error logger
	if err := utils.InitLogger(); err != nil {
		log.Printf("⚠️  Failed to initialize error logger: %v", err)
		log.Println("Continuing without file logging...")
	}
	defer utils.CloseLogger()

	// Initialize database
	log.Println("🔌 Connecting to database...")
	if err := database.Connect(); err != nil {
		utils.LogError("Failed to connect to database: %v", err)
		log.Printf("❌ Failed to connect to database: %v", err)
		log.Println("\n📋 Please check:")
		log.Println("   1. .env file exists and has correct database credentials")
		log.Println("   2. Database server is accessible (check DB_HOST, DB_PORT)")
		log.Println("   3. Username and password are correct (DB_USER, DB_PASSWORD)")
		log.Println("   4. Database exists (DB_NAME)")
		waitForEnter()
		os.Exit(1)
	}
	log.Println("✅ Database connected successfully")

	// Initialize Fiber app
	app := fiber.New(fiber.Config{
		AppName: "TMS Backend API",
	})

	// Middleware
	app.Use(logger.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowMethods: "GET,POST,PUT,DELETE,OPTIONS",
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
	}))

	// Health check
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// API routes
	api := app.Group("/api")

	// Device routes
	api.Get("/devices", handlers.GetDevices)
	api.Get("/devices/:id", handlers.GetDevice)
	api.Post("/devices", handlers.CreateDevice)
	api.Put("/devices/:id", handlers.UpdateDevice)
	api.Delete("/devices/:id", handlers.DeleteDevice)

	// Machine routes (legacy compatibility)
	api.Get("/machines", handlers.GetMachines)
	api.Put("/machines/:machineIp/:probeNo", handlers.UpdateMachine)

	// Temperature logs
	api.Get("/temp-logs", handlers.GetTempLogs)
	api.Get("/reports/templog", handlers.GetTempLogReport)

	// Temperature errors
	api.Get("/temp-errors", handlers.GetTempErrors)

	// Polling control
	api.Get("/poll", handlers.TriggerPoll)

	// SSE for real-time updates
	api.Get("/temperature-stream", handlers.TemperatureStream)

	// Start polling service
	pollingSvc := services.NewPollingService()
	go pollingSvc.Start()

	// Graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("Shutting down gracefully...")
		pollingSvc.Stop()
		app.Shutdown()
		os.Exit(0)
	}()

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Println("========================================")
	log.Printf("🚀 Starting TMS Backend Server on port %s", port)
	log.Println("========================================")
	log.Println("✨ Server is ready!")
	log.Println("📝 Press Ctrl+C or close this window to stop the server")
	log.Println("")

	if err := app.Listen(":" + port); err != nil {
		utils.LogError("Failed to start server: %v", err)
		log.Printf("❌ Failed to start server: %v", err)
		log.Println("\n📋 Possible reasons:")
		log.Printf("   1. Port %s is already in use by another application\n", port)
		log.Println("   2. Insufficient permissions to bind to the port")
		log.Println("💡 Try changing the PORT in .env file to a different number (e.g., 8081)")
		waitForEnter()
		os.Exit(1)
	}
}
