package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	fiberlogger "github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/joho/godotenv"

	"tms-backend/internal/database"
	"tms-backend/internal/handlers"
	"tms-backend/internal/services"
	"tms-backend/internal/tray"
	"tms-backend/internal/utils"
)

var fiberApp *fiber.App

func waitForEnter() {
	fmt.Println("\n🔴 Press Enter to exit...")
	bufio.NewReader(os.Stdin).ReadBytes('\n')
}

// setupFileLogger redirects the standard log output to a file
// when running in system tray mode (no console window).
func setupFileLogger() (*os.File, error) {
	logsDir := "logs"
	os.MkdirAll(logsDir, 0755)

	logFile, err := os.OpenFile("logs/tms-backend.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}

	// Redirect standard log to file
	log.SetOutput(io.MultiWriter(logFile))
	return logFile, nil
}

func startServer() {
	// Recover from any panic
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC: %v", r)
			tray.SetError(fmt.Sprintf("PANIC: %v", r))
		}
	}()

	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	// Initialize error logger
	if err := utils.InitLogger(); err != nil {
		log.Printf("Failed to initialize error logger: %v", err)
	}

	// Wait for network to be ready (important for startup)
	log.Println("Waiting for network connectivity...")
	if !utils.WaitForNetwork(60 * time.Second) {
		utils.LogError("Network not ready after timeout")
		log.Println("WARNING: Network may not be ready, but continuing anyway...")
	}

	// Initialize database with retry
	log.Println("Connecting to database...")
	err := utils.RetryWithBackoff(
		"Database connection",
		func() error {
			return database.Connect()
		},
		5,              // max attempts
		2*time.Second,  // initial delay
		30*time.Second, // max delay
	)
	if err != nil {
		utils.LogError("Failed to connect to database after retries: %v", err)
		log.Printf("Failed to connect to database: %v", err)
		tray.SetError("Database connection failed")
		return
	}
	log.Println("Database connected successfully")

	// Initialize MQTT service with retry
	log.Println("Initializing MQTT service...")
	services.GlobalMQTTService = services.NewMQTTService()
	if services.GlobalMQTTService.IsEnabled() {
		err := utils.RetryWithBackoff(
			"MQTT connection",
			func() error {
				return services.GlobalMQTTService.Connect()
			},
			5,              // max attempts
			2*time.Second,  // initial delay
			30*time.Second, // max delay
		)
		if err != nil {
			utils.LogError("Failed to connect to MQTT broker after retries: %v", err)
			log.Printf("MQTT connection failed after retries: %v (continuing without MQTT)", err)
		}
	}

	// Initialize Polling service (after MQTT is ready)
	log.Println("Initializing polling service...")
	services.GlobalPollingService = services.NewPollingService()

	// Initialize Fiber app
	fiberApp = fiber.New(fiber.Config{
		AppName:               "TMS Backend API",
		DisableStartupMessage: true,
	})

	// Middleware
	fiberApp.Use(fiberlogger.New())
	fiberApp.Use(cors.New())

	// Health check
	fiberApp.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// API routes
	api := fiberApp.Group("/api")

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
	log.Println("Starting polling service...")
	go services.GlobalPollingService.Start()

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Println("========================================")
	log.Printf("Starting TMS Backend Server on port %s", port)
	log.Println("========================================")

	tray.SetRunning()

	if err := fiberApp.Listen(":" + port); err != nil {
		utils.LogError("Failed to start server: %v", err)
		log.Printf("Failed to start server: %v", err)
		tray.SetError(fmt.Sprintf("Port %s in use", port))
	}
}

func cleanup() {
	log.Println("Shutting down gracefully...")
	if services.GlobalPollingService != nil {
		services.GlobalPollingService.Stop()
	}
	if services.GlobalMQTTService != nil {
		services.GlobalMQTTService.Disconnect()
	}
	if fiberApp != nil {
		fiberApp.Shutdown()
	}
	utils.CloseLogger()
}

func main() {
	// Setup file-only logging for tray mode (no console)
	logFile, err := setupFileLogger()
	if err != nil {
		// If we can't set up file logging, try to continue anyway
		log.Printf("Warning: could not setup file logger: %v", err)
	}
	if logFile != nil {
		defer logFile.Close()
	}

	// Get port for tray tooltip
	if err := godotenv.Load(); err == nil {
		// .env loaded successfully
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Run as system tray application
	tray.Run(port, startServer, cleanup)
}
