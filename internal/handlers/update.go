package handlers

import (
	"github.com/gofiber/fiber/v2"

	"tms-backend/internal/updater"
	"tms-backend/internal/version"
)

// GetUpdateStatus handles GET /api/update/status.
func GetUpdateStatus(c *fiber.Ctx) error {
	if updater.Global == nil {
		return c.JSON(updater.Status{Current: version.Version, State: updater.StateDisabled, Reason: "updater not running"})
	}
	return c.JSON(updater.Global.Status())
}

// CheckForUpdate handles POST /api/update/check: triggers a check now. If a
// newer release exists it is downloaded and installed (honouring
// UPDATE_WINDOW and running Report Jobs), then the server restarts.
func CheckForUpdate(c *fiber.Ctx) error {
	if updater.Global == nil || updater.Global.Status().State == updater.StateDisabled {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "auto update is disabled"})
	}
	updater.Global.CheckNow()
	return c.Status(fiber.StatusAccepted).JSON(updater.Global.Status())
}
