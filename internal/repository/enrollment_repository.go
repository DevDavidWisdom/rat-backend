package repository

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mdm-system/backend/internal/models"
)

type EnrollmentRepository struct {
	pool *pgxpool.Pool
}

func NewEnrollmentRepository(pool *pgxpool.Pool) *EnrollmentRepository {
	return &EnrollmentRepository{pool: pool}
}

func generateToken() (string, error) {
	// Generate 4-digit PIN (0000-9999)
	bytes := make([]byte, 2)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	// Convert to number 0-9999 and format as 4 digits
	pin := int(bytes[0])*256 + int(bytes[1])
	pin = pin % 10000
	return fmt.Sprintf("%04d", pin), nil
}

func (r *EnrollmentRepository) Create(ctx context.Context, enrollment *models.EnrollmentToken) error {
	token, err := generateToken()
	if err != nil {
		return err
	}

	query := `
		INSERT INTO enrollment_tokens (
			id, organization_id, group_id, policy_id, created_by,
			token, name, max_uses, expires_at, is_active
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING created_at
	`

	enrollment.ID = uuid.New()
	enrollment.Token = token
	enrollment.IsActive = true

	err = r.pool.QueryRow(ctx, query,
		enrollment.ID,
		enrollment.OrganizationID,
		enrollment.GroupID,
		enrollment.PolicyID,
		enrollment.CreatedBy,
		enrollment.Token,
		enrollment.Name,
		enrollment.MaxUses,
		enrollment.ExpiresAt,
		enrollment.IsActive,
	).Scan(&enrollment.CreatedAt)

	return err
}

func (r *EnrollmentRepository) GetByToken(ctx context.Context, token string) (*models.EnrollmentToken, error) {
	query := `
		SELECT id, organization_id, group_id, policy_id, created_by,
		       token, name, max_uses, current_uses, expires_at, is_active, created_at
		FROM enrollment_tokens 
		WHERE token = $1
	`

	var enrollment models.EnrollmentToken
	err := r.pool.QueryRow(ctx, query, token).Scan(
		&enrollment.ID,
		&enrollment.OrganizationID,
		&enrollment.GroupID,
		&enrollment.PolicyID,
		&enrollment.CreatedBy,
		&enrollment.Token,
		&enrollment.Name,
		&enrollment.MaxUses,
		&enrollment.CurrentUses,
		&enrollment.ExpiresAt,
		&enrollment.IsActive,
		&enrollment.CreatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &enrollment, nil
}

func (r *EnrollmentRepository) ValidateAndConsume(ctx context.Context, token string) (*models.EnrollmentToken, error) {
	enrollment, err := r.GetByToken(ctx, token)
	if err != nil {
		return nil, err
	}

	// Check if token is active
	if !enrollment.IsActive {
		return nil, errors.New("enrollment token is inactive")
	}

	// Check expiry
	if enrollment.ExpiresAt != nil && enrollment.ExpiresAt.Before(time.Now()) {
		return nil, errors.New("enrollment token has expired")
	}

	// Check max uses
	if enrollment.MaxUses != nil && enrollment.CurrentUses >= *enrollment.MaxUses {
		return nil, errors.New("enrollment token has reached maximum uses")
	}

	// Increment usage
	query := `UPDATE enrollment_tokens SET current_uses = current_uses + 1 WHERE id = $1`
	_, err = r.pool.Exec(ctx, query, enrollment.ID)
	if err != nil {
		return nil, err
	}

	enrollment.CurrentUses++
	return enrollment, nil
}

func (r *EnrollmentRepository) List(ctx context.Context, orgID uuid.UUID) ([]models.EnrollmentToken, error) {
	query := `
		SELECT id, organization_id, group_id, policy_id, created_by,
		       token, name, max_uses, current_uses, expires_at, is_active, created_at
		FROM enrollment_tokens 
		WHERE organization_id = $1
		ORDER BY created_at DESC
	`

	rows, err := r.pool.Query(ctx, query, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []models.EnrollmentToken
	for rows.Next() {
		var enrollment models.EnrollmentToken
		err := rows.Scan(
			&enrollment.ID,
			&enrollment.OrganizationID,
			&enrollment.GroupID,
			&enrollment.PolicyID,
			&enrollment.CreatedBy,
			&enrollment.Token,
			&enrollment.Name,
			&enrollment.MaxUses,
			&enrollment.CurrentUses,
			&enrollment.ExpiresAt,
			&enrollment.IsActive,
			&enrollment.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, enrollment)
	}

	return tokens, nil
}

func (r *EnrollmentRepository) Deactivate(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE enrollment_tokens SET is_active = false WHERE id = $1`
	result, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *EnrollmentRepository) DecrementUses(ctx context.Context, token string) error {
	query := `UPDATE enrollment_tokens SET current_uses = GREATEST(current_uses - 1, 0) WHERE token = $1 AND current_uses > 0`
	_, err := r.pool.Exec(ctx, query, token)
	return err
}

func (r *EnrollmentRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM enrollment_tokens WHERE id = $1`
	result, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
