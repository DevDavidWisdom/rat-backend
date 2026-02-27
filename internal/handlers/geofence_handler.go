package handlers

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mdm-system/backend/internal/models"
)

type GeofenceHandler struct {
	pool *pgxpool.Pool
}

func NewGeofenceHandler(pool *pgxpool.Pool) *GeofenceHandler {
	return &GeofenceHandler{pool: pool}
}

func (h *GeofenceHandler) RegisterRoutes(protected fiber.Router) {
	gf := protected.Group("/geofences")
	gf.Get("/", h.List)
	gf.Post("/", h.Create)
	gf.Delete("/:id", h.Delete)
	gf.Get("/breaches", h.ListBreaches)
}

// ── CRUD ──

type CreateGeofenceRequest struct {
	Name         string                `json:"name"`
	Polygon      []models.PolygonPoint `json:"polygon"` // [{lat, lng}, ...]
	Action       string                `json:"action"`
	GroupID      *uuid.UUID            `json:"group_id"`
	EnrollmentID *uuid.UUID            `json:"enrollment_id"`
}

func (h *GeofenceHandler) Create(c *fiber.Ctx) error {
	var req CreateGeofenceRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid request body"})
	}
	if req.Name == "" {
		return c.Status(400).JSON(fiber.Map{"message": "Name is required"})
	}
	if len(req.Polygon) < 3 {
		return c.Status(400).JSON(fiber.Map{"message": "Polygon must have at least 3 points"})
	}
	if req.Action == "" {
		req.Action = "NOTIFY"
	}

	orgID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	polygonJSON, err := json.Marshal(req.Polygon)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"message": "Failed to encode polygon"})
	}

	var id uuid.UUID
	err = h.pool.QueryRow(c.Context(),
		`INSERT INTO geofences (organization_id, name, polygon, action, group_id, enrollment_id)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id`,
		orgID, req.Name, polygonJSON, req.Action, req.GroupID, req.EnrollmentID,
	).Scan(&id)
	if err != nil {
		log.Printf("Error creating geofence: %v", err)
		return c.Status(500).JSON(fiber.Map{"message": "Failed to create geofence"})
	}

	return c.Status(201).JSON(fiber.Map{
		"data": fiber.Map{
			"id":            id,
			"name":          req.Name,
			"polygon":       req.Polygon,
			"action":        req.Action,
			"group_id":      req.GroupID,
			"enrollment_id": req.EnrollmentID,
		},
	})
}

func (h *GeofenceHandler) List(c *fiber.Ctx) error {
	orgID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	rows, err := h.pool.Query(c.Context(),
		`SELECT g.id, g.name, g.polygon, g.action,
		        g.group_id, g.enrollment_id, g.is_active, g.created_at,
		        dg.name as group_name, et.name as enrollment_name,
		        (SELECT COUNT(*) FROM geofence_breaches b WHERE b.geofence_id = g.id AND b.resolved = false) as active_breaches
		 FROM geofences g
		 LEFT JOIN device_groups dg ON g.group_id = dg.id
		 LEFT JOIN enrollment_tokens et ON g.enrollment_id = et.id
		 WHERE g.organization_id = $1
		 ORDER BY g.created_at DESC`, orgID)
	if err != nil {
		log.Printf("Error listing geofences: %v", err)
		return c.Status(500).JSON(fiber.Map{"message": "Failed to list geofences"})
	}
	defer rows.Close()

	var geofences []fiber.Map
	for rows.Next() {
		var (
			id                        uuid.UUID
			groupID, enrollmentID     *uuid.UUID
			name, action              string
			groupName, enrollmentName *string
			polygonJSON               []byte
			isActive                  bool
			createdAt                 interface{}
			activeBreaches            int
		)
		if err := rows.Scan(&id, &name, &polygonJSON, &action,
			&groupID, &enrollmentID, &isActive, &createdAt,
			&groupName, &enrollmentName, &activeBreaches); err != nil {
			log.Printf("Error scanning geofence row: %v", err)
			continue
		}

		var polygon []models.PolygonPoint
		if polygonJSON != nil {
			_ = json.Unmarshal(polygonJSON, &polygon)
		}

		gf := fiber.Map{
			"id":              id,
			"name":            name,
			"polygon":         polygon,
			"action":          action,
			"group_id":        groupID,
			"enrollment_id":   enrollmentID,
			"is_active":       isActive,
			"created_at":      createdAt,
			"group_name":      groupName,
			"enrollment_name": enrollmentName,
			"active_breaches": activeBreaches,
		}
		geofences = append(geofences, gf)
	}

	if geofences == nil {
		geofences = []fiber.Map{}
	}

	return c.JSON(fiber.Map{"data": fiber.Map{"geofences": geofences}})
}

func (h *GeofenceHandler) Delete(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid geofence ID"})
	}

	_, err = h.pool.Exec(c.Context(), "DELETE FROM geofences WHERE id = $1", id)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"message": "Failed to delete geofence"})
	}

	return c.JSON(fiber.Map{"message": "Geofence deleted"})
}

// ── Breaches ──

func (h *GeofenceHandler) ListBreaches(c *fiber.Ctx) error {
	orgID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	geofenceID := c.Query("geofence_id")
	limitStr := c.Query("limit", "100")

	query := `
		SELECT b.id, b.geofence_id, b.device_id, b.device_latitude, b.device_longitude,
		       b.distance_meters, b.resolved, b.created_at,
		       d.name as device_name, d.model as device_model,
		       dg.name as group_name, et.name as enrollment_name,
		       gf.name as geofence_name
		FROM geofence_breaches b
		JOIN devices d ON b.device_id = d.id
		JOIN geofences gf ON b.geofence_id = gf.id
		LEFT JOIN device_groups dg ON d.group_id = dg.id
		LEFT JOIN enrollment_tokens et ON d.enrollment_token = et.token
		WHERE gf.organization_id = $1
	`
	args := []interface{}{orgID}
	argIdx := 2

	if geofenceID != "" {
		if gfID, err := uuid.Parse(geofenceID); err == nil {
			query += " AND b.geofence_id = $" + itoa(argIdx)
			args = append(args, gfID)
			argIdx++
		}
	}

	query += " ORDER BY b.created_at DESC LIMIT $" + itoa(argIdx)
	args = append(args, limitStr)

	rows, err := h.pool.Query(c.Context(), query, args...)
	if err != nil {
		log.Printf("Error listing breaches: %v", err)
		return c.Status(500).JSON(fiber.Map{"message": "Failed to list breaches"})
	}
	defer rows.Close()

	var breaches []fiber.Map
	for rows.Next() {
		var (
			id, geofenceID, deviceID                                   uuid.UUID
			deviceLat, deviceLng, distanceMeters                       float64
			resolved                                                   bool
			createdAt                                                  interface{}
			deviceName, deviceModel, groupName, enrollmentName, gfName *string
		)
		if err := rows.Scan(&id, &geofenceID, &deviceID, &deviceLat, &deviceLng,
			&distanceMeters, &resolved, &createdAt,
			&deviceName, &deviceModel,
			&groupName, &enrollmentName,
			&gfName); err != nil {
			log.Printf("Error scanning breach row: %v", err)
			continue
		}
		breaches = append(breaches, fiber.Map{
			"id":               id,
			"geofence_id":      geofenceID,
			"device_id":        deviceID,
			"device_latitude":  deviceLat,
			"device_longitude": deviceLng,
			"distance_meters":  distanceMeters,
			"resolved":         resolved,
			"created_at":       createdAt,
			"device_name":      deviceName,
			"device_model":     deviceModel,
			"group_name":       groupName,
			"enrollment_name":  enrollmentName,
			"geofence_name":    gfName,
		})
	}

	if breaches == nil {
		breaches = []fiber.Map{}
	}

	return c.JSON(fiber.Map{"data": fiber.Map{"breaches": breaches}})
}

func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}
