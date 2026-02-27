package handlers

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/mdm-system/backend/internal/models"
	"github.com/mdm-system/backend/internal/services"
)

type DeviceHandler struct {
	deviceService *services.DeviceService
}

func NewDeviceHandler(deviceService *services.DeviceService) *DeviceHandler {
	return &DeviceHandler{deviceService: deviceService}
}

// ListDevices returns paginated device list
func (h *DeviceHandler) ListDevices(c *fiber.Ctx) error {
	orgID := c.Locals("org_id").(uuid.UUID)

	page, _ := strconv.Atoi(c.Query("page", "1"))
	pageSize, _ := strconv.Atoi(c.Query("page_size", "20"))
	search := c.Query("search", "")
	status := c.Query("status", "")
	groupID := c.Query("group_id", "")

	filter := &models.DeviceFilter{
		OrganizationID: &orgID,
		Page:           page,
		PageSize:       pageSize,
		Search:         search,
	}

	if status != "" {
		filter.Status = []models.DeviceStatus{models.DeviceStatus(status)}
	}

	if groupID != "" {
		if gid, err := uuid.Parse(groupID); err == nil {
			filter.GroupID = &gid
		}
	}

	response, err := h.deviceService.ListDevices(c.Context(), filter)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"LIST_FAILED",
			err.Error(),
		))
	}

	return c.JSON(models.SuccessResponse(response))
}

func (h *DeviceHandler) ExportDevices(c *fiber.Ctx) error {
	orgID := c.Locals("org_id").(uuid.UUID)

	devices, err := h.deviceService.ListAllDevices(c.Context(), orgID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse("EXPORT_FAILED", err.Error()))
	}

	c.Set("Content-Type", "text/csv")
	c.Set("Content-Disposition", "attachment; filename=devices_export.csv")

	// Header
	export := "ID,Name,Device ID,Model,Manufacturer,Status,Battery,Last Seen\n"
	for _, d := range devices {
		name := ""
		if d.Name != nil {
			name = *d.Name
		}
		lastSeen := "Never"
		if d.LastSeen != nil {
			lastSeen = d.LastSeen.Format("2006-01-02 15:04:05")
		}

		battery := "0"
		if d.BatteryLevel != nil {
			battery = strconv.Itoa(int(*d.BatteryLevel))
		}

		export += d.ID.String() + "," + name + "," + d.DeviceID + "," +
			*d.Model + "," + *d.Manufacturer + "," + string(d.Status) + "," +
			battery + "," + lastSeen + "\n"
	}

	return c.SendString(export)
}

// GetDevice returns a single device by ID
func (h *DeviceHandler) GetDevice(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_ID",
			"Invalid device ID",
		))
	}

	device, err := h.deviceService.GetDevice(c.Context(), id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(models.ErrorResponse(
			"NOT_FOUND",
			"Device not found",
		))
	}

	return c.JSON(models.SuccessResponse(device))
}

// UpdateDevice updates device details
func (h *DeviceHandler) UpdateDevice(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_ID",
			"Invalid device ID",
		))
	}

	var req models.UpdateDeviceRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body",
		))
	}

	device, err := h.deviceService.UpdateDevice(c.Context(), id, &req)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"UPDATE_FAILED",
			err.Error(),
		))
	}

	return c.JSON(models.SuccessResponse(device))
}

// DeleteDevice removes a device
func (h *DeviceHandler) DeleteDevice(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_ID",
			"Invalid device ID",
		))
	}

	if err := h.deviceService.DeleteDevice(c.Context(), id); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"DELETE_FAILED",
			err.Error(),
		))
	}

	return c.JSON(models.SuccessResponse(fiber.Map{"deleted": true}))
}

// CreateEnrollment creates a new enrollment token
func (h *DeviceHandler) CreateEnrollment(c *fiber.Ctx) error {
	orgID := c.Locals("org_id").(uuid.UUID)
	userID := c.Locals("user_id").(uuid.UUID)

	var req models.CreateEnrollmentRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body",
		))
	}

	enrollment, err := h.deviceService.CreateEnrollment(c.Context(), &req, orgID, userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"CREATE_FAILED",
			err.Error(),
		))
	}

	return c.Status(fiber.StatusCreated).JSON(models.SuccessResponse(enrollment))
}

// ListEnrollments returns all enrollment tokens
func (h *DeviceHandler) ListEnrollments(c *fiber.Ctx) error {
	orgID := c.Locals("org_id").(uuid.UUID)

	enrollments, err := h.deviceService.ListEnrollments(c.Context(), orgID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"LIST_FAILED",
			err.Error(),
		))
	}

	return c.JSON(models.SuccessResponse(enrollments))
}

// DeactivateEnrollment deactivates an enrollment token
func (h *DeviceHandler) DeactivateEnrollment(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_ID",
			"Invalid enrollment ID",
		))
	}

	if err := h.deviceService.DeactivateEnrollment(c.Context(), id); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"DEACTIVATE_FAILED",
			err.Error(),
		))
	}

	return c.JSON(models.SuccessResponse(fiber.Map{"deactivated": true}))
}

// RegisterDevice handles device self-registration
func (h *DeviceHandler) RegisterDevice(c *fiber.Ctx) error {
	var req models.DeviceRegistrationRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body",
		))
	}

	response, err := h.deviceService.RegisterDevice(c.Context(), &req)
	if err != nil {
		status := fiber.StatusInternalServerError
		if err == services.ErrInvalidEnrollment {
			status = fiber.StatusUnauthorized
		} else if err == services.ErrDeviceAlreadyExists {
			status = fiber.StatusConflict
		}
		return c.Status(status).JSON(models.ErrorResponse(
			"REGISTRATION_FAILED",
			err.Error(),
		))
	}

	return c.Status(fiber.StatusCreated).JSON(models.SuccessResponse(response))
}

// GetStats returns dashboard statistics
func (h *DeviceHandler) GetStats(c *fiber.Ctx) error {
	orgID := c.Locals("org_id").(uuid.UUID)

	stats, err := h.deviceService.GetStats(c.Context(), orgID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"STATS_FAILED",
			err.Error(),
		))
	}

	return c.JSON(models.SuccessResponse(stats))
}

// ReportTelemetry handles telemetry reports from devices
func (h *DeviceHandler) ReportTelemetry(c *fiber.Ctx) error {
	deviceIdentity := c.Locals("device_identity").(string)

	var telemetry models.TelemetryReport
	if err := c.BodyParser(&telemetry); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body",
		))
	}

	telemetry.DeviceID = deviceIdentity

	if err := h.deviceService.UpdateTelemetry(c.Context(), deviceIdentity, &telemetry); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse(
			"TELEMETRY_FAILED",
			err.Error(),
		))
	}

	return c.JSON(models.SuccessResponse(fiber.Map{"received": true}))
}
