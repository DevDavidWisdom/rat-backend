package services

import (
	"context"

	"github.com/google/uuid"
	"github.com/mdm-system/backend/internal/models"
	"github.com/mdm-system/backend/internal/repository"
)

type AuditService struct {
	auditRepo *repository.AuditRepository
}

func NewAuditService(auditRepo *repository.AuditRepository) *AuditService {
	return &AuditService{auditRepo: auditRepo}
}

func (s *AuditService) Log(ctx context.Context, log *models.AuditLog) error {
	return s.auditRepo.Create(ctx, log)
}

func (s *AuditService) ListLogs(ctx context.Context, orgID uuid.UUID, page, pageSize int) ([]models.AuditLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 50
	}
	offset := (page - 1) * pageSize
	return s.auditRepo.List(ctx, orgID, pageSize, offset)
}
