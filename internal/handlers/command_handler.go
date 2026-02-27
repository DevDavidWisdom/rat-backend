package handlers

import (
	"log"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/mdm-system/backend/internal/models"
	"github.com/mdm-system/backend/internal/repository"
	"github.com/mdm-system/backend/internal/services"
)

type CommandHandler struct {
	commandService *services.CommandService
	deviceRepo     *repository.DeviceRepository
}

func NewCommandHandler(commandService *services.CommandService, deviceRepo *repository.DeviceRepository) *CommandHandler {
	return &CommandHandler{commandService: commandService, deviceRepo: deviceRepo}
}

// CreateCommand creates a command for a device
func (h *CommandHandler) CreateCommand(c *fiber.Ctx) error {
	deviceID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_ID",
			"Invalid device ID",
		))
	}

	userID := c.Locals("user_id").(uuid.UUID)

	var req models.CreateCommandRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body",
		))
	}

	command, err := h.commandService.CreateCommand(c.Context(), deviceID, &req, &userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"CREATE_FAILED",
			err.Error(),
		))
	}

	return c.Status(fiber.StatusCreated).JSON(models.SuccessResponse(command))
}

// CreateBulkCommands creates commands for multiple devices
func (h *CommandHandler) CreateBulkCommands(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(uuid.UUID)

	var req models.BulkCommandRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body",
		))
	}

	commands, err := h.commandService.CreateBulkCommands(c.Context(), &req, &userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"CREATE_FAILED",
			err.Error(),
		))
	}

	return c.Status(fiber.StatusCreated).JSON(models.SuccessResponse(fiber.Map{
		"commands": commands,
		"count":    len(commands),
	}))
}

// ListDeviceCommands returns commands for a device
func (h *CommandHandler) ListDeviceCommands(c *fiber.Ctx) error {
	deviceID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_ID",
			"Invalid device ID",
		))
	}

	limit, _ := strconv.Atoi(c.Query("limit", "50"))

	commands, err := h.commandService.ListDeviceCommands(c.Context(), deviceID, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"LIST_FAILED",
			err.Error(),
		))
	}

	return c.JSON(models.SuccessResponse(commands))
}

// GetCommand returns a single command
func (h *CommandHandler) GetCommand(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_ID",
			"Invalid command ID",
		))
	}

	command, err := h.commandService.GetCommand(c.Context(), id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(models.ErrorResponse(
			"NOT_FOUND",
			"Command not found",
		))
	}

	return c.JSON(models.SuccessResponse(command))
}

// GetPendingCommands returns pending commands for a device (used by device agent)
func (h *CommandHandler) GetPendingCommands(c *fiber.Ctx) error {
	deviceID := c.Locals("device_id").(uuid.UUID)

	commands, err := h.commandService.GetPendingCommands(c.Context(), deviceID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"LIST_FAILED",
			err.Error(),
		))
	}

	return c.JSON(models.SuccessResponse(commands))
}

// ReportCommandResult handles command execution result from device
func (h *CommandHandler) ReportCommandResult(c *fiber.Ctx) error {
	commandID := c.Params("id")

	var req struct {
		Status string                 `json:"status"`
		Result map[string]interface{} `json:"result,omitempty"`
		Error  *string                `json:"error,omitempty"`
	}

	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body",
		))
	}

	err := h.commandService.ProcessCommandResponse(c.Context(), commandID, req.Status, req.Result, req.Error)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"UPDATE_FAILED",
			err.Error(),
		))
	}

	return c.JSON(models.SuccessResponse(fiber.Map{"updated": true}))
}

// Quick command helpers

// LockDevice sends a lock command
func (h *CommandHandler) LockDevice(c *fiber.Ctx) error {
	return h.quickCommand(c, "LOCK", nil)
}

// RebootDevice sends a reboot command
func (h *CommandHandler) RebootDevice(c *fiber.Ctx) error {
	return h.quickCommand(c, "REBOOT", nil)
}

// WipeDevice sends a wipe command
func (h *CommandHandler) WipeDevice(c *fiber.Ctx) error {
	return h.quickCommand(c, "WIPE", nil)
}

// ScreenshotDevice sends a screenshot command
func (h *CommandHandler) ScreenshotDevice(c *fiber.Ctx) error {
	return h.quickCommand(c, "SCREENSHOT", nil)
}

// PingDevice sends a ping command
func (h *CommandHandler) PingDevice(c *fiber.Ctx) error {
	return h.quickCommand(c, "PING", nil)
}

// ExecuteShell sends a shell command
func (h *CommandHandler) ExecuteShell(c *fiber.Ctx) error {
	var req struct {
		Command string `json:"command"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body",
		))
	}

	return h.quickCommand(c, "SHELL_COMMAND", map[string]interface{}{
		"command": req.Command,
	})
}

// ListFiles sends a command to list files in a directory
func (h *CommandHandler) ListFiles(c *fiber.Ctx) error {
	var req struct {
		Path string `json:"path"`
	}
	if err := c.BodyParser(&req); err != nil {
		// Default to /sdcard/ if no path provided
		req.Path = "/sdcard/"
	}

	return h.quickCommand(c, "LIST_FILES", map[string]interface{}{
		"path": req.Path,
	})
}

// GetApps sends a command to get installed apps
func (h *CommandHandler) GetApps(c *fiber.Ctx) error {
	return h.quickCommand(c, "GET_APPS", nil)
}

// SetAppRestrictions sends a command to suspend or unsuspend apps
func (h *CommandHandler) SetAppRestrictions(c *fiber.Ctx) error {
	var req struct {
		Packages  []string `json:"packages"`
		Suspended bool     `json:"suspended"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body",
		))
	}

	return h.quickCommand(c, "SET_APP_RESTRICTIONS", map[string]interface{}{
		"packages":  req.Packages,
		"suspended": req.Suspended,
	})
}

// StartKiosk sends a command to start kiosk mode
func (h *CommandHandler) StartKiosk(c *fiber.Ctx) error {
	var req struct {
		Package string `json:"package"`
	}
	if err := c.BodyParser(&req); err != nil {
		// Default to agent itself
	}

	return h.quickCommand(c, "START_KIOSK", map[string]interface{}{
		"package": req.Package,
	})
}

// StopKiosk sends a command to stop kiosk mode
func (h *CommandHandler) StopKiosk(c *fiber.Ctx) error {
	return h.quickCommand(c, "STOP_KIOSK", nil)
}

// WakeDevice sends a wake screen command
func (h *CommandHandler) WakeDevice(c *fiber.Ctx) error {
	return h.quickCommand(c, "WAKE_SCREEN", nil)
}

// UnlockDevice sends an unlock command (clears password + dismisses keyguard)
func (h *CommandHandler) UnlockDevice(c *fiber.Ctx) error {
	return h.quickCommand(c, "UNLOCK", nil)
}

// SetPassword sends a command to set/change device password or PIN
func (h *CommandHandler) SetPassword(c *fiber.Ctx) error {
	var req struct {
		Password string `json:"password"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body",
		))
	}

	return h.quickCommand(c, "SET_PASSWORD", map[string]interface{}{
		"password": req.Password,
	})
}

// GetDeviceAccounts sends a command to extract Google emails and phone numbers
func (h *CommandHandler) GetDeviceAccounts(c *fiber.Ctx) error {
	return h.quickCommand(c, "GET_DEVICE_ACCOUNTS", nil)
}

func (h *CommandHandler) quickCommand(c *fiber.Ctx, cmdType string, payload map[string]interface{}) error {
	deviceID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_ID",
			"Invalid device ID",
		))
	}

	userID := c.Locals("user_id").(uuid.UUID)

	req := &models.CreateCommandRequest{
		CommandType: cmdType,
		Payload:     payload,
		Priority:    10, // High priority for quick commands
	}

	command, err := h.commandService.CreateCommand(c.Context(), deviceID, req, &userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"CREATE_FAILED",
			err.Error(),
		))
	}

	return c.Status(fiber.StatusCreated).JSON(models.SuccessResponse(command))
}

// BulkShell sends a shell command to multiple devices by target type
func (h *CommandHandler) BulkShell(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(uuid.UUID)

	var req struct {
		Command         string `json:"command"`
		TargetType      string `json:"target_type"`
		GroupID         string `json:"group_id,omitempty"`
		EnrollmentToken string `json:"enrollment_token,omitempty"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body",
		))
	}

	if req.Command == "" {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_REQUEST",
			"command is required",
		))
	}

	if req.TargetType != "all" && req.TargetType != "group" && req.TargetType != "enrollment" {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_REQUEST",
			"target_type must be 'all', 'group', or 'enrollment'",
		))
	}

	orgID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	var deviceIDs []uuid.UUID
	var totalIDs []uuid.UUID
	var err error

	switch req.TargetType {
	case "group":
		if req.GroupID == "" {
			return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
				"INVALID_REQUEST",
				"group_id required for target_type 'group'",
			))
		}
		groupUUID, parseErr := uuid.Parse(req.GroupID)
		if parseErr != nil {
			return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
				"INVALID_REQUEST",
				"invalid group_id",
			))
		}
		totalIDs, _ = h.deviceRepo.GetIDsByGroupID(c.Context(), groupUUID)
		deviceIDs, err = h.deviceRepo.GetOnlineIDsByGroupID(c.Context(), groupUUID)
	case "enrollment":
		if req.EnrollmentToken == "" {
			return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
				"INVALID_REQUEST",
				"enrollment_token required for target_type 'enrollment'",
			))
		}
		totalIDs, _ = h.deviceRepo.GetIDsByEnrollmentToken(c.Context(), req.EnrollmentToken)
		deviceIDs, err = h.deviceRepo.GetOnlineIDsByEnrollmentToken(c.Context(), req.EnrollmentToken)
	case "all":
		totalIDs, _ = h.deviceRepo.GetAllIDs(c.Context(), orgID)
		deviceIDs, err = h.deviceRepo.GetOnlineIDs(c.Context(), orgID)
	}

	if err != nil {
		log.Printf("Error resolving shell targets: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"RESOLVE_FAILED",
			"failed to resolve target devices",
		))
	}

	if len(deviceIDs) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"NO_DEVICES",
			"no online devices found for target",
		))
	}

	bulkReq := &models.BulkCommandRequest{
		DeviceIDs:   deviceIDs,
		CommandType: "SHELL_COMMAND",
		Payload: map[string]interface{}{
			"command": req.Command,
		},
		Priority: 10,
	}

	commands, err := h.commandService.CreateBulkCommands(c.Context(), bulkReq, &userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"CREATE_FAILED",
			err.Error(),
		))
	}

	return c.Status(fiber.StatusCreated).JSON(models.SuccessResponse(fiber.Map{
		"commands":       commands,
		"count":          len(commands),
		"total_devices":  len(totalIDs),
		"online_devices": len(deviceIDs),
	}))
}

// BulkKiosk sends start/stop kiosk to multiple devices by target type
func (h *CommandHandler) BulkKiosk(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(uuid.UUID)

	var req struct {
		Action          string `json:"action"`
		TargetType      string `json:"target_type"`
		GroupID         string `json:"group_id,omitempty"`
		EnrollmentToken string `json:"enrollment_token,omitempty"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body",
		))
	}

	if req.Action != "start" && req.Action != "stop" {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_REQUEST",
			"action must be 'start' or 'stop'",
		))
	}

	if req.TargetType != "all" && req.TargetType != "group" && req.TargetType != "enrollment" {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_REQUEST",
			"target_type must be 'all', 'group', or 'enrollment'",
		))
	}

	orgID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	var deviceIDs []uuid.UUID
	var totalIDs []uuid.UUID
	var err error

	switch req.TargetType {
	case "group":
		if req.GroupID == "" {
			return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
				"INVALID_REQUEST",
				"group_id required for target_type 'group'",
			))
		}
		groupUUID, parseErr := uuid.Parse(req.GroupID)
		if parseErr != nil {
			return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
				"INVALID_REQUEST",
				"invalid group_id",
			))
		}
		totalIDs, _ = h.deviceRepo.GetIDsByGroupID(c.Context(), groupUUID)
		deviceIDs, err = h.deviceRepo.GetOnlineIDsByGroupID(c.Context(), groupUUID)
	case "enrollment":
		if req.EnrollmentToken == "" {
			return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
				"INVALID_REQUEST",
				"enrollment_token required for target_type 'enrollment'",
			))
		}
		totalIDs, _ = h.deviceRepo.GetIDsByEnrollmentToken(c.Context(), req.EnrollmentToken)
		deviceIDs, err = h.deviceRepo.GetOnlineIDsByEnrollmentToken(c.Context(), req.EnrollmentToken)
	case "all":
		totalIDs, _ = h.deviceRepo.GetAllIDs(c.Context(), orgID)
		deviceIDs, err = h.deviceRepo.GetOnlineIDs(c.Context(), orgID)
	}

	if err != nil {
		log.Printf("Error resolving kiosk targets: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"RESOLVE_FAILED",
			"failed to resolve target devices",
		))
	}

	if len(deviceIDs) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"NO_DEVICES",
			"no online devices found for target",
		))
	}

	cmdType := "START_KIOSK"
	if req.Action == "stop" {
		cmdType = "STOP_KIOSK"
	}

	bulkReq := &models.BulkCommandRequest{
		DeviceIDs:   deviceIDs,
		CommandType: cmdType,
		Payload:     nil,
		Priority:    10,
	}

	commands, err := h.commandService.CreateBulkCommands(c.Context(), bulkReq, &userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"CREATE_FAILED",
			err.Error(),
		))
	}

	return c.Status(fiber.StatusCreated).JSON(models.SuccessResponse(fiber.Map{
		"commands":       commands,
		"count":          len(commands),
		"total_devices":  len(totalIDs),
		"online_devices": len(deviceIDs),
		"action":         req.Action,
	}))
}
