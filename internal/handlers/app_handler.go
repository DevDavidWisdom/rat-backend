package handlers

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/mdm-system/backend/internal/models"
	"github.com/mdm-system/backend/internal/services"
)

type AppHandler struct {
	appService *services.AppService
}

func NewAppHandler(appService *services.AppService) *AppHandler {
	return &AppHandler{appService: appService}
}

func (h *AppHandler) ListApps(c *fiber.Ctx) error {
	orgID := c.Locals("org_id").(uuid.UUID)
	apps, err := h.appService.ListApps(c.Context(), orgID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse("LIST_FAILED", err.Error()))
	}
	return c.JSON(models.SuccessResponse(apps))
}

// UploadAPK handles multipart APK file upload
func (h *AppHandler) UploadAPK(c *fiber.Ctx) error {
	orgID := c.Locals("org_id").(uuid.UUID)
	userID := c.Locals("user_id").(uuid.UUID)

	// Get the uploaded file
	file, err := c.FormFile("file")
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse("NO_FILE", "APK file is required"))
	}

	// Get metadata from form fields
	appName := c.FormValue("app_name")
	if appName == "" {
		appName = file.Filename
	}
	packageName := c.FormValue("package_name")
	if packageName == "" {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse("MISSING_PACKAGE", "package_name is required"))
	}
	versionCodeStr := c.FormValue("version_code", "1")
	versionCode, _ := strconv.Atoi(versionCodeStr)
	if versionCode < 1 {
		versionCode = 1
	}
	versionName := c.FormValue("version_name", "1.0")
	description := c.FormValue("description", "")
	isMandatory := c.FormValue("is_mandatory") == "true"

	// Open the file
	f, err := file.Open()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse("FILE_ERROR", "Failed to read file"))
	}
	defer f.Close()

	app, err := h.appService.UploadAPK(c.Context(), orgID, &userID, f, file.Size, appName, packageName, versionCode, versionName, description, isMandatory)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse("UPLOAD_FAILED", err.Error()))
	}

	return c.Status(fiber.StatusCreated).JSON(models.SuccessResponse(app))
}

// DeployApp deploys an app to specified targets
func (h *AppHandler) DeployApp(c *fiber.Ctx) error {
	orgID := c.Locals("org_id").(uuid.UUID)
	appID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse("INVALID_ID", "Invalid app ID"))
	}

	type DeployRequest struct {
		DeviceIDs        []uuid.UUID `json:"device_ids"`
		GroupIDs         []uuid.UUID `json:"group_ids"`
		EnrollmentTokens []string    `json:"enrollment_tokens"`
		All              bool        `json:"all"`
	}
	var req DeployRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse("INVALID_BODY", err.Error()))
	}

	issuerID := c.Locals("user_id").(uuid.UUID)
	count, err := h.appService.DeployApp(c.Context(), orgID, appID, req.DeviceIDs, req.GroupIDs, req.EnrollmentTokens, req.All, &issuerID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse("DEPLOY_FAILED", err.Error()))
	}

	return c.JSON(models.SuccessResponse(fiber.Map{"queued": true, "targets": count}))
}

// DeleteApp removes an app and its APK from storage
func (h *AppHandler) DeleteApp(c *fiber.Ctx) error {
	appID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse("INVALID_ID", "Invalid app ID"))
	}

	if err := h.appService.DeleteApp(c.Context(), appID); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse("DELETE_FAILED", err.Error()))
	}

	return c.JSON(models.SuccessResponse(fiber.Map{"deleted": true}))
}

type AuditHandler struct {
	auditService *services.AuditService
}

func NewAuditHandler(auditService *services.AuditService) *AuditHandler {
	return &AuditHandler{auditService: auditService}
}

func (h *AuditHandler) ListLogs(c *fiber.Ctx) error {
	orgID := c.Locals("org_id").(uuid.UUID)
	page, _ := strconv.Atoi(c.Query("page", "1"))
	pageSize, _ := strconv.Atoi(c.Query("page_size", "50"))

	logs, total, err := h.auditService.ListLogs(c.Context(), orgID, page, pageSize)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse("LIST_FAILED", err.Error()))
	}

	return c.JSON(models.SuccessResponse(fiber.Map{
		"logs":  logs,
		"total": total,
		"page":  page,
	}))
}
