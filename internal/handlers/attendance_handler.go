package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mdm-system/backend/internal/models"
	"github.com/mdm-system/backend/internal/services"
)

// WiFi fingerprint types for hybrid attendance
type WifiScan struct {
	BSSID     string  `json:"bssid"`
	SSID      string  `json:"ssid"`
	RSSI      float64 `json:"rssi"`
	Frequency int     `json:"frequency"`
}

type WifiFingerprint struct {
	PointIndex int        `json:"point_index"`
	Scans      []WifiScan `json:"scans"`
	CapturedAt time.Time  `json:"captured_at"`
	DeviceID   string     `json:"device_id"`
}

// AttendanceHandler handles attendance zones, sessions, and device responses
type AttendanceHandler struct {
	pool           *pgxpool.Pool
	commandService *services.CommandService
	// Active sessions waiting for device responses
	activeSessions sync.Map // sessionID -> *activeSession
	// Track calibration commands: commandID -> zoneID
	calibrateCommands sync.Map
}

type activeSession struct {
	mu               sync.Mutex
	sessionID        uuid.UUID
	zoneID           uuid.UUID
	polygon          [][]float64 // buffered polygon
	wifiFingerprints []WifiFingerprint
	wifiThreshold    float64
	centerLat        float64
	centerLng        float64
	totalDevices     int
	respondedCount   int
	presentCount     int
	absentCount      int
	offlineCount     int
	uncertainCount   int
	startTime        time.Time
	timeoutSeconds   int
	deviceCommandMap map[uuid.UUID]uuid.UUID // deviceID -> commandID
	completed        bool
	// For retakes: previous status per device so timeout restores instead of marking offline
	retakePrevStatus map[uuid.UUID]string
}

func NewAttendanceHandler(pool *pgxpool.Pool, commandService *services.CommandService) *AttendanceHandler {
	return &AttendanceHandler{
		pool:           pool,
		commandService: commandService,
	}
}

func (h *AttendanceHandler) RegisterRoutes(protected fiber.Router, deviceAPI fiber.Router) {
	// Admin routes (behind auth middleware)
	attendance := protected.Group("/attendance")
	attendance.Post("/zones", h.CreateZone)
	attendance.Get("/zones", h.ListZones)
	attendance.Get("/zones/:id", h.GetZone)
	attendance.Put("/zones/:id", h.UpdateZone)
	attendance.Delete("/zones/:id", h.DeleteZone)
	attendance.Post("/zones/:id/take", h.TakeAttendance)
	attendance.Post("/zones/:id/calibrate-wifi", h.CalibrateWiFi)
	attendance.Get("/zones/:id/fingerprints", h.GetFingerprints)
	attendance.Delete("/zones/:id/fingerprints/:index", h.DeleteFingerprint)
	attendance.Get("/sessions/:id", h.GetSession)
	attendance.Get("/sessions/:id/records", h.GetSessionRecords)
	attendance.Post("/sessions/:id/complete", h.CompleteSession)
	attendance.Post("/sessions/:id/retake", h.RetakeAttendance)

	// Device-facing route (behind device auth)
	deviceAPI.Post("/attendance/respond", h.DeviceRespond)
}

// ── Zone CRUD ──

type CreateZoneRequest struct {
	Name         string      `json:"name"`
	GroupID      *uuid.UUID  `json:"group_id"`
	EnrollmentID *uuid.UUID  `json:"enrollment_id"`
	Polygon      [][]float64 `json:"polygon"` // [[lat,lng], ...]
	BufferMeters int         `json:"buffer_meters"`
}

func (h *AttendanceHandler) CreateZone(c *fiber.Ctx) error {
	var req CreateZoneRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid request body"})
	}

	if req.Name == "" {
		return c.Status(400).JSON(fiber.Map{"message": "Name is required"})
	}
	if len(req.Polygon) < 3 {
		return c.Status(400).JSON(fiber.Map{"message": "At least 3 corners required"})
	}
	if req.BufferMeters <= 0 {
		req.BufferMeters = 30
	}

	// Compute center
	centerLat, centerLng := computeCenter(req.Polygon)

	// Compute buffered polygon
	buffered := expandPolygon(req.Polygon, float64(req.BufferMeters))

	polygonJSON, _ := json.Marshal(req.Polygon)
	bufferedJSON, _ := json.Marshal(buffered)

	orgID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	var zoneID uuid.UUID
	err := h.pool.QueryRow(c.Context(),
		`INSERT INTO attendance_zones (organization_id, group_id, enrollment_id, name, polygon, buffered_polygon, buffer_meters, center_lat, center_lng)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		orgID, req.GroupID, req.EnrollmentID, req.Name, polygonJSON, bufferedJSON, req.BufferMeters, centerLat, centerLng,
	).Scan(&zoneID)
	if err != nil {
		log.Printf("Error creating zone: %v", err)
		return c.Status(500).JSON(fiber.Map{"message": "Failed to create zone"})
	}

	return c.Status(201).JSON(fiber.Map{
		"data": fiber.Map{
			"id":               zoneID,
			"name":             req.Name,
			"polygon":          req.Polygon,
			"buffered_polygon": buffered,
			"buffer_meters":    req.BufferMeters,
			"center_lat":       centerLat,
			"center_lng":       centerLng,
			"enrollment_id":    req.EnrollmentID,
		},
	})
}

func (h *AttendanceHandler) ListZones(c *fiber.Ctx) error {
	orgID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	rows, err := h.pool.Query(c.Context(),
		`SELECT z.id, z.name, z.polygon, z.buffered_polygon, z.buffer_meters, z.center_lat, z.center_lng, z.is_active, z.group_id, z.enrollment_id, z.created_at,
		        g.name as group_name, et.name as enrollment_name, z.wifi_fingerprints, z.wifi_match_threshold
		 FROM attendance_zones z
		 LEFT JOIN device_groups g ON z.group_id = g.id
		 LEFT JOIN enrollment_tokens et ON z.enrollment_id = et.id
		 WHERE z.organization_id = $1
		 ORDER BY z.created_at DESC`, orgID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"message": "Failed to list zones"})
	}
	defer rows.Close()

	var zones []fiber.Map
	for rows.Next() {
		var id uuid.UUID
		var name string
		var polygon, bufferedPolygon json.RawMessage
		var bufferMeters int
		var centerLat, centerLng *float64
		var isActive bool
		var groupID *uuid.UUID
		var enrollmentID *uuid.UUID
		var createdAt time.Time
		var groupName *string
		var enrollmentName *string
		var wifiFingerprints json.RawMessage
		var wifiMatchThreshold *float64

		if err := rows.Scan(&id, &name, &polygon, &bufferedPolygon, &bufferMeters, &centerLat, &centerLng, &isActive, &groupID, &enrollmentID, &createdAt, &groupName, &enrollmentName, &wifiFingerprints, &wifiMatchThreshold); err != nil {
			continue
		}

		zone := fiber.Map{
			"id":                   id,
			"name":                 name,
			"polygon":              json.RawMessage(polygon),
			"buffered_polygon":     json.RawMessage(bufferedPolygon),
			"buffer_meters":        bufferMeters,
			"center_lat":           centerLat,
			"center_lng":           centerLng,
			"is_active":            isActive,
			"group_id":             groupID,
			"enrollment_id":        enrollmentID,
			"group_name":           groupName,
			"enrollment_name":      enrollmentName,
			"created_at":           createdAt,
			"wifi_fingerprints":    json.RawMessage(wifiFingerprints),
			"wifi_match_threshold": wifiMatchThreshold,
		}
		zones = append(zones, zone)
	}

	if zones == nil {
		zones = []fiber.Map{}
	}

	return c.JSON(fiber.Map{"data": zones})
}

func (h *AttendanceHandler) GetZone(c *fiber.Ctx) error {
	zoneID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid zone ID"})
	}

	var id uuid.UUID
	var name string
	var polygon, bufferedPolygon json.RawMessage
	var bufferMeters int
	var centerLat, centerLng *float64
	var isActive bool
	var groupID *uuid.UUID
	var enrollmentID *uuid.UUID
	var createdAt time.Time
	var wifiFingerprints json.RawMessage
	var wifiMatchThreshold *float64

	err = h.pool.QueryRow(c.Context(),
		`SELECT id, name, polygon, buffered_polygon, buffer_meters, center_lat, center_lng, is_active, group_id, enrollment_id, created_at, wifi_fingerprints, wifi_match_threshold
		 FROM attendance_zones WHERE id = $1`, zoneID,
	).Scan(&id, &name, &polygon, &bufferedPolygon, &bufferMeters, &centerLat, &centerLng, &isActive, &groupID, &enrollmentID, &createdAt, &wifiFingerprints, &wifiMatchThreshold)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"message": "Zone not found"})
	}

	return c.JSON(fiber.Map{
		"data": fiber.Map{
			"id":                   id,
			"name":                 name,
			"polygon":              json.RawMessage(polygon),
			"buffered_polygon":     json.RawMessage(bufferedPolygon),
			"buffer_meters":        bufferMeters,
			"center_lat":           centerLat,
			"center_lng":           centerLng,
			"is_active":            isActive,
			"group_id":             groupID,
			"enrollment_id":        enrollmentID,
			"created_at":           createdAt,
			"wifi_fingerprints":    json.RawMessage(wifiFingerprints),
			"wifi_match_threshold": wifiMatchThreshold,
		},
	})
}

func (h *AttendanceHandler) UpdateZone(c *fiber.Ctx) error {
	zoneID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid zone ID"})
	}

	var req CreateZoneRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid request body"})
	}

	if req.BufferMeters <= 0 {
		req.BufferMeters = 30
	}

	centerLat, centerLng := computeCenter(req.Polygon)
	buffered := expandPolygon(req.Polygon, float64(req.BufferMeters))
	polygonJSON, _ := json.Marshal(req.Polygon)
	bufferedJSON, _ := json.Marshal(buffered)

	_, err = h.pool.Exec(c.Context(),
		`UPDATE attendance_zones SET name=$1, group_id=$2, enrollment_id=$3, polygon=$4, buffered_polygon=$5, buffer_meters=$6, center_lat=$7, center_lng=$8 WHERE id=$9`,
		req.Name, req.GroupID, req.EnrollmentID, polygonJSON, bufferedJSON, req.BufferMeters, centerLat, centerLng, zoneID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"message": "Failed to update zone"})
	}

	return c.JSON(fiber.Map{"data": fiber.Map{"id": zoneID, "updated": true}})
}

func (h *AttendanceHandler) DeleteZone(c *fiber.Ctx) error {
	zoneID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid zone ID"})
	}

	_, err = h.pool.Exec(c.Context(), "DELETE FROM attendance_zones WHERE id = $1", zoneID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"message": "Failed to delete zone"})
	}

	return c.JSON(fiber.Map{"data": fiber.Map{"deleted": true}})
}

// ── Take Attendance ──

type TakeAttendanceRequest struct {
	TimeoutSeconds int        `json:"timeout_seconds"`
	GroupID        *uuid.UUID `json:"group_id"`
	EnrollmentID   *uuid.UUID `json:"enrollment_id"`
}

func (h *AttendanceHandler) TakeAttendance(c *fiber.Ctx) error {
	zoneID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid zone ID"})
	}

	var req TakeAttendanceRequest
	_ = c.BodyParser(&req)
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 45 // generous default for slow networks
	}

	// Load zone
	var zoneGroupID *uuid.UUID
	var zoneEnrollmentID *uuid.UUID
	var bufferedPolygonJSON json.RawMessage
	var wifiFingerprintsJSON json.RawMessage
	var wifiThreshold *float64
	var centerLat, centerLng *float64
	err = h.pool.QueryRow(c.Context(),
		"SELECT group_id, enrollment_id, buffered_polygon, wifi_fingerprints, wifi_match_threshold, center_lat, center_lng FROM attendance_zones WHERE id = $1 AND is_active = true", zoneID,
	).Scan(&zoneGroupID, &zoneEnrollmentID, &bufferedPolygonJSON, &wifiFingerprintsJSON, &wifiThreshold, &centerLat, &centerLng)

	// Request overrides take priority over zone defaults
	groupID := zoneGroupID
	if req.GroupID != nil {
		groupID = req.GroupID
	}
	enrollmentID := zoneEnrollmentID
	if req.EnrollmentID != nil {
		enrollmentID = req.EnrollmentID
	}
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"message": "Zone not found or inactive"})
	}

	var bufferedPolygon [][]float64
	if err := json.Unmarshal(bufferedPolygonJSON, &bufferedPolygon); err != nil {
		return c.Status(500).JSON(fiber.Map{"message": "Invalid zone polygon data"})
	}

	// Parse WiFi fingerprints
	var wifiFingerprints []WifiFingerprint
	if wifiFingerprintsJSON != nil {
		_ = json.Unmarshal(wifiFingerprintsJSON, &wifiFingerprints)
	}
	threshold := 0.6
	if wifiThreshold != nil {
		threshold = *wifiThreshold
	}
	cLat := 0.0
	cLng := 0.0
	if centerLat != nil {
		cLat = *centerLat
	}
	if centerLng != nil {
		cLng = *centerLng
	}

	// Get devices for this group (or all if no group).
	// Query status so we can immediately mark offline devices without wasting time.
	orgID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	type deviceEntry struct {
		ID     uuid.UUID
		Status string
	}
	var allDevices []deviceEntry

	if enrollmentID != nil {
		// Filter by enrollment token
		rows, err := h.pool.Query(c.Context(),
			`SELECT d.id, d.status FROM devices d
			 JOIN enrollment_tokens et ON d.enrollment_token = et.token
			 WHERE d.organization_id = $1 AND et.id = $2 AND d.status != 'disabled'`, orgID, *enrollmentID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"message": "Failed to query devices"})
		}
		defer rows.Close()
		for rows.Next() {
			var d deviceEntry
			if err := rows.Scan(&d.ID, &d.Status); err == nil {
				allDevices = append(allDevices, d)
			}
		}
	} else if groupID != nil {
		rows, err := h.pool.Query(c.Context(),
			"SELECT id, status FROM devices WHERE organization_id = $1 AND group_id = $2 AND status != 'disabled'", orgID, *groupID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"message": "Failed to query devices"})
		}
		defer rows.Close()
		for rows.Next() {
			var d deviceEntry
			if err := rows.Scan(&d.ID, &d.Status); err == nil {
				allDevices = append(allDevices, d)
			}
		}
	} else {
		rows, err := h.pool.Query(c.Context(),
			"SELECT id, status FROM devices WHERE organization_id = $1 AND status != 'disabled'", orgID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"message": "Failed to query devices"})
		}
		defer rows.Close()
		for rows.Next() {
			var d deviceEntry
			if err := rows.Scan(&d.ID, &d.Status); err == nil {
				allDevices = append(allDevices, d)
			}
		}
	}

	if len(allDevices) == 0 {
		return c.Status(400).JSON(fiber.Map{"message": "No devices found for this zone's group"})
	}

	// Split into online vs offline — offline devices are marked immediately, no waiting
	var onlineDeviceIDs []uuid.UUID
	var offlineDeviceIDs []uuid.UUID
	for _, d := range allDevices {
		if d.Status == "online" {
			onlineDeviceIDs = append(onlineDeviceIDs, d.ID)
		} else {
			offlineDeviceIDs = append(offlineDeviceIDs, d.ID)
		}
	}

	// Create session
	var sessionID uuid.UUID
	err = h.pool.QueryRow(c.Context(),
		`INSERT INTO attendance_sessions (zone_id, organization_id, total_devices, timeout_seconds)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		zoneID, orgID, len(allDevices), req.TimeoutSeconds,
	).Scan(&sessionID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"message": "Failed to create session"})
	}

	// Create records: "pending" for online devices, "offline" immediately for offline ones
	for _, did := range onlineDeviceIDs {
		_, _ = h.pool.Exec(c.Context(),
			`INSERT INTO attendance_records (session_id, device_id, status) VALUES ($1, $2, 'pending')
			 ON CONFLICT (session_id, device_id) DO NOTHING`,
			sessionID, did)
	}
	for _, did := range offlineDeviceIDs {
		_, _ = h.pool.Exec(c.Context(),
			`INSERT INTO attendance_records (session_id, device_id, status) VALUES ($1, $2, 'offline')
			 ON CONFLICT (session_id, device_id) DO NOTHING`,
			sessionID, did)
	}

	if len(offlineDeviceIDs) > 0 {
		log.Printf("Attendance session %s: %d device(s) marked offline immediately (not online)", sessionID, len(offlineDeviceIDs))
	}

	// Update counts now so offline count is visible right away
	h.updateSessionCounts(c.Context(), sessionID)

	// Track active session — totalDevices only counts online devices that we actually need to wait for
	as := &activeSession{
		sessionID:        sessionID,
		zoneID:           zoneID,
		polygon:          bufferedPolygon,
		wifiFingerprints: wifiFingerprints,
		wifiThreshold:    threshold,
		centerLat:        cLat,
		centerLng:        cLng,
		totalDevices:     len(onlineDeviceIDs),
		startTime:        time.Now(),
		timeoutSeconds:   req.TimeoutSeconds,
		deviceCommandMap: make(map[uuid.UUID]uuid.UUID),
	}
	h.activeSessions.Store(sessionID.String(), as)

	// If no online devices at all, complete the session immediately
	if len(onlineDeviceIDs) == 0 {
		log.Printf("Attendance session %s: no online devices — completing immediately", sessionID)
		as.mu.Lock()
		as.completed = true
		as.mu.Unlock()
		h.completeSession(c.Context(), sessionID)
		h.activeSessions.Delete(sessionID.String())

		return c.Status(201).JSON(fiber.Map{
			"data": fiber.Map{
				"session_id":      sessionID,
				"zone_id":         zoneID,
				"total_devices":   len(allDevices),
				"online_devices":  0,
				"offline_devices": len(offlineDeviceIDs),
				"status":          "completed",
				"timeout":         req.TimeoutSeconds,
			},
		})
	}

	// Fan out GET_ATTENDANCE commands only to ONLINE devices
	for _, did := range onlineDeviceIDs {
		cmd, err := h.commandService.CreateCommand(c.Context(), did, &models.CreateCommandRequest{
			CommandType: "GET_ATTENDANCE",
			Payload: map[string]interface{}{
				"session_id": sessionID.String(),
			},
			Priority:       10,
			TimeoutSeconds: req.TimeoutSeconds + 15, // extra buffer for slow networks
		}, nil)
		if err != nil {
			log.Printf("Failed to send GET_ATTENDANCE to device %s: %v", did, err)
			// Mark as offline immediately
			h.markDeviceResult(c.Context(), sessionID, did, "offline", nil)
			continue
		}
		as.mu.Lock()
		as.deviceCommandMap[did] = cmd.ID
		as.mu.Unlock()
	}

	// Start timeout goroutine — marks remaining pending devices as offline
	go h.sessionTimeout(sessionID, time.Duration(req.TimeoutSeconds)*time.Second)

	return c.Status(201).JSON(fiber.Map{
		"data": fiber.Map{
			"session_id":      sessionID,
			"zone_id":         zoneID,
			"total_devices":   len(allDevices),
			"online_devices":  len(onlineDeviceIDs),
			"offline_devices": len(offlineDeviceIDs),
			"status":          "in_progress",
			"timeout":         req.TimeoutSeconds,
		},
	})
}

func (h *AttendanceHandler) sessionTimeout(sessionID uuid.UUID, timeout time.Duration) {
	time.Sleep(timeout + 5*time.Second) // 5s grace period

	val, ok := h.activeSessions.Load(sessionID.String())
	if !ok {
		return
	}
	as := val.(*activeSession)
	as.mu.Lock()
	defer as.mu.Unlock()

	if as.completed {
		return
	}

	ctx := context.Background()

	if as.retakePrevStatus != nil {
		// Retake timeout: restore previous status for devices that didn't respond
		// This ensures retake can only IMPROVE data, never lose it
		for deviceID, prevStatus := range as.retakePrevStatus {
			result, err := h.pool.Exec(ctx,
				`UPDATE attendance_records SET status = $1
				 WHERE session_id = $2 AND device_id = $3 AND status = 'pending'`,
				prevStatus, sessionID, deviceID)
			if err != nil {
				log.Printf("Retake timeout: error restoring status for %s: %v", deviceID, err)
			} else if result.RowsAffected() > 0 {
				log.Printf("Retake timeout: restored device %s to previous status '%s'", deviceID, prevStatus)
			}
		}
	} else {
		// Original take timeout: mark remaining pending records as offline
		_, err := h.pool.Exec(ctx,
			`UPDATE attendance_records SET status = 'offline' WHERE session_id = $1 AND status = 'pending'`,
			sessionID)
		if err != nil {
			log.Printf("Error marking timeout devices offline: %v", err)
		}
	}

	// Count and update session
	h.updateSessionCounts(ctx, sessionID)
	as.completed = true
	h.activeSessions.Delete(sessionID.String())
}

// ── Device Response ──

type DeviceAttendanceResponse struct {
	SessionID      string                   `json:"session_id"`
	DeviceID       string                   `json:"device_id"`
	Latitude       *float64                 `json:"latitude"`
	Longitude      *float64                 `json:"longitude"`
	GPSAccuracy    *float64                 `json:"gps_accuracy"`
	WiFiScan       []map[string]interface{} `json:"wifi_scan"`
	BatteryLevel   *int                     `json:"battery_level"`
	ConnectionType string                   `json:"connection_type"`
}

func (h *AttendanceHandler) DeviceRespond(c *fiber.Ctx) error {
	var resp DeviceAttendanceResponse
	if err := c.BodyParser(&resp); err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid body"})
	}

	sessionID, err := uuid.Parse(resp.SessionID)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid session_id"})
	}
	deviceID, err := uuid.Parse(resp.DeviceID)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid device_id"})
	}

	h.processDeviceResponse(c.Context(), sessionID, deviceID, &resp)

	return c.JSON(fiber.Map{"data": fiber.Map{"received": true}})
}

func (h *AttendanceHandler) processDeviceResponse(ctx context.Context, sessionID uuid.UUID, deviceID uuid.UUID, resp *DeviceAttendanceResponse) {
	val, ok := h.activeSessions.Load(sessionID.String())
	if !ok {
		// Session might have expired, still record the response
		h.markDeviceResult(ctx, sessionID, deviceID, "offline", resp)
		return
	}
	as := val.(*activeSession)

	// Calculate response time
	responseTimeMs := int(time.Since(as.startTime).Milliseconds())

	// ── Hybrid Two-Layer Attendance Check ──
	// Layer 1: GPS coarse check (are you near the building?)
	// Layer 2: WiFi fingerprint match (are you in the right room/section?)

	status := "uncertain" // default: device responded but can't determine
	wifiScore := 0.0

	hasGPS := resp.Latitude != nil && resp.Longitude != nil
	hasWifiFingerprints := len(as.wifiFingerprints) > 0

	if hasWifiFingerprints {
		// ── HYBRID MODE: GPS coarse + WiFi fine ──
		gpsNear := false
		gpsFar := false

		if hasGPS {
			accuracy := 999.0
			if resp.GPSAccuracy != nil {
				accuracy = *resp.GPSAccuracy
			}

			// Coarse GPS: within 200m of zone center (generous for GPS inaccuracy)
			distToCenter := haversineDistance(*resp.Latitude, *resp.Longitude, as.centerLat, as.centerLng)
			gpsCoarseRadius := 200.0 + accuracy // account for GPS error
			if distToCenter <= gpsCoarseRadius {
				gpsNear = true
			} else {
				gpsFar = true
			}
		}

		// WiFi fingerprint matching
		if len(resp.WiFiScan) > 0 {
			// Convert device scan to WifiScan slice
			var deviceScans []WifiScan
			for _, item := range resp.WiFiScan {
				scan := WifiScan{}
				if b, ok := item["bssid"].(string); ok {
					scan.BSSID = b
				}
				if s, ok := item["ssid"].(string); ok {
					scan.SSID = s
				}
				if r, ok := item["rssi"].(float64); ok {
					scan.RSSI = r
				}
				if f, ok := item["frequency"].(float64); ok {
					scan.Frequency = int(f)
				}
				if scan.BSSID != "" {
					deviceScans = append(deviceScans, scan)
				}
			}

			// Compare against all calibration fingerprints, take best match
			bestScore := 0.0
			for _, fp := range as.wifiFingerprints {
				score := wifiCosineSimilarity(fp.Scans, deviceScans)
				if score > bestScore {
					bestScore = score
				}
			}
			wifiScore = bestScore
		}

		wifiMatch := wifiScore >= as.wifiThreshold

		// Decision matrix:
		// GPS near + WiFi match → PRESENT (confirmed at location)
		// GPS near + WiFi no match → UNCERTAIN (right area, maybe wrong room)
		// GPS far + any WiFi → ABSENT (definitely not at venue)
		// No GPS + WiFi match → PRESENT (indoor GPS failure but WiFi confirms)
		// No GPS + WiFi no match → UNCERTAIN
		if gpsFar {
			status = "absent"
		} else if gpsNear && wifiMatch {
			status = "present"
		} else if gpsNear && !wifiMatch {
			status = "uncertain"
		} else if !hasGPS && wifiMatch {
			status = "present"
		} else {
			status = "uncertain"
		}
	} else {
		// ── GPS-ONLY MODE (no WiFi fingerprints calibrated) ──
		if hasGPS {
			accuracy := 999.0
			if resp.GPSAccuracy != nil {
				accuracy = *resp.GPSAccuracy
			}
			if accuracy <= 100 {
				inside := pointInPolygon(*resp.Latitude, *resp.Longitude, as.polygon)
				if inside {
					status = "present"
				} else {
					status = "absent"
				}
			}
		}
	}

	// Update record — compute GPS average across retakes
	wifiJSON, _ := json.Marshal(resp.WiFiScan)
	now := time.Now()

	// Load previous GPS data for averaging (if this is a retake)
	var prevLat, prevLng, prevAcc *float64
	var prevRetake int
	_ = h.pool.QueryRow(ctx,
		`SELECT avg_latitude, avg_longitude, avg_gps_accuracy, retake_number
		 FROM attendance_records WHERE session_id = $1 AND device_id = $2`,
		sessionID, deviceID,
	).Scan(&prevLat, &prevLng, &prevAcc, &prevRetake)

	// Compute averaged GPS position (weighted by 1/accuracy — better readings count more)
	avgLat := resp.Latitude
	avgLng := resp.Longitude
	avgAcc := resp.GPSAccuracy
	if resp.Latitude != nil && resp.Longitude != nil && prevLat != nil && prevLng != nil && prevRetake > 0 {
		// Weight: better accuracy (lower number) = higher weight
		prevW := 1.0
		currW := 1.0
		if prevAcc != nil && *prevAcc > 0 {
			prevW = 1.0 / *prevAcc
		}
		if resp.GPSAccuracy != nil && *resp.GPSAccuracy > 0 {
			currW = 1.0 / *resp.GPSAccuracy
		}
		totalW := prevW + currW
		mLat := (*prevLat*prevW + *resp.Latitude*currW) / totalW
		mLng := (*prevLng*prevW + *resp.Longitude*currW) / totalW
		avgLat = &mLat
		avgLng = &mLng
		// Averaged accuracy improves roughly as 1/sqrt(n)
		if resp.GPSAccuracy != nil && prevAcc != nil {
			bestAcc := math.Min(*prevAcc, *resp.GPSAccuracy) / math.Sqrt(float64(prevRetake+1))
			if bestAcc < 1 {
				bestAcc = 1
			}
			avgAcc = &bestAcc
		}
	}

	// Re-evaluate status using averaged position if available
	if avgLat != nil && avgLng != nil && (prevLat != nil || resp.Latitude != nil) {
		// Recalculate with averaged coordinates
		if len(as.wifiFingerprints) == 0 {
			// GPS-only mode with averaged position
			acc := 999.0
			if avgAcc != nil {
				acc = *avgAcc
			}
			if acc <= 100 {
				if pointInPolygon(*avgLat, *avgLng, as.polygon) {
					status = "present"
				} else {
					status = "absent"
				}
			}
		}
		// If hybrid mode, the wifiScore-based status is already good;
		// but update GPS coarse check with averaged position
	}

	_, err := h.pool.Exec(ctx,
		`UPDATE attendance_records
		 SET status = $1, latitude = $2, longitude = $3, gps_accuracy = $4,
		     wifi_scan = $5, battery_level = $6, connection_type = $7,
		     response_time_ms = $8, responded_at = $9,
		     avg_latitude = $10, avg_longitude = $11, avg_gps_accuracy = $12
		 WHERE session_id = $13 AND device_id = $14`,
		status, resp.Latitude, resp.Longitude, resp.GPSAccuracy,
		wifiJSON, resp.BatteryLevel, resp.ConnectionType,
		responseTimeMs, now,
		avgLat, avgLng, avgAcc,
		sessionID, deviceID)
	if err != nil {
		log.Printf("Error updating attendance record: %v", err)
	}

	if hasWifiFingerprints && wifiScore > 0 {
		// Store wifi_score as metadata in wifi_scan JSON
		log.Printf("Attendance device %s: status=%s gps_near=%v wifi_score=%.3f threshold=%.2f",
			deviceID, status, hasGPS, wifiScore, as.wifiThreshold)
	}

	// Update active session counter
	as.mu.Lock()
	as.respondedCount++
	switch status {
	case "present":
		as.presentCount++
	case "absent":
		as.absentCount++
	case "uncertain":
		as.uncertainCount++
	}

	allResponded := as.respondedCount >= as.totalDevices
	as.mu.Unlock()

	// Update session counts in DB
	h.updateSessionCounts(ctx, sessionID)

	// If all responded, mark complete
	if allResponded {
		as.mu.Lock()
		as.completed = true
		as.mu.Unlock()

		h.completeSession(ctx, sessionID)
		h.activeSessions.Delete(sessionID.String())
	}
}

func (h *AttendanceHandler) markDeviceResult(ctx context.Context, sessionID, deviceID uuid.UUID, status string, resp *DeviceAttendanceResponse) {
	if resp != nil {
		wifiJSON, _ := json.Marshal(resp.WiFiScan)
		_, _ = h.pool.Exec(ctx,
			`UPDATE attendance_records
			 SET status = $1, latitude = $2, longitude = $3, gps_accuracy = $4,
			     wifi_scan = $5, battery_level = $6, connection_type = $7, responded_at = $8
			 WHERE session_id = $9 AND device_id = $10`,
			status, resp.Latitude, resp.Longitude, resp.GPSAccuracy,
			wifiJSON, resp.BatteryLevel, resp.ConnectionType, time.Now(),
			sessionID, deviceID)
	} else {
		_, _ = h.pool.Exec(ctx,
			`UPDATE attendance_records SET status = $1 WHERE session_id = $2 AND device_id = $3`,
			status, sessionID, deviceID)
	}
}

func (h *AttendanceHandler) updateSessionCounts(ctx context.Context, sessionID uuid.UUID) {
	_, err := h.pool.Exec(ctx, `
		UPDATE attendance_sessions SET
			present_count = (SELECT COUNT(*) FROM attendance_records WHERE session_id = $1 AND status = 'present'),
			absent_count = (SELECT COUNT(*) FROM attendance_records WHERE session_id = $1 AND status = 'absent'),
			offline_count = (SELECT COUNT(*) FROM attendance_records WHERE session_id = $1 AND status = 'offline'),
			uncertain_count = (SELECT COUNT(*) FROM attendance_records WHERE session_id = $1 AND status = 'uncertain')
		WHERE id = $1`, sessionID)
	if err != nil {
		log.Printf("Error updating session counts: %v", err)
	}
}

func (h *AttendanceHandler) completeSession(ctx context.Context, sessionID uuid.UUID) {
	_, err := h.pool.Exec(ctx,
		`UPDATE attendance_sessions SET status = 'completed', completed_at = $1 WHERE id = $2`,
		time.Now(), sessionID)
	if err != nil {
		log.Printf("Error completing session: %v", err)
	}

	// Run cluster chaining analysis after completing
	go h.runClusterChainAnalysis(ctx, sessionID)
}

// ── Cluster Chain Analysis ──
// After all GPS data is in, look at the spatial cluster of devices.
// Devices that are close to confirmed-present devices get upgraded from absent/uncertain → present.
// This handles GPS drift at polygon edges in halls where devices sit side by side.
func (h *AttendanceHandler) runClusterChainAnalysis(bgCtx context.Context, sessionID uuid.UUID) {
	ctx := context.Background() // don't use request ctx in goroutine

	// Load zone polygon for max-zone-distance guard
	var zoneID uuid.UUID
	var centerLat, centerLng *float64
	err := h.pool.QueryRow(ctx,
		`SELECT s.zone_id, z.center_lat, z.center_lng
		 FROM attendance_sessions s
		 JOIN attendance_zones z ON s.zone_id = z.id
		 WHERE s.id = $1`, sessionID,
	).Scan(&zoneID, &centerLat, &centerLng)
	if err != nil {
		log.Printf("Cluster: failed to load zone for session %s: %v", sessionID, err)
		return
	}

	cLat, cLng := 0.0, 0.0
	if centerLat != nil {
		cLat = *centerLat
	}
	if centerLng != nil {
		cLng = *centerLng
	}

	// Load all records with GPS data
	type deviceRecord struct {
		ID       uuid.UUID
		DeviceID uuid.UUID
		Status   string
		Lat      float64
		Lng      float64
		Accuracy float64
		HasGPS   bool
	}

	rows, err := h.pool.Query(ctx,
		`SELECT id, device_id, status,
		        COALESCE(avg_latitude, latitude), COALESCE(avg_longitude, longitude),
		        COALESCE(avg_gps_accuracy, gps_accuracy, 999)
		 FROM attendance_records WHERE session_id = $1`, sessionID)
	if err != nil {
		log.Printf("Cluster: failed to load records for session %s: %v", sessionID, err)
		return
	}
	defer rows.Close()

	var devices []deviceRecord
	for rows.Next() {
		var d deviceRecord
		var lat, lng, acc *float64
		if err := rows.Scan(&d.ID, &d.DeviceID, &d.Status, &lat, &lng, &acc); err != nil {
			continue
		}
		if lat != nil && lng != nil {
			d.Lat = *lat
			d.Lng = *lng
			d.HasGPS = true
		}
		if acc != nil {
			d.Accuracy = *acc
		}
		devices = append(devices, d)
	}

	if len(devices) < 2 {
		log.Printf("Cluster: session %s has <2 devices, skipping", sessionID)
		return
	}

	// ── Configuration ──
	const chainRadius = 25.0     // max distance between two "neighbors" (meters)
	const maxZoneDistance = 80.0 // device must be within this of zone center to be chain-eligible
	const maxGPSAccuracy = 60.0  // ignore devices with worse accuracy for chaining source

	// Build sets
	presentSet := make(map[int]bool)   // index -> true
	candidateSet := make(map[int]bool) // absent/uncertain devices eligible for upgrade

	for i, d := range devices {
		if !d.HasGPS {
			continue
		}
		// Save raw_status (original GPS-only verdict) before cluster modifies it
		_, _ = h.pool.Exec(ctx,
			`UPDATE attendance_records SET raw_status = status WHERE id = $1 AND raw_status IS NULL`, d.ID)

		if d.Status == "present" {
			presentSet[i] = true
		} else if d.Status == "absent" || d.Status == "uncertain" {
			// Check guards: must have GPS, accuracy < cap, within max zone distance
			distToZone := haversineDistance(d.Lat, d.Lng, cLat, cLng)
			if d.Accuracy <= maxGPSAccuracy && distToZone <= maxZoneDistance {
				candidateSet[i] = true
			}
		}
	}

	if len(presentSet) == 0 {
		log.Printf("Cluster: session %s has no present devices — no chaining possible", sessionID)
		return
	}

	// ── Iterative flood-fill chaining ──
	// Each pass: candidates within chainRadius of any present device get upgraded.
	// Then they become sources for the next pass.
	changed := true
	totalUpgraded := 0
	for changed {
		changed = false
		for ci := range candidateSet {
			c := devices[ci]
			bestDist := math.MaxFloat64
			bestPresentIdx := -1

			// Find nearest present device
			for pi := range presentSet {
				p := devices[pi]
				dist := haversineDistance(c.Lat, c.Lng, p.Lat, p.Lng)
				if dist < bestDist {
					bestDist = dist
					bestPresentIdx = pi
				}
			}

			if bestDist <= chainRadius && bestPresentIdx >= 0 {
				// Upgrade this device to present via cluster chain
				devices[ci].Status = "present"
				presentSet[ci] = true
				delete(candidateSet, ci)
				changed = true
				totalUpgraded++

				chainDeviceID := devices[bestPresentIdx].DeviceID
				_, _ = h.pool.Exec(ctx,
					`UPDATE attendance_records
					 SET status = 'present', cluster_status = 'chain_upgraded',
					     cluster_chain_device = $1, cluster_distance = $2
					 WHERE id = $3`,
					chainDeviceID, bestDist, devices[ci].ID)

				log.Printf("Cluster: device %s upgraded to present (%.1fm from %s)",
					devices[ci].DeviceID, bestDist, chainDeviceID)
			}
		}
	}

	// Mark present devices that were NOT upgraded as "direct_present"
	for pi := range presentSet {
		// Only mark if it wasn't chain-upgraded (already has cluster_status)
		_, _ = h.pool.Exec(ctx,
			`UPDATE attendance_records SET cluster_status = 'direct'
			 WHERE id = $1 AND cluster_status IS NULL AND status = 'present'`,
			devices[pi].ID)
	}

	if totalUpgraded > 0 {
		// Recount session totals after cluster upgrades
		h.updateSessionCounts(ctx, sessionID)
		log.Printf("Cluster: session %s — %d device(s) upgraded via chain linking", sessionID, totalUpgraded)
	} else {
		log.Printf("Cluster: session %s — no upgrades (all correctly classified)", sessionID)
	}
}

// ── Retake Attendance ──
// Fires a new GPS round for the SAME session. Merges GPS positions with previous takes
// (weighted average) then re-runs cluster analysis. Only re-queries online devices that
// already responded in the original session (no point querying ones that were offline).
func (h *AttendanceHandler) RetakeAttendance(c *fiber.Ctx) error {
	sessionID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid session ID"})
	}

	// Load session — must be completed
	var zoneID uuid.UUID
	var status string
	var retakeCount int
	var timeoutSeconds int
	err = h.pool.QueryRow(c.Context(),
		`SELECT zone_id, status, retake_count, timeout_seconds FROM attendance_sessions WHERE id = $1`, sessionID,
	).Scan(&zoneID, &status, &retakeCount, &timeoutSeconds)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"message": "Session not found"})
	}
	if status != "completed" {
		return c.Status(400).JSON(fiber.Map{"message": "Session must be completed before retake"})
	}

	// Load zone for polygon + fingerprints
	var bufferedPolygonJSON json.RawMessage
	var wifiFingerprintsJSON json.RawMessage
	var wifiThreshold *float64
	var centerLat, centerLng *float64
	err = h.pool.QueryRow(c.Context(),
		"SELECT buffered_polygon, wifi_fingerprints, wifi_match_threshold, center_lat, center_lng FROM attendance_zones WHERE id = $1", zoneID,
	).Scan(&bufferedPolygonJSON, &wifiFingerprintsJSON, &wifiThreshold, &centerLat, &centerLng)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"message": "Failed to load zone"})
	}

	var bufferedPolygon [][]float64
	_ = json.Unmarshal(bufferedPolygonJSON, &bufferedPolygon)
	var wifiFingerprints []WifiFingerprint
	if wifiFingerprintsJSON != nil {
		_ = json.Unmarshal(wifiFingerprintsJSON, &wifiFingerprints)
	}
	threshold := 0.6
	if wifiThreshold != nil {
		threshold = *wifiThreshold
	}
	cLat, cLng := 0.0, 0.0
	if centerLat != nil {
		cLat = *centerLat
	}
	if centerLng != nil {
		cLng = *centerLng
	}

	// Increment retake count
	newRetake := retakeCount + 1
	_, _ = h.pool.Exec(c.Context(),
		`UPDATE attendance_sessions SET retake_count = $1, status = 'in_progress', completed_at = NULL WHERE id = $2`,
		newRetake, sessionID)

	// Get ALL non-offline devices from this session — retake sends to everyone
	// regardless of current device online status. Commands queue via MQTT;
	// devices that don't respond will have their previous status restored on timeout.
	rows, err := h.pool.Query(c.Context(),
		`SELECT r.device_id, r.status
		 FROM attendance_records r
		 WHERE r.session_id = $1 AND r.status != 'offline'`, sessionID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"message": "Failed to query records"})
	}
	defer rows.Close()

	type retakeDevice struct {
		deviceID   uuid.UUID
		prevStatus string
	}
	var retakeDevices []retakeDevice
	for rows.Next() {
		var rd retakeDevice
		if err := rows.Scan(&rd.deviceID, &rd.prevStatus); err == nil {
			retakeDevices = append(retakeDevices, rd)
		}
	}

	if len(retakeDevices) == 0 {
		// All devices were offline in original — nothing to retake
		_, _ = h.pool.Exec(c.Context(),
			`UPDATE attendance_sessions SET retake_count = $1, status = 'completed' WHERE id = $2`,
			retakeCount, sessionID) // revert retake_count
		return c.JSON(fiber.Map{"data": fiber.Map{
			"session_id":   sessionID,
			"retake_count": retakeCount,
			"status":       "completed",
			"message":      "No devices to retake — all were offline in original session",
		}})
	}

	// Save previous status for each device (so timeout can restore, not destroy data)
	prevStatuses := make(map[uuid.UUID]string, len(retakeDevices))
	for _, rd := range retakeDevices {
		prevStatuses[rd.deviceID] = rd.prevStatus
	}

	// Reset records to "pending" for retake (GPS avg columns are preserved)
	for _, rd := range retakeDevices {
		_, _ = h.pool.Exec(c.Context(),
			`UPDATE attendance_records
			 SET status = 'pending', retake_number = $1,
			     cluster_status = NULL, cluster_chain_device = NULL, cluster_distance = NULL
			 WHERE session_id = $2 AND device_id = $3`,
			newRetake, sessionID, rd.deviceID)
	}

	// Update counts immediately so dashboard shows pending state
	h.updateSessionCounts(c.Context(), sessionID)

	// Track active session for retake responses
	as := &activeSession{
		sessionID:        sessionID,
		zoneID:           zoneID,
		polygon:          bufferedPolygon,
		wifiFingerprints: wifiFingerprints,
		wifiThreshold:    threshold,
		centerLat:        cLat,
		centerLng:        cLng,
		totalDevices:     len(retakeDevices),
		startTime:        time.Now(),
		timeoutSeconds:   timeoutSeconds,
		deviceCommandMap: make(map[uuid.UUID]uuid.UUID),
		retakePrevStatus: prevStatuses,
	}
	h.activeSessions.Store(sessionID.String(), as)

	// Fan out GET_ATTENDANCE to ALL retake devices
	sentCount := 0
	for _, rd := range retakeDevices {
		cmd, err := h.commandService.CreateCommand(c.Context(), rd.deviceID, &models.CreateCommandRequest{
			CommandType: "GET_ATTENDANCE",
			Payload: map[string]interface{}{
				"session_id": sessionID.String(),
			},
			Priority:       10,
			TimeoutSeconds: timeoutSeconds + 15,
		}, nil)
		if err != nil {
			log.Printf("Retake: failed to send GET_ATTENDANCE to %s: %v", rd.deviceID, err)
			continue
		}
		as.mu.Lock()
		as.deviceCommandMap[rd.deviceID] = cmd.ID
		as.mu.Unlock()
		sentCount++
	}

	// Start timeout — on expiry, unresponsive devices get previous status restored
	go h.sessionTimeout(sessionID, time.Duration(timeoutSeconds)*time.Second)

	log.Printf("Retake #%d for session %s: %d devices targeted, %d commands sent",
		newRetake, sessionID, len(retakeDevices), sentCount)

	return c.JSON(fiber.Map{"data": fiber.Map{
		"session_id":       sessionID,
		"retake_count":     newRetake,
		"targeted_devices": len(retakeDevices),
		"commands_sent":    sentCount,
		"status":           "in_progress",
		"timeout":          timeoutSeconds,
	}})
}

// ── Command Result Hook ──
// HandleCommandResult is called by the command service when a GET_ATTENDANCE command completes.
// It extracts attendance data from the command result and processes it.
func (h *AttendanceHandler) HandleCommandResult(ctx context.Context, cmd *models.Command, result map[string]interface{}) {
	if result == nil {
		return
	}

	sessionIDStr, _ := result["session_id"].(string)
	if sessionIDStr == "" {
		// Try payload
		if cmd.Payload != nil {
			sessionIDStr, _ = cmd.Payload["session_id"].(string)
		}
	}
	if sessionIDStr == "" {
		log.Printf("Attendance result missing session_id for command %s", cmd.ID)
		return
	}

	sessionID, err := uuid.Parse(sessionIDStr)
	if err != nil {
		log.Printf("Invalid session_id in attendance result: %s", sessionIDStr)
		return
	}

	// Extract response data
	resp := &DeviceAttendanceResponse{
		SessionID: sessionIDStr,
		DeviceID:  cmd.DeviceID.String(),
	}

	if lat, ok := result["latitude"].(float64); ok {
		resp.Latitude = &lat
	}
	if lng, ok := result["longitude"].(float64); ok {
		resp.Longitude = &lng
	}
	if acc, ok := result["gps_accuracy"].(float64); ok {
		resp.GPSAccuracy = &acc
	}
	if battery, ok := result["battery_level"].(float64); ok {
		b := int(battery)
		resp.BatteryLevel = &b
	}
	if connType, ok := result["connection_type"].(string); ok {
		resp.ConnectionType = connType
	}
	if wifiScan, ok := result["wifi_scan"].([]interface{}); ok {
		var parsed []map[string]interface{}
		for _, item := range wifiScan {
			if m, ok := item.(map[string]interface{}); ok {
				parsed = append(parsed, m)
			}
		}
		resp.WiFiScan = parsed
	}

	h.processDeviceResponse(ctx, sessionID, cmd.DeviceID, resp)
}

// ── Session Query ──

func (h *AttendanceHandler) GetSession(c *fiber.Ctx) error {
	sessionID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid session ID"})
	}

	var id uuid.UUID
	var zoneID uuid.UUID
	var status string
	var totalDevices, presentCount, absentCount, offlineCount, uncertainCount int
	var initiatedAt, createdAt time.Time
	var completedAt *time.Time
	var retakeCount int

	err = h.pool.QueryRow(c.Context(),
		`SELECT id, zone_id, status, total_devices, present_count, absent_count, offline_count, uncertain_count, initiated_at, completed_at, created_at, COALESCE(retake_count, 1)
		 FROM attendance_sessions WHERE id = $1`, sessionID,
	).Scan(&id, &zoneID, &status, &totalDevices, &presentCount, &absentCount, &offlineCount, &uncertainCount, &initiatedAt, &completedAt, &createdAt, &retakeCount)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"message": "Session not found"})
	}

	return c.JSON(fiber.Map{
		"data": fiber.Map{
			"id":              id,
			"zone_id":         zoneID,
			"status":          status,
			"total_devices":   totalDevices,
			"present_count":   presentCount,
			"absent_count":    absentCount,
			"offline_count":   offlineCount,
			"uncertain_count": uncertainCount,
			"initiated_at":    initiatedAt,
			"completed_at":    completedAt,
			"retake_count":    retakeCount,
		},
	})
}

func (h *AttendanceHandler) GetSessionRecords(c *fiber.Ctx) error {
	sessionID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid session ID"})
	}

	rows, err := h.pool.Query(c.Context(),
		`SELECT r.id, r.device_id, r.status, r.latitude, r.longitude, r.gps_accuracy,
		        r.wifi_scan, r.battery_level, r.connection_type, r.response_time_ms, r.responded_at,
		        d.name as device_name, d.model as device_model, d.device_id as device_hw_id,
		        r.cluster_status, r.cluster_chain_device, r.cluster_distance,
		        r.avg_latitude, r.avg_longitude, r.avg_gps_accuracy, r.raw_status, r.retake_number
		 FROM attendance_records r
		 JOIN devices d ON r.device_id = d.id
		 WHERE r.session_id = $1
		 ORDER BY r.status, d.name`, sessionID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"message": "Failed to list records"})
	}
	defer rows.Close()

	var records []fiber.Map
	for rows.Next() {
		var id, deviceID uuid.UUID
		var status string
		var lat, lng, gpsAcc *float64
		var wifiScan json.RawMessage
		var batteryLevel *int
		var connectionType *string
		var responseTimeMs *int
		var respondedAt *time.Time
		var deviceName, deviceModel, deviceHwID *string
		var clusterStatus *string
		var clusterChainDevice *uuid.UUID
		var clusterDistance *float64
		var avgLat, avgLng, avgAcc *float64
		var rawStatus *string
		var retakeNumber *int

		if err := rows.Scan(&id, &deviceID, &status, &lat, &lng, &gpsAcc, &wifiScan, &batteryLevel, &connectionType, &responseTimeMs, &respondedAt, &deviceName, &deviceModel, &deviceHwID,
			&clusterStatus, &clusterChainDevice, &clusterDistance,
			&avgLat, &avgLng, &avgAcc, &rawStatus, &retakeNumber); err != nil {
			log.Printf("Error scanning attendance record: %v", err)
			continue
		}

		rec := fiber.Map{
			"id":                   id,
			"device_id":            deviceID,
			"status":               status,
			"latitude":             lat,
			"longitude":            lng,
			"gps_accuracy":         gpsAcc,
			"battery_level":        batteryLevel,
			"connection_type":      connectionType,
			"response_time_ms":     responseTimeMs,
			"responded_at":         respondedAt,
			"device_name":          deviceName,
			"device_model":         deviceModel,
			"device_hw_id":         deviceHwID,
			"cluster_status":       clusterStatus,
			"cluster_chain_device": clusterChainDevice,
			"cluster_distance":     clusterDistance,
			"avg_latitude":         avgLat,
			"avg_longitude":        avgLng,
			"avg_gps_accuracy":     avgAcc,
			"raw_status":           rawStatus,
			"retake_number":        retakeNumber,
		}
		if wifiScan != nil {
			rec["wifi_scan"] = json.RawMessage(wifiScan)
		}
		records = append(records, rec)
	}

	if records == nil {
		records = []fiber.Map{}
	}

	return c.JSON(fiber.Map{"data": records})
}

func (h *AttendanceHandler) CompleteSession(c *fiber.Ctx) error {
	sessionID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid session ID"})
	}

	// Mark remaining pending as offline
	_, _ = h.pool.Exec(c.Context(),
		`UPDATE attendance_records SET status = 'offline' WHERE session_id = $1 AND status = 'pending'`,
		sessionID)

	h.updateSessionCounts(c.Context(), sessionID)
	h.completeSession(c.Context(), sessionID)
	h.activeSessions.Delete(sessionID.String())

	return c.JSON(fiber.Map{"data": fiber.Map{"completed": true}})
}

// ── WiFi Calibration ──

type CalibrateWiFiRequest struct {
	DeviceID   string `json:"device_id"`
	PointIndex int    `json:"point_index"` // Which calibration point (0-19)
}

func (h *AttendanceHandler) CalibrateWiFi(c *fiber.Ctx) error {
	zoneID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid zone ID"})
	}

	var req CalibrateWiFiRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid request body"})
	}

	deviceID, err := uuid.Parse(req.DeviceID)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid device_id"})
	}

	// Verify zone exists
	var exists bool
	err = h.pool.QueryRow(c.Context(),
		"SELECT EXISTS(SELECT 1 FROM attendance_zones WHERE id = $1)", zoneID).Scan(&exists)
	if err != nil || !exists {
		return c.Status(404).JSON(fiber.Map{"message": "Zone not found"})
	}

	// Send CALIBRATE_WIFI command to the device
	cmd, err := h.commandService.CreateCommand(c.Context(), deviceID, &models.CreateCommandRequest{
		CommandType: "CALIBRATE_WIFI",
		Payload: map[string]interface{}{
			"zone_id":     zoneID.String(),
			"point_index": req.PointIndex,
		},
		Priority:       10,
		TimeoutSeconds: 30,
	}, nil)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"message": "Failed to send calibration command: " + err.Error()})
	}

	// Track this command -> zone mapping so the hook knows where to store results
	h.calibrateCommands.Store(cmd.ID.String(), zoneID)

	return c.Status(202).JSON(fiber.Map{
		"data": fiber.Map{
			"command_id":  cmd.ID,
			"device_id":   deviceID,
			"zone_id":     zoneID,
			"point_index": req.PointIndex,
			"status":      "sent",
		},
	})
}

// HandleCalibrateWiFiResult is called when a CALIBRATE_WIFI command completes.
func (h *AttendanceHandler) HandleCalibrateWiFiResult(ctx context.Context, cmd *models.Command, result map[string]interface{}) {
	if result == nil {
		return
	}

	// Look up zone from tracked calibration commands
	val, ok := h.calibrateCommands.LoadAndDelete(cmd.ID.String())
	if !ok {
		log.Printf("CALIBRATE_WIFI result for unknown command %s", cmd.ID)
		return
	}
	zoneID := val.(uuid.UUID)

	// Extract point_index from payload
	pointIndex := 0
	if cmd.Payload != nil {
		if pi, ok := cmd.Payload["point_index"].(float64); ok {
			pointIndex = int(pi)
		}
	}

	// Extract WiFi scan from result
	var scans []WifiScan
	if wifiData, ok := result["wifi_scan"].([]interface{}); ok {
		for _, item := range wifiData {
			if m, ok := item.(map[string]interface{}); ok {
				scan := WifiScan{}
				if b, ok := m["bssid"].(string); ok {
					scan.BSSID = b
				}
				if s, ok := m["ssid"].(string); ok {
					scan.SSID = s
				}
				if r, ok := m["rssi"].(float64); ok {
					scan.RSSI = r
				}
				if f, ok := m["frequency"].(float64); ok {
					scan.Frequency = int(f)
				}
				if scan.BSSID != "" {
					scans = append(scans, scan)
				}
			}
		}
	}

	if len(scans) == 0 {
		log.Printf("CALIBRATE_WIFI: no WiFi scans in result for zone %s", zoneID)
		return
	}

	// Load existing fingerprints
	var existingJSON json.RawMessage
	err := h.pool.QueryRow(ctx,
		"SELECT wifi_fingerprints FROM attendance_zones WHERE id = $1", zoneID,
	).Scan(&existingJSON)
	if err != nil {
		log.Printf("CALIBRATE_WIFI: failed to load zone %s: %v", zoneID, err)
		return
	}

	var fingerprints []WifiFingerprint
	if existingJSON != nil {
		_ = json.Unmarshal(existingJSON, &fingerprints)
	}

	// Replace or append fingerprint for this point_index
	newFP := WifiFingerprint{
		PointIndex: pointIndex,
		Scans:      scans,
		CapturedAt: time.Now(),
		DeviceID:   cmd.DeviceID.String(),
	}

	replaced := false
	for i, fp := range fingerprints {
		if fp.PointIndex == pointIndex {
			fingerprints[i] = newFP
			replaced = true
			break
		}
	}
	if !replaced {
		fingerprints = append(fingerprints, newFP)
	}

	// Save back
	fpJSON, _ := json.Marshal(fingerprints)
	_, err = h.pool.Exec(ctx,
		"UPDATE attendance_zones SET wifi_fingerprints = $1 WHERE id = $2", fpJSON, zoneID)
	if err != nil {
		log.Printf("CALIBRATE_WIFI: failed to save fingerprints for zone %s: %v", zoneID, err)
		return
	}

	log.Printf("CALIBRATE_WIFI: stored fingerprint for zone %s point %d (%d APs)", zoneID, pointIndex, len(scans))
}

func (h *AttendanceHandler) GetFingerprints(c *fiber.Ctx) error {
	zoneID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid zone ID"})
	}

	var fpJSON json.RawMessage
	err = h.pool.QueryRow(c.Context(),
		"SELECT wifi_fingerprints FROM attendance_zones WHERE id = $1", zoneID).Scan(&fpJSON)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"message": "Zone not found"})
	}

	return c.JSON(fiber.Map{"data": json.RawMessage(fpJSON)})
}

func (h *AttendanceHandler) DeleteFingerprint(c *fiber.Ctx) error {
	zoneID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid zone ID"})
	}

	indexStr := c.Params("index")
	var targetIndex int
	if _, err := fmt.Sscanf(indexStr, "%d", &targetIndex); err != nil {
		return c.Status(400).JSON(fiber.Map{"message": "Invalid index"})
	}

	var fpJSON json.RawMessage
	err = h.pool.QueryRow(c.Context(),
		"SELECT wifi_fingerprints FROM attendance_zones WHERE id = $1", zoneID).Scan(&fpJSON)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"message": "Zone not found"})
	}

	var fingerprints []WifiFingerprint
	if fpJSON != nil {
		_ = json.Unmarshal(fpJSON, &fingerprints)
	}

	// Remove fingerprint with matching point_index
	filtered := make([]WifiFingerprint, 0, len(fingerprints))
	for _, fp := range fingerprints {
		if fp.PointIndex != targetIndex {
			filtered = append(filtered, fp)
		}
	}

	newJSON, _ := json.Marshal(filtered)
	_, err = h.pool.Exec(c.Context(),
		"UPDATE attendance_zones SET wifi_fingerprints = $1 WHERE id = $2", newJSON, zoneID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"message": "Failed to delete fingerprint"})
	}

	return c.JSON(fiber.Map{"data": fiber.Map{"deleted": true, "remaining": len(filtered)}})
}

// ── WiFi Cosine Similarity ──

// wifiCosineSimilarity computes cosine similarity between reference and scan RSSI vectors.
// BSSIDs present in only one set get a floor value of -100 dBm.
// Returns 0.0 to 1.0 where higher = more similar.
func wifiCosineSimilarity(reference []WifiScan, scan []WifiScan) float64 {
	refMap := make(map[string]float64, len(reference))
	for _, r := range reference {
		refMap[r.BSSID] = r.RSSI
	}
	scanMap := make(map[string]float64, len(scan))
	for _, s := range scan {
		scanMap[s.BSSID] = s.RSSI
	}

	// Collect all BSSIDs
	allBSSIDs := make(map[string]bool)
	for k := range refMap {
		allBSSIDs[k] = true
	}
	for k := range scanMap {
		allBSSIDs[k] = true
	}

	if len(allBSSIDs) == 0 {
		return 0
	}

	const floor = -100.0
	var dotProduct, normRef, normScan float64
	commonCount := 0

	for bssid := range allBSSIDs {
		r := floor
		s := floor
		if v, ok := refMap[bssid]; ok {
			r = v
		}
		if v, ok := scanMap[bssid]; ok {
			s = v
		}

		// Shift to positive values (RSSI ranges roughly -30 to -100)
		rShifted := r - floor // 0 to ~70
		sShifted := s - floor

		dotProduct += rShifted * sShifted
		normRef += rShifted * rShifted
		normScan += sShifted * sShifted

		if _, inRef := refMap[bssid]; inRef {
			if _, inScan := scanMap[bssid]; inScan {
				commonCount++
			}
		}
	}

	if normRef == 0 || normScan == 0 {
		return 0
	}

	similarity := dotProduct / (math.Sqrt(normRef) * math.Sqrt(normScan))

	// Penalize if too few common BSSIDs (less confidence)
	if commonCount < 3 {
		similarity *= 0.5
	}

	return similarity
}

// ── Geometry Math ──

// pointInPolygon uses ray-casting algorithm
func pointInPolygon(lat, lng float64, polygon [][]float64) bool {
	n := len(polygon)
	if n < 3 {
		return false
	}

	inside := false
	j := n - 1
	for i := 0; i < n; i++ {
		yi := polygon[i][0]
		xi := polygon[i][1]
		yj := polygon[j][0]
		xj := polygon[j][1]

		if ((yi > lat) != (yj > lat)) &&
			(lng < (xj-xi)*(lat-yi)/(yj-yi)+xi) {
			inside = !inside
		}
		j = i
	}
	return inside
}

// computeCenter returns the centroid of a polygon
func computeCenter(polygon [][]float64) (float64, float64) {
	var sumLat, sumLng float64
	for _, p := range polygon {
		sumLat += p[0]
		sumLng += p[1]
	}
	n := float64(len(polygon))
	return sumLat / n, sumLng / n
}

// expandPolygon expands each vertex outward from center by bufferMeters
func expandPolygon(polygon [][]float64, bufferMeters float64) [][]float64 {
	centerLat, centerLng := computeCenter(polygon)
	expanded := make([][]float64, len(polygon))

	for i, p := range polygon {
		lat := p[0]
		lng := p[1]

		// Direction from center to vertex
		dLat := lat - centerLat
		dLng := lng - centerLng

		// Convert to meters (approximate at this latitude)
		metersPerDegreeLat := 111320.0
		metersPerDegreeLng := 111320.0 * math.Cos(centerLat*math.Pi/180)

		dxMeters := dLng * metersPerDegreeLng
		dyMeters := dLat * metersPerDegreeLat

		dist := math.Sqrt(dxMeters*dxMeters + dyMeters*dyMeters)
		if dist < 0.001 {
			expanded[i] = []float64{lat, lng}
			continue
		}

		// Scale factor to push vertex outward by bufferMeters
		scale := (dist + bufferMeters) / dist
		newLat := centerLat + dLat*scale
		newLng := centerLng + dLng*scale

		expanded[i] = []float64{
			math.Round(newLat*1e8) / 1e8,
			math.Round(newLng*1e8) / 1e8,
		}
	}
	return expanded
}

// haversineDistance returns distance in meters between two lat/lng points
func haversineDistance(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000
	phi1 := lat1 * math.Pi / 180
	phi2 := lat2 * math.Pi / 180
	dPhi := (lat2 - lat1) * math.Pi / 180
	dLambda := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(dPhi/2)*math.Sin(dPhi/2) +
		math.Cos(phi1)*math.Cos(phi2)*math.Sin(dLambda/2)*math.Sin(dLambda/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return R * c
}
