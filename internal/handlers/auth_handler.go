package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/mdm-system/backend/internal/models"
	"github.com/mdm-system/backend/internal/services"
)

type AuthHandler struct {
	authService *services.AuthService
}

func NewAuthHandler(authService *services.AuthService) *AuthHandler {
	return &AuthHandler{authService: authService}
}

func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req models.LoginRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse("INVALID_REQUEST", "Invalid request body"))
	}
	response, err := h.authService.Login(c.Context(), &req)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(models.ErrorResponse("LOGIN_FAILED", err.Error()))
	}
	return c.JSON(models.SuccessResponse(response))
}

func (h *AuthHandler) Register(c *fiber.Ctx) error {
	var req models.RegisterRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse("INVALID_REQUEST", "Invalid request body"))
	}
	orgID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	user, err := h.authService.Register(c.Context(), &req, orgID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse("REGISTRATION_FAILED", err.Error()))
	}
	return c.Status(fiber.StatusCreated).JSON(models.SuccessResponse(user))
}

func (h *AuthHandler) RefreshToken(c *fiber.Ctx) error {
	var req models.RefreshTokenRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse("INVALID_REQUEST", "Invalid request body"))
	}
	response, err := h.authService.RefreshToken(c.Context(), req.RefreshToken)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(models.ErrorResponse("REFRESH_FAILED", err.Error()))
	}
	return c.JSON(models.SuccessResponse(response))
}

func (h *AuthHandler) Me(c *fiber.Ctx) error {
	userID := c.Locals("user_id").(uuid.UUID)
	email := c.Locals("email").(string)
	role := c.Locals("role").(models.UserRole)
	orgID := c.Locals("org_id").(uuid.UUID)
	return c.JSON(models.SuccessResponse(fiber.Map{
		"user_id":         userID,
		"email":           email,
		"role":            role,
		"organization_id": orgID,
	}))
}
