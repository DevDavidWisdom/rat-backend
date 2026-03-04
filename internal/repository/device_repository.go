package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mdm-system/backend/internal/models"
)

type DeviceRepository struct {
	pool *pgxpool.Pool
}

func NewDeviceRepository(pool *pgxpool.Pool) *DeviceRepository {
	return &DeviceRepository{pool: pool}
}

func (r *DeviceRepository) Create(ctx context.Context, device *models.Device) error {
	query := `
		INSERT INTO devices (
			id, organization_id, group_id, policy_id, serial_number, device_id,
			enrollment_token, device_token, name, model, manufacturer, android_version,
			sdk_version, agent_version, status, metadata, tags, enrolled_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, NOW()
		) RETURNING created_at, updated_at, enrolled_at
	`
	device.ID = uuid.New()

	err := r.pool.QueryRow(ctx, query,
		device.ID,
		device.OrganizationID,
		device.GroupID,
		device.PolicyID,
		device.SerialNumber,
		device.DeviceID,
		device.EnrollmentToken,
		device.DeviceToken,
		device.Name,
		device.Model,
		device.Manufacturer,
		device.AndroidVersion,
		device.SDKVersion,
		device.AgentVersion,
		device.Status,
		device.Metadata,
		device.Tags,
	).Scan(&device.CreatedAt, &device.UpdatedAt, &device.EnrolledAt)

	return err
}

func (r *DeviceRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.Device, error) {
	query := `
		SELECT d.id, d.organization_id, d.group_id, d.policy_id, d.serial_number, 
		       d.device_id, d.device_token, d.name, d.model, d.manufacturer, 
		       d.android_version, d.sdk_version, d.agent_version, d.status, 
		       d.last_seen, d.enrolled_at, d.battery_level, d.storage_total, 
		       d.storage_available, d.memory_total, d.memory_available, d.network_type,
		       d.ip_address, d.latitude, d.longitude, d.metadata, d.tags, 
		       d.created_at, d.updated_at,
		       d.google_emails, d.phone_numbers,
		       d.wifi_ssid, d.wifi_rssi, d.charging_type,
		       d.foreground_app, d.current_url, d.link_speed_mbps,
		       d.issam_id,
		       g.name as group_name, p.name as policy_name,
		       et.name as enrollment_name
		FROM devices d
		LEFT JOIN device_groups g ON d.group_id = g.id
		LEFT JOIN policies p ON d.policy_id = p.id
		LEFT JOIN enrollment_tokens et ON d.enrollment_token = et.token
		WHERE d.id = $1
	`

	var device models.Device
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&device.ID,
		&device.OrganizationID,
		&device.GroupID,
		&device.PolicyID,
		&device.SerialNumber,
		&device.DeviceID,
		&device.DeviceToken,
		&device.Name,
		&device.Model,
		&device.Manufacturer,
		&device.AndroidVersion,
		&device.SDKVersion,
		&device.AgentVersion,
		&device.Status,
		&device.LastSeen,
		&device.EnrolledAt,
		&device.BatteryLevel,
		&device.StorageTotal,
		&device.StorageAvailable,
		&device.MemoryTotal,
		&device.MemoryAvailable,
		&device.NetworkType,
		&device.IPAddress,
		&device.Latitude,
		&device.Longitude,
		&device.Metadata,
		&device.Tags,
		&device.CreatedAt,
		&device.UpdatedAt,
		&device.GoogleEmails,
		&device.PhoneNumbers,
		&device.WifiSsid,
		&device.WifiRssi,
		&device.ChargingType,
		&device.ForegroundApp,
		&device.CurrentUrl,
		&device.LinkSpeedMbps,
		&device.IssamID,
		&device.GroupName,
		&device.PolicyName,
		&device.EnrollmentName,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &device, nil
}

func (r *DeviceRepository) GetIDsByGroupID(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error) {
	query := `SELECT id FROM devices WHERE group_id = $1`
	rows, err := r.pool.Query(ctx, query, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (r *DeviceRepository) GetByDeviceID(ctx context.Context, deviceID string) (*models.Device, error) {
	query := `
		SELECT id, organization_id, group_id, policy_id, serial_number, 
		       device_id, device_token, name, model, manufacturer, 
		       android_version, sdk_version, agent_version, status, 
		       last_seen, enrolled_at, battery_level, storage_total, 
		       storage_available, memory_total, memory_available, network_type,
		       ip_address, latitude, longitude, metadata, tags, 
		       created_at, updated_at
		FROM devices WHERE device_id = $1
	`

	var device models.Device
	err := r.pool.QueryRow(ctx, query, deviceID).Scan(
		&device.ID,
		&device.OrganizationID,
		&device.GroupID,
		&device.PolicyID,
		&device.SerialNumber,
		&device.DeviceID,
		&device.DeviceToken,
		&device.Name,
		&device.Model,
		&device.Manufacturer,
		&device.AndroidVersion,
		&device.SDKVersion,
		&device.AgentVersion,
		&device.Status,
		&device.LastSeen,
		&device.EnrolledAt,
		&device.BatteryLevel,
		&device.StorageTotal,
		&device.StorageAvailable,
		&device.MemoryTotal,
		&device.MemoryAvailable,
		&device.NetworkType,
		&device.IPAddress,
		&device.Latitude,
		&device.Longitude,
		&device.Metadata,
		&device.Tags,
		&device.CreatedAt,
		&device.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &device, nil
}

func (r *DeviceRepository) GetByToken(ctx context.Context, token string) (*models.Device, error) {
	query := `
		SELECT id, organization_id, group_id, policy_id, device_id, status
		FROM devices WHERE device_token = $1
	`

	var device models.Device
	err := r.pool.QueryRow(ctx, query, token).Scan(
		&device.ID,
		&device.OrganizationID,
		&device.GroupID,
		&device.PolicyID,
		&device.DeviceID,
		&device.Status,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &device, nil
}

func (r *DeviceRepository) List(ctx context.Context, filter *models.DeviceFilter) ([]models.Device, int64, error) {
	// Build WHERE clause
	conditions := []string{"1=1"}
	args := []interface{}{}
	argNum := 1

	if filter.OrganizationID != nil {
		conditions = append(conditions, fmt.Sprintf("d.organization_id = $%d", argNum))
		args = append(args, *filter.OrganizationID)
		argNum++
	}

	if filter.GroupID != nil {
		conditions = append(conditions, fmt.Sprintf("d.group_id = $%d", argNum))
		args = append(args, *filter.GroupID)
		argNum++
	}

	if len(filter.Status) > 0 {
		placeholders := make([]string, len(filter.Status))
		for i, status := range filter.Status {
			placeholders[i] = fmt.Sprintf("$%d", argNum)
			args = append(args, status)
			argNum++
		}
		conditions = append(conditions, fmt.Sprintf("d.status IN (%s)", strings.Join(placeholders, ",")))
	}

	if filter.Search != "" {
		searchPattern := "%" + filter.Search + "%"
		conditions = append(conditions, fmt.Sprintf(
			"(d.name ILIKE $%d OR d.device_id ILIKE $%d OR d.serial_number ILIKE $%d OR d.model ILIKE $%d)",
			argNum, argNum, argNum, argNum,
		))
		args = append(args, searchPattern)
		argNum++
	}

	whereClause := strings.Join(conditions, " AND ")

	// Count query
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM devices d WHERE %s", whereClause)
	var total int64
	err := r.pool.QueryRow(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// Set defaults for pagination
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 100 {
		filter.PageSize = 20
	}
	offset := (filter.Page - 1) * filter.PageSize

	// Data query
	query := fmt.Sprintf(`
		SELECT d.id, d.organization_id, d.group_id, d.policy_id, d.serial_number, 
		       d.device_id, d.name, d.model, d.manufacturer, d.android_version, 
		       d.sdk_version, d.agent_version, d.status, d.last_seen, d.enrolled_at,
		       d.battery_level, d.storage_total, d.storage_available, d.network_type,
		       d.ip_address, d.latitude, d.longitude, d.tags, d.created_at, d.updated_at,
		       g.name as group_name, et.name as enrollment_name
		FROM devices d
		LEFT JOIN device_groups g ON d.group_id = g.id
		LEFT JOIN enrollment_tokens et ON d.enrollment_token = et.token
		WHERE %s
		ORDER BY d.last_seen DESC NULLS LAST, d.created_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argNum, argNum+1)
	args = append(args, filter.PageSize, offset)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var devices []models.Device
	for rows.Next() {
		var device models.Device
		err := rows.Scan(
			&device.ID,
			&device.OrganizationID,
			&device.GroupID,
			&device.PolicyID,
			&device.SerialNumber,
			&device.DeviceID,
			&device.Name,
			&device.Model,
			&device.Manufacturer,
			&device.AndroidVersion,
			&device.SDKVersion,
			&device.AgentVersion,
			&device.Status,
			&device.LastSeen,
			&device.EnrolledAt,
			&device.BatteryLevel,
			&device.StorageTotal,
			&device.StorageAvailable,
			&device.NetworkType,
			&device.IPAddress,
			&device.Latitude,
			&device.Longitude,
			&device.Tags,
			&device.CreatedAt,
			&device.UpdatedAt,
			&device.GroupName,
			&device.EnrollmentName,
		)
		if err != nil {
			return nil, 0, err
		}
		devices = append(devices, device)
	}

	return devices, total, nil
}

func (r *DeviceRepository) Update(ctx context.Context, device *models.Device) error {
	query := `
		UPDATE devices SET
			group_id = $2, policy_id = $3, name = $4, tags = $5, updated_at = NOW()
		WHERE id = $1
	`
	_, err := r.pool.Exec(ctx, query,
		device.ID,
		device.GroupID,
		device.PolicyID,
		device.Name,
		device.Tags,
	)
	return err
}

func (r *DeviceRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status models.DeviceStatus) error {
	query := `UPDATE devices SET status = $2, last_seen = NOW(), updated_at = NOW() WHERE id = $1`
	_, err := r.pool.Exec(ctx, query, id, status)
	return err
}

func (r *DeviceRepository) UpdateToken(ctx context.Context, id uuid.UUID, token string) error {
	query := `UPDATE devices SET device_token = $2, updated_at = NOW() WHERE id = $1`
	_, err := r.pool.Exec(ctx, query, id, token)
	return err
}

func (r *DeviceRepository) UpdateTelemetry(ctx context.Context, deviceID string, telemetry *models.TelemetryReport) error {
	query := `
		UPDATE devices SET
			battery_level = COALESCE($2, battery_level),
			storage_total = COALESCE($3, storage_total),
			storage_available = COALESCE($4, storage_available),
			memory_total = COALESCE($5, memory_total),
			memory_available = COALESCE($6, memory_available),
			network_type = COALESCE($7, network_type),
			ip_address = COALESCE($8, ip_address),
			latitude = COALESCE($9, latitude),
			longitude = COALESCE($10, longitude),
			agent_version = COALESCE($11, agent_version),
			wifi_ssid = COALESCE($12, wifi_ssid),
			wifi_rssi = COALESCE($13, wifi_rssi),
			charging_type = COALESCE($14, charging_type),
			foreground_app = COALESCE($15, foreground_app),
			current_url = COALESCE($16, current_url),
			link_speed_mbps = COALESCE($17, link_speed_mbps),
			status = 'online',
			last_seen = NOW(),
			updated_at = NOW()
		WHERE device_id = $1
	`
	_, err := r.pool.Exec(ctx, query,
		deviceID,
		telemetry.BatteryLevel,
		telemetry.StorageTotal,
		telemetry.StorageAvailable,
		telemetry.MemoryTotal,
		telemetry.MemoryAvailable,
		telemetry.NetworkType,
		telemetry.IPAddress,
		telemetry.Latitude,
		telemetry.Longitude,
		telemetry.AgentVersion,
		telemetry.WifiSsid,
		telemetry.WifiRssi,
		telemetry.ChargingType,
		telemetry.ForegroundApp,
		telemetry.CurrentUrl,
		telemetry.LinkSpeedMbps,
	)
	return err
}

func (r *DeviceRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM devices WHERE id = $1`
	result, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *DeviceRepository) GetEnrollmentToken(ctx context.Context, id uuid.UUID) (*string, error) {
	query := `SELECT enrollment_token FROM devices WHERE id = $1`
	var token *string
	err := r.pool.QueryRow(ctx, query, id).Scan(&token)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return token, nil
}

func (r *DeviceRepository) GetStats(ctx context.Context, orgID uuid.UUID) (*models.DashboardStats, error) {
	query := `
		SELECT 
			COUNT(*) as total,
			COUNT(*) FILTER (WHERE status = 'online') as online,
			COUNT(*) FILTER (WHERE status = 'offline') as offline,
			COUNT(*) FILTER (WHERE status = 'pending') as pending
		FROM devices WHERE organization_id = $1
	`

	var stats models.DashboardStats
	err := r.pool.QueryRow(ctx, query, orgID).Scan(
		&stats.TotalDevices,
		&stats.OnlineDevices,
		&stats.OfflineDevices,
		&stats.PendingDevices,
	)
	if err != nil {
		return nil, err
	}

	stats.StatusCounts = map[string]int64{
		"online":  stats.OnlineDevices,
		"offline": stats.OfflineDevices,
		"pending": stats.PendingDevices,
	}

	return &stats, nil
}

func (r *DeviceRepository) MarkOfflineDevices(ctx context.Context, timeout time.Duration) (int64, error) {
	query := `
		UPDATE devices 
		SET status = 'offline', updated_at = NOW()
		WHERE status = 'online' 
		AND last_seen < NOW() - $1::interval
	`
	result, err := r.pool.Exec(ctx, query, timeout)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (r *DeviceRepository) ListIDsByPolicyID(ctx context.Context, policyID uuid.UUID) ([]uuid.UUID, error) {
	query := `
		SELECT id FROM devices WHERE policy_id = $1
		UNION
		SELECT d.id FROM devices d
		JOIN device_groups g ON d.group_id = g.id
		WHERE g.policy_id = $2
	`
	rows, err := r.pool.Query(ctx, query, policyID, policyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (r *DeviceRepository) ListAll(ctx context.Context, orgID uuid.UUID) ([]models.Device, error) {
	query := `
		SELECT d.id, d.organization_id, d.group_id, d.policy_id, d.serial_number, 
		       d.device_id, d.name, d.model, d.manufacturer, d.android_version, 
		       d.sdk_version, d.agent_version, d.status, d.last_seen, d.enrolled_at,
		       d.battery_level, d.storage_total, d.storage_available, d.network_type,
		       d.ip_address, d.latitude, d.longitude, d.tags, d.created_at, d.updated_at
		FROM devices d
		WHERE d.organization_id = $1
		ORDER BY d.last_seen DESC NULLS LAST
	`
	rows, err := r.pool.Query(ctx, query, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []models.Device
	for rows.Next() {
		var d models.Device
		err := rows.Scan(
			&d.ID, &d.OrganizationID, &d.GroupID, &d.PolicyID, &d.SerialNumber,
			&d.DeviceID, &d.Name, &d.Model, &d.Manufacturer, &d.AndroidVersion,
			&d.SDKVersion, &d.AgentVersion, &d.Status, &d.LastSeen, &d.EnrolledAt,
			&d.BatteryLevel, &d.StorageTotal, &d.StorageAvailable, &d.NetworkType,
			&d.IPAddress, &d.Latitude, &d.Longitude, &d.Tags, &d.CreatedAt, &d.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, nil
}

func (r *DeviceRepository) GetIDsByEnrollmentToken(ctx context.Context, token string) ([]uuid.UUID, error) {
	query := `SELECT id FROM devices WHERE enrollment_token = $1`
	rows, err := r.pool.Query(ctx, query, token)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (r *DeviceRepository) GetAllIDs(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	query := `SELECT id FROM devices WHERE organization_id = $1`
	rows, err := r.pool.Query(ctx, query, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetOnlineIDsByGroupID returns IDs of devices in a group that are currently online.
func (r *DeviceRepository) GetOnlineIDsByGroupID(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error) {
	query := `SELECT id FROM devices WHERE group_id = $1 AND status = 'online'`
	rows, err := r.pool.Query(ctx, query, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetOnlineIDs returns IDs of all online devices in an organization.
func (r *DeviceRepository) GetOnlineIDs(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	query := `SELECT id FROM devices WHERE organization_id = $1 AND status = 'online'`
	rows, err := r.pool.Query(ctx, query, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetOnlineIDsByEnrollmentToken returns IDs of online devices enrolled with a specific token.
func (r *DeviceRepository) GetOnlineIDsByEnrollmentToken(ctx context.Context, token string) ([]uuid.UUID, error) {
	query := `SELECT id FROM devices WHERE enrollment_token = $1 AND status = 'online'`
	rows, err := r.pool.Query(ctx, query, token)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// UpdateAccountInfo saves extracted Google emails and phone numbers for a device.
func (r *DeviceRepository) UpdateAccountInfo(ctx context.Context, deviceID uuid.UUID, emails []string, phones []string) error {
	query := `
		UPDATE devices SET
			google_emails = $2,
			phone_numbers = $3,
			updated_at = NOW()
		WHERE id = $1
	`
	_, err := r.pool.Exec(ctx, query, deviceID, emails, phones)
	return err
}

// UpdateIssamID saves the extracted ISSAM agent_id for a device.
func (r *DeviceRepository) UpdateIssamID(ctx context.Context, deviceID uuid.UUID, issamID string) error {
	query := `
		UPDATE devices SET
			issam_id = $2,
			updated_at = NOW()
		WHERE id = $1
	`
	_, err := r.pool.Exec(ctx, query, deviceID, issamID)
	return err
}
