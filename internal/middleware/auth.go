package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/mdm-system/backend/internal/models"
	"github.com/mdm-system/backend/internal/services"
)

type AuthMiddleware struct {
	authService *services.AuthService
}

func NewAuthMiddleware(authService *services.AuthService) *AuthMiddleware {
	return &AuthMiddleware{authService: authService}
}

func (m *AuthMiddleware) Protected() fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(models.ErrorResponse("UNAUTHORIZED", "Missing authorization header"))
		}
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(models.ErrorResponse("UNAUTHORIZED", "Invalid authorization header format"))
		}
		token := parts[1]
		claims, err := m.authService.ValidateToken(token)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(models.ErrorResponse("UNAUTHORIZED", err.Error()))
		}
		c.Locals("user_id", claims.UserID)
		c.Locals("org_id", claims.OrganizationID)
		c.Locals("email", claims.Email)
		c.Locals("role", claims.Role)
		return c.Next()
	}
}

func (m *AuthMiddleware) RequireRole(roles ...models.UserRole) fiber.Handler {
	return func(c *fiber.Ctx) error {
		userRole, ok := c.Locals("role").(models.UserRole)
		if !ok {
			return c.Status(fiber.StatusForbidden).JSON(models.ErrorResponse("FORBIDDEN", "Unable to determine user role"))
		}
		for _, role := range roles {
			if userRole == role {
				return c.Next()
			}
		}
		return c.Status(fiber.StatusForbidden).JSON(models.ErrorResponse("FORBIDDEN", "Insufficient permissions"))
	}
}

func (m *AuthMiddleware) DeviceAuth() fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(models.ErrorResponse("UNAUTHORIZED", "Missing authorization header"))
		}
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(models.ErrorResponse("UNAUTHORIZED", "Invalid authorization header format"))
		}
		token := parts[1]
		claims, err := m.authService.ValidateDeviceToken(token)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(models.ErrorResponse("UNAUTHORIZED", err.Error()))
		}
		c.Locals("device_id", claims.DeviceID)
		c.Locals("device_identity", claims.DeviceIdentity)
		c.Locals("org_id", claims.OrganizationID)
		return c.Next()
	}
}
