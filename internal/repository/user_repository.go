package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mdm-system/backend/internal/models"
)

var (
	ErrNotFound     = errors.New("record not found")
	ErrDuplicate    = errors.New("record already exists")
	ErrInvalidInput = errors.New("invalid input")
)

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

func (r *UserRepository) Create(ctx context.Context, user *models.User) error {
	query := `
		INSERT INTO users (id, organization_id, email, password_hash, name, role, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at, updated_at
	`
	user.ID = uuid.New()

	err := r.pool.QueryRow(ctx, query,
		user.ID,
		user.OrganizationID,
		user.Email,
		user.PasswordHash,
		user.Name,
		user.Role,
		user.IsActive,
	).Scan(&user.CreatedAt, &user.UpdatedAt)

	if err != nil {
		return err
	}
	return nil
}

func (r *UserRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	query := `
		SELECT id, organization_id, email, password_hash, name, role, is_active, 
		       last_login, two_factor_enabled, created_at, updated_at
		FROM users WHERE id = $1
	`

	var user models.User
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&user.ID,
		&user.OrganizationID,
		&user.Email,
		&user.PasswordHash,
		&user.Name,
		&user.Role,
		&user.IsActive,
		&user.LastLogin,
		&user.TwoFactorEnabled,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &user, nil
}

func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*models.User, error) {
	query := `
		SELECT id, organization_id, email, password_hash, name, role, is_active, 
		       last_login, two_factor_enabled, created_at, updated_at
		FROM users WHERE email = $1
	`

	var user models.User
	err := r.pool.QueryRow(ctx, query, email).Scan(
		&user.ID,
		&user.OrganizationID,
		&user.Email,
		&user.PasswordHash,
		&user.Name,
		&user.Role,
		&user.IsActive,
		&user.LastLogin,
		&user.TwoFactorEnabled,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &user, nil
}

func (r *UserRepository) UpdateLastLogin(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE users SET last_login = $1 WHERE id = $2`
	_, err := r.pool.Exec(ctx, query, time.Now(), id)
	return err
}

func (r *UserRepository) List(ctx context.Context, orgID uuid.UUID) ([]models.User, error) {
	query := `
		SELECT id, organization_id, email, password_hash, name, role, is_active, 
		       last_login, two_factor_enabled, created_at, updated_at
		FROM users WHERE organization_id = $1
		ORDER BY created_at DESC
	`

	rows, err := r.pool.Query(ctx, query, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		var user models.User
		err := rows.Scan(
			&user.ID,
			&user.OrganizationID,
			&user.Email,
			&user.PasswordHash,
			&user.Name,
			&user.Role,
			&user.IsActive,
			&user.LastLogin,
			&user.TwoFactorEnabled,
			&user.CreatedAt,
			&user.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, nil
}
