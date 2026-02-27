package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mdm-system/backend/internal/models"
)

type PolicyRepository struct {
	db *pgxpool.Pool
}

func NewPolicyRepository(db *pgxpool.Pool) *PolicyRepository {
	return &PolicyRepository{db: db}
}

func (r *PolicyRepository) Create(ctx context.Context, p *models.Policy) error {
	p.ID = uuid.New()
	p.CreatedAt = time.Now()
	p.UpdatedAt = time.Now()

	query := `
		INSERT INTO policies (
			id, organization_id, name, description, rules, is_default, priority, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err := r.db.Exec(ctx, query,
		p.ID, p.OrganizationID, p.Name, p.Description, p.Rules, p.IsDefault, p.Priority, p.CreatedAt, p.UpdatedAt,
	)
	return err
}

func (r *PolicyRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.Policy, error) {
	query := `SELECT id, organization_id, name, description, rules, is_default, priority, created_at, updated_at FROM policies WHERE id = $1`

	var p models.Policy
	err := r.db.QueryRow(ctx, query, id).Scan(
		&p.ID, &p.OrganizationID, &p.Name, &p.Description, &p.Rules, &p.IsDefault, &p.Priority, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *PolicyRepository) List(ctx context.Context, orgID uuid.UUID) ([]models.Policy, error) {
	query := `SELECT id, organization_id, name, description, rules, is_default, priority, created_at, updated_at FROM policies WHERE organization_id = $1 ORDER BY priority DESC, name ASC`

	rows, err := r.db.Query(ctx, query, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []models.Policy
	for rows.Next() {
		var p models.Policy
		if err := rows.Scan(&p.ID, &p.OrganizationID, &p.Name, &p.Description, &p.Rules, &p.IsDefault, &p.Priority, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		policies = append(policies, p)
	}
	return policies, nil
}

func (r *PolicyRepository) Update(ctx context.Context, p *models.Policy) error {
	p.UpdatedAt = time.Now()
	query := `
		UPDATE policies 
		SET name = $2, description = $3, rules = $4, is_default = $5, priority = $6, updated_at = $7
		WHERE id = $1`

	_, err := r.db.Exec(ctx, query,
		p.ID, p.Name, p.Description, p.Rules, p.IsDefault, p.Priority, p.UpdatedAt,
	)
	return err
}

func (r *PolicyRepository) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM policies WHERE id = $1`, id)
	return err
}

// Group Repository methods (could be separate but keeping here for simplicity for now)

type GroupRepository struct {
	db *pgxpool.Pool
}

func NewGroupRepository(db *pgxpool.Pool) *GroupRepository {
	return &GroupRepository{db: db}
}

func (r *GroupRepository) Create(ctx context.Context, g *models.DeviceGroup) error {
	g.ID = uuid.New()
	g.CreatedAt = time.Now()
	g.UpdatedAt = time.Now()

	query := `
		INSERT INTO device_groups (
			id, organization_id, name, description, parent_group_id, settings, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := r.db.Exec(ctx, query,
		g.ID, g.OrganizationID, g.Name, g.Description, g.ParentGroupID, g.Settings, g.CreatedAt, g.UpdatedAt,
	)
	return err
}

func (r *GroupRepository) List(ctx context.Context, orgID uuid.UUID) ([]models.DeviceGroup, error) {
	query := `
		SELECT g.id, g.organization_id, g.name, g.description, g.parent_group_id, g.settings, g.created_at, g.updated_at,
		(SELECT COUNT(*) FROM devices WHERE group_id = g.id) as device_count
		FROM device_groups g WHERE g.organization_id = $1 ORDER BY g.name ASC`

	rows, err := r.db.Query(ctx, query, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []models.DeviceGroup
	for rows.Next() {
		var g models.DeviceGroup
		if err := rows.Scan(&g.ID, &g.OrganizationID, &g.Name, &g.Description, &g.ParentGroupID, &g.Settings, &g.CreatedAt, &g.UpdatedAt, &g.DeviceCount); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, nil
}
