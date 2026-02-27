package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/mdm-system/backend/internal/models"
	"github.com/mdm-system/backend/internal/services"
)

type PolicyHandler struct {
	policyService *services.PolicyService
}

func NewPolicyHandler(policyService *services.PolicyService) *PolicyHandler {
	return &PolicyHandler{policyService: policyService}
}

func (h *PolicyHandler) ListGroups(c *fiber.Ctx) error {
	orgID := c.Locals("organization_id").(uuid.UUID)
	groups, err := h.policyService.ListGroups(c.Context(), orgID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse("INTERNAL_ERROR", err.Error()))
	}
	return c.JSON(models.SuccessResponse(groups))
}

func (h *PolicyHandler) CreateGroup(c *fiber.Ctx) error {
	orgID := c.Locals("organization_id").(uuid.UUID)
	var g models.DeviceGroup
	if err := c.BodyParser(&g); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse("INVALID_REQUEST", err.Error()))
	}
	g.OrganizationID = orgID
	if err := h.policyService.CreateGroup(c.Context(), &g); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse("INTERNAL_ERROR", err.Error()))
	}
	return c.Status(fiber.StatusCreated).JSON(models.SuccessResponse(g))
}

func (h *PolicyHandler) ListPolicies(c *fiber.Ctx) error {
	orgID := c.Locals("organization_id").(uuid.UUID)
	policies, err := h.policyService.ListPolicies(c.Context(), orgID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse("INTERNAL_ERROR", err.Error()))
	}
	return c.JSON(models.SuccessResponse(policies))
}

func (h *PolicyHandler) CreatePolicy(c *fiber.Ctx) error {
	orgID := c.Locals("organization_id").(uuid.UUID)
	var p models.Policy
	if err := c.BodyParser(&p); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(models.ErrorResponse("INVALID_REQUEST", err.Error()))
	}
	p.OrganizationID = orgID
	if err := h.policyService.CreatePolicy(c.Context(), &p); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(models.ErrorResponse("INTERNAL_ERROR", err.Error()))
	}
	return c.Status(fiber.StatusCreated).JSON(models.SuccessResponse(p))
}
