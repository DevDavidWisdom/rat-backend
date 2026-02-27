package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mdm-system/backend/internal/models"
)

type AuditRepository struct {
	pool *pgxpool.Pool
}

func NewAuditRepository(pool *pgxpool.Pool) *AuditRepository {
	return &AuditRepository{pool: pool}
}

func (r *AuditRepository) Create(ctx context.Context, log *models.AuditLog) error {
	query := `
		INSERT INTO audit_logs (
			id, organization_id, user_id, device_id, action, 
			target_type, target_id, metadata, ip_address, user_agent
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING created_at
	`
	log.ID = uuid.New()
	return r.pool.QueryRow(ctx, query,
		log.ID, log.OrganizationID, log.UserID, log.DeviceID, log.Action,
		log.TargetType, log.TargetID, log.Metadata, log.IPAddress, log.UserAgent,
	).Scan(&log.CreatedAt)
}

func (r *AuditRepository) List(ctx context.Context, orgID uuid.UUID, limit, offset int) ([]models.AuditLog, int64, error) {
	countQuery := `SELECT COUNT(*) FROM audit_logs WHERE organization_id = $1`
	var total int64
	err := r.pool.QueryRow(ctx, countQuery, orgID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	query := `
		SELECT id, organization_id, user_id, device_id, action, 
		       target_type, target_id, metadata, ip_address, user_agent, created_at
		FROM audit_logs
		WHERE organization_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`
	rows, err := r.pool.Query(ctx, query, orgID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var logs []models.AuditLog
	for rows.Next() {
		var l models.AuditLog
		err := rows.Scan(
			&l.ID, &l.OrganizationID, &l.UserID, &l.DeviceID, &l.Action,
			&l.TargetType, &l.TargetID, &l.Metadata, &l.IPAddress, &l.UserAgent, &l.CreatedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		logs = append(logs, l)
	}
	return logs, total, nil
}
