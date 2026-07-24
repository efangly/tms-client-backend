package handlers

import (
	"github.com/gofiber/fiber/v2"

	"tms-backend/internal/database"
	"tms-backend/internal/services"
)

// RegisterRoutes wires every HTTP route (including /health) onto app. Shared
// by main.go, the in-process handler tests, and the e2e suite so the route
// table only lives in one place.
func RegisterRoutes(app *fiber.App) {
	// Health check — includes dependency status
	app.Get("/health", func(c *fiber.Ctx) error {
		dbOK := false
		if sqlDB, err := database.DB.DB(); err == nil {
			dbOK = sqlDB.Ping() == nil
		}
		mqttOK := services.GlobalMQTTService != nil && services.GlobalMQTTService.IsConnected()
		status, code := "ok", 200
		if !dbOK {
			status, code = "degraded", 503
		}
		return c.Status(code).JSON(fiber.Map{"status": status, "db": dbOK, "mqtt": mqttOK})
	})

	// API routes
	api := app.Group("/api")

	// Device routes
	api.Get("/devices", GetDevices)
	api.Get("/devices/:id", GetDevice)
	api.Post("/devices", CreateDevice)
	api.Put("/devices/:id", UpdateDevice)
	api.Delete("/devices/:id", DeleteDevice)

	// Machine routes (legacy compatibility)
	api.Get("/machines", GetMachines)
	api.Put("/machines/:machineIp/:probeNo", UpdateMachine)

	// Schedule routes (stored in color field of master_machine)
	api.Get("/machines/:machineIp/:probeNo/schedule", GetSchedule)
	api.Put("/machines/:machineIp/:probeNo/schedule", SetSchedule)
	api.Post("/machines/:machineIp/:probeNo/schedule/:time", AddScheduleTime)
	api.Delete("/machines/:machineIp/:probeNo/schedule/:time", RemoveScheduleTime)

	// Temperature logs
	api.Get("/temp-logs", GetTempLogs)
	api.Get("/reports/templog", GetTempLogReport)

	// Temperature errors
	api.Get("/temp-errors", GetTempErrors)

	// Archive: move old temp_log data to local files, and restore it back
	// into temp_log_archive for reporting on demand.
	api.Post("/archive/run", RunArchiveHandler)
	api.Get("/archive", GetArchiveManifests)
	api.Post("/archive/restore", RestoreArchiveHandler)

	// Polling control (POST — triggers a state change)
	api.Post("/poll", TriggerPoll)

	// SSE for real-time updates
	api.Get("/temperature-stream", TemperatureStream)
}
