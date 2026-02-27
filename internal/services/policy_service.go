package services

import (
	"context"

	"github.com/google/uuid"
	"github.com/mdm-system/backend/internal/models"
	"github.com/mdm-system/backend/internal/repository"
)

type PolicyService struct {
	policyRepo     *repository.PolicyRepository
	groupRepo      *repository.GroupRepository
	deviceRepo     *repository.DeviceRepository
	commandService *CommandService
}

func NewPolicyService(
	policyRepo *repository.PolicyRepository,
	groupRepo *repository.GroupRepository,
	deviceRepo *repository.DeviceRepository,
	commandService *CommandService,
) *PolicyService {
	return &PolicyService{
		policyRepo:     policyRepo,
		groupRepo:      groupRepo,
		deviceRepo:     deviceRepo,
		commandService: commandService,
	}
}

func (s *PolicyService) CreatePolicy(ctx context.Context, p *models.Policy) error {
	return s.policyRepo.Create(ctx, p)
}

func (s *PolicyService) UpdatePolicy(ctx context.Context, p *models.Policy) error {
	if err := s.policyRepo.Update(ctx, p); err != nil {
		return err
	}

	// Logic to push policy updates to all affected devices
	// For a 10k fleet, we find all devices assigned this policy ID
	// This would ideally be a background job, but for now we do it here
	return s.PushPolicyToAffectedDevices(ctx, p.ID)
}

func (s *PolicyService) PushPolicyToAffectedDevices(ctx context.Context, policyID uuid.UUID) error {
	policy, err := s.policyRepo.GetByID(ctx, policyID)
	if err != nil {
		return err
	}

	// Find all devices using this policy (directly or via group)
	deviceIDs, err := s.deviceRepo.ListIDsByPolicyID(ctx, policyID)
	if err != nil {
		return err
	}

	if len(deviceIDs) == 0 {
		return nil
	}

	// Create bulk command to set policy
	_, err = s.commandService.CreateBulkCommands(ctx, &models.BulkCommandRequest{
		DeviceIDs:   deviceIDs,
		CommandType: "SET_POLICY",
		Payload: map[string]interface{}{
			"rules": policy.Rules,
			"name":  policy.Name,
		},
		Priority: 10,
	}, nil)

	return err
}

func (s *PolicyService) CreateGroup(ctx context.Context, g *models.DeviceGroup) error {
	return s.groupRepo.Create(ctx, g)
}

func (s *PolicyService) ListGroups(ctx context.Context, orgID uuid.UUID) ([]models.DeviceGroup, error) {
	return s.groupRepo.List(ctx, orgID)
}

func (s *PolicyService) ListPolicies(ctx context.Context, orgID uuid.UUID) ([]models.Policy, error) {
	return s.policyRepo.List(ctx, orgID)
}
