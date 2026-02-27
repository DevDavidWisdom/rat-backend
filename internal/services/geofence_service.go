package services

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mdm-system/backend/internal/models"
)

type GeofenceService struct {
	pool           *pgxpool.Pool
	commandService *CommandService
	auditService   *AuditService
}

func NewGeofenceService(pool *pgxpool.Pool, cmdService *CommandService, auditService *AuditService) *GeofenceService {
	return &GeofenceService{
		pool:           pool,
		commandService: cmdService,
		auditService:   auditService,
	}
}

// pointInPolygon uses the ray-casting algorithm to determine if a point is inside a polygon.
func pointInPolygon(lat, lng float64, polygon []models.PolygonPoint) bool {
	n := len(polygon)
	if n < 3 {
		return false
	}
	inside := false
	j := n - 1
	for i := 0; i < n; i++ {
		yi, xi := polygon[i].Lat, polygon[i].Lng
		yj, xj := polygon[j].Lat, polygon[j].Lng
		if ((yi > lat) != (yj > lat)) &&
			(lng < (xj-xi)*(lat-yi)/(yj-yi)+xi) {
			inside = !inside
		}
		j = i
	}
	return inside
}

func (s *GeofenceService) ProcessLocationUpdate(ctx context.Context, deviceID uuid.UUID, loc models.DeviceLocation) error {
	// 1. Update device location in DB
	query := `INSERT INTO device_locations (device_id, latitude, longitude, accuracy, altitude, speed, bearing, timestamp)
	          VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	          ON CONFLICT (device_id) DO UPDATE SET
	          latitude = EXCLUDED.latitude, longitude = EXCLUDED.longitude, accuracy = EXCLUDED.accuracy,
	          timestamp = EXCLUDED.timestamp`
	_, err := s.pool.Exec(ctx, query, deviceID, loc.Latitude, loc.Longitude, loc.Accuracy, loc.Altitude, loc.Speed, loc.Bearing, loc.Timestamp)
	if err != nil {
		return err
	}

	// 2. Fetch device info (org, group, enrollment)
	var device models.Device
	var enrollmentToken *string
	err = s.pool.QueryRow(ctx, "SELECT organization_id, group_id, enrollment_token FROM devices WHERE id = $1", deviceID).Scan(
		&device.OrganizationID, &device.GroupID, &enrollmentToken)
	if err != nil {
		return err
	}

	// 3. Fetch active geofences for this device's organization
	rows, err := s.pool.Query(ctx,
		`SELECT g.id, g.polygon, g.action, g.name, g.organization_id, g.group_id, g.enrollment_id
		 FROM geofences g
		 WHERE g.organization_id = $1 AND g.is_active = true`, device.OrganizationID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var gf models.Geofence
		var polygonJSON []byte
		if err := rows.Scan(&gf.ID, &polygonJSON, &gf.Action, &gf.Name, &gf.OrganizationID, &gf.GroupID, &gf.EnrollmentID); err != nil {
			continue
		}
		if polygonJSON != nil {
			_ = json.Unmarshal(polygonJSON, &gf.Polygon)
		}
		if len(gf.Polygon) < 3 {
			continue
		}

		// Filter: if geofence is scoped to a group, skip devices not in that group
		if gf.GroupID != nil {
			if device.GroupID == nil || *device.GroupID != *gf.GroupID {
				continue
			}
		}

		// Filter: if geofence is scoped to an enrollment, skip devices not from that enrollment
		if gf.EnrollmentID != nil && enrollmentToken != nil {
			var enrollID *uuid.UUID
			_ = s.pool.QueryRow(ctx, "SELECT id FROM enrollment_tokens WHERE token = $1", *enrollmentToken).Scan(&enrollID)
			if enrollID == nil || *enrollID != *gf.EnrollmentID {
				continue
			}
		}

		if !pointInPolygon(loc.Latitude, loc.Longitude, gf.Polygon) {
			// Device is OUTSIDE the polygon — record breach
			s.recordBreach(ctx, gf.ID, deviceID, loc.Latitude, loc.Longitude, 0)
			s.triggerGeofenceBreach(ctx, deviceID, gf)
		}
	}

	return nil
}

func (s *GeofenceService) recordBreach(ctx context.Context, geofenceID, deviceID uuid.UUID, lat, lng, distance float64) {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO geofence_breaches (geofence_id, device_id, device_latitude, device_longitude, distance_meters)
		 VALUES ($1, $2, $3, $4, $5)`,
		geofenceID, deviceID, lat, lng, distance)
	if err != nil {
		// Log but don't fail the location update
		_ = err
	}
}

func (s *GeofenceService) triggerGeofenceBreach(ctx context.Context, deviceID uuid.UUID, gf models.Geofence) {
	// 1. Log breach in audit logs
	s.auditService.Log(ctx, &models.AuditLog{
		OrganizationID: gf.OrganizationID,
		DeviceID:       &deviceID,
		Action:         "GEOFENCE_BREACH",
		TargetType:     "GEOFENCE",
		TargetID:       gf.ID.String(),
		Metadata: map[string]interface{}{
			"geofence_name": gf.Name,
			"action_taken":  gf.Action,
		},
	})

	// 2. Trigger automated action
	switch gf.Action {
	case "LOCK":
		s.commandService.CreateCommand(ctx, deviceID, &models.CreateCommandRequest{
			CommandType: "DEVICE_LOCK",
			Payload: map[string]interface{}{
				"reason": "Geofence breach: " + gf.Name,
			},
		}, nil)
	case "WIPE":
		s.commandService.CreateCommand(ctx, deviceID, &models.CreateCommandRequest{
			CommandType: "DEVICE_WIPE",
			Payload: map[string]interface{}{
				"reason": "Geofence breach: " + gf.Name,
			},
		}, nil)
	case "NOTIFY":
		// Notification logic would go here (e.g. push notification, email)
	}
}
