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

type CommandRepository struct {
	pool *pgxpool.Pool
}

func NewCommandRepository(pool *pgxpool.Pool) *CommandRepository {
	return &CommandRepository{pool: pool}
}

func (r *CommandRepository) Create(ctx context.Context, cmd *models.Command) error {
	query := `
		INSERT INTO commands (
			id, device_id, issued_by, command_type, payload, status, 
			priority, timeout_seconds, max_retries
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING created_at
	`
	cmd.ID = uuid.New()
	if cmd.Status == "" {
		cmd.Status = models.CommandStatusPending
	}
	if cmd.TimeoutSeconds == 0 {
		cmd.TimeoutSeconds = 300
	}
	if cmd.MaxRetries == 0 {
		cmd.MaxRetries = 3
	}

	err := r.pool.QueryRow(ctx, query,
		cmd.ID,
		cmd.DeviceID,
		cmd.IssuedBy,
		cmd.CommandType,
		cmd.Payload,
		cmd.Status,
		cmd.Priority,
		cmd.TimeoutSeconds,
		cmd.MaxRetries,
	).Scan(&cmd.CreatedAt)

	return err
}

func (r *CommandRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.Command, error) {
	query := `
		SELECT id, device_id, issued_by, command_type, payload, status, priority,
		       created_at, queued_at, delivered_at, executed_at, completed_at,
		       timeout_seconds, result, error_message, retry_count, max_retries
		FROM commands WHERE id = $1
	`

	var cmd models.Command
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&cmd.ID,
		&cmd.DeviceID,
		&cmd.IssuedBy,
		&cmd.CommandType,
		&cmd.Payload,
		&cmd.Status,
		&cmd.Priority,
		&cmd.CreatedAt,
		&cmd.QueuedAt,
		&cmd.DeliveredAt,
		&cmd.ExecutedAt,
		&cmd.CompletedAt,
		&cmd.TimeoutSeconds,
		&cmd.Result,
		&cmd.ErrorMessage,
		&cmd.RetryCount,
		&cmd.MaxRetries,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &cmd, nil
}

func (r *CommandRepository) ListByDevice(ctx context.Context, deviceID uuid.UUID, limit int) ([]models.Command, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT id, device_id, issued_by, command_type, payload, status, priority,
		       created_at, queued_at, delivered_at, executed_at, completed_at,
		       timeout_seconds, result, error_message, retry_count, max_retries
		FROM commands 
		WHERE device_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`

	rows, err := r.pool.Query(ctx, query, deviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var commands []models.Command
	for rows.Next() {
		var cmd models.Command
		err := rows.Scan(
			&cmd.ID,
			&cmd.DeviceID,
			&cmd.IssuedBy,
			&cmd.CommandType,
			&cmd.Payload,
			&cmd.Status,
			&cmd.Priority,
			&cmd.CreatedAt,
			&cmd.QueuedAt,
			&cmd.DeliveredAt,
			&cmd.ExecutedAt,
			&cmd.CompletedAt,
			&cmd.TimeoutSeconds,
			&cmd.Result,
			&cmd.ErrorMessage,
			&cmd.RetryCount,
			&cmd.MaxRetries,
		)
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
	}

	return commands, nil
}

func (r *CommandRepository) GetPendingByDevice(ctx context.Context, deviceID uuid.UUID) ([]models.Command, error) {
	query := `
		SELECT id, device_id, issued_by, command_type, payload, status, priority,
		       created_at, timeout_seconds
		FROM commands 
		WHERE device_id = $1 AND status IN ('pending', 'queued', 'delivered')
		ORDER BY priority DESC, created_at ASC
	`

	rows, err := r.pool.Query(ctx, query, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var commands []models.Command
	for rows.Next() {
		var cmd models.Command
		err := rows.Scan(
			&cmd.ID,
			&cmd.DeviceID,
			&cmd.IssuedBy,
			&cmd.CommandType,
			&cmd.Payload,
			&cmd.Status,
			&cmd.Priority,
			&cmd.CreatedAt,
			&cmd.TimeoutSeconds,
		)
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
	}

	return commands, nil
}

func (r *CommandRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status models.CommandStatus, result map[string]interface{}, errMsg *string) error {
	now := time.Now()

	query := `
		UPDATE commands SET
			status = $2,
			result = COALESCE($3, result),
			error_message = COALESCE($4, error_message),
	`

	switch status {
	case models.CommandStatusQueued:
		query += "queued_at = $5"
	case models.CommandStatusDelivered:
		query += "delivered_at = $5"
	case models.CommandStatusExecuting:
		query += "executed_at = $5"
	case models.CommandStatusCompleted, models.CommandStatusFailed, models.CommandStatusTimeout:
		query += "completed_at = $5"
	default:
		query += "updated_at = $5"
	}

	query += " WHERE id = $1"

	_, err := r.pool.Exec(ctx, query, id, status, result, errMsg, now)
	return err
}

func (r *CommandRepository) IncrementRetry(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE commands SET retry_count = retry_count + 1 WHERE id = $1`
	_, err := r.pool.Exec(ctx, query, id)
	return err
}

func (r *CommandRepository) GetTimedOut(ctx context.Context) ([]models.Command, error) {
	query := `
		SELECT id, device_id, command_type, status, created_at, timeout_seconds
		FROM commands 
		WHERE status IN ('pending', 'queued', 'delivered', 'executing')
		AND created_at + (timeout_seconds || ' seconds')::interval < NOW()
	`

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var commands []models.Command
	for rows.Next() {
		var cmd models.Command
		err := rows.Scan(
			&cmd.ID,
			&cmd.DeviceID,
			&cmd.CommandType,
			&cmd.Status,
			&cmd.CreatedAt,
			&cmd.TimeoutSeconds,
		)
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
	}

	return commands, nil
}
