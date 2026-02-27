package handlers

import (
	"encoding/json"
	"log"
	"math"
	"sync"
	"time"

	"github.com/mdm-system/backend/internal/config"
	"github.com/mdm-system/backend/internal/models"
	"github.com/mdm-system/backend/internal/services"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// ==================== Constants ====================

const (
	trackingSessionTimeout = 30 * time.Minute
	trackingWriteDeadline  = 2 * time.Second
	trackingDeviceDeadline = 500 * time.Millisecond
	trackingPingInterval   = 10 * time.Second
	earthRadiusKm          = 6371.0
)

// ==================== Types ====================

type TrackingHandler struct {
	cfg            *config.Config
	sessions       *TrackingSessionManager
	commandService *services.CommandService
}

type TrackingSession struct {
	ID         string          `json:"id"`
	DeviceID   string          `json:"device_id"`
	AdminID    string          `json:"admin_id"`
	Status     string          `json:"status"` // waiting, tracking, ended
	CreatedAt  time.Time       `json:"created_at"`
	AdminConn  *websocket.Conn `json:"-"`
	DeviceConn *websocket.Conn `json:"-"`
	mutex      sync.RWMutex

	// Location trail & daily distance
	Trail          []LocationPoint `json:"trail"`
	DailyDistance  float64         `json:"daily_distance_km"` // km traveled today
	TotalDistance  float64         `json:"total_distance_km"` // km traveled in session
	PointCount     int64           `json:"point_count"`
	LastPoint      *LocationPoint  `json:"-"`
	DailyStartDate string          `json:"-"` // YYYY-MM-DD for daily reset
}

type LocationPoint struct {
	Lat       float64 `json:"lat"`
	Lng       float64 `json:"lng"`
	Accuracy  float64 `json:"acc"`
	Speed     float64 `json:"spd"` // m/s
	Heading   float64 `json:"hdg"` // degrees
	Altitude  float64 `json:"alt"` // meters
	Battery   int     `json:"bat"` // percent
	Timestamp int64   `json:"ts"`  // unix ms
}

type TrackingSessionManager struct {
	sessions map[string]*TrackingSession
	byDevice map[string]*TrackingSession
	mutex    sync.RWMutex
}

type TrackingSignalMessage struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id,omitempty"`
	DeviceID  string          `json:"device_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

func NewTrackingHandler(cfg *config.Config, commandService *services.CommandService) *TrackingHandler {
	return &TrackingHandler{
		cfg:            cfg,
		commandService: commandService,
		sessions: &TrackingSessionManager{
			sessions: make(map[string]*TrackingSession),
			byDevice: make(map[string]*TrackingSession),
		},
	}
}

func (h *TrackingHandler) RegisterRoutes(app *fiber.App) {
	tracking := app.Group("/api/v1/tracking")

	tracking.Post("/sessions", h.CreateSession)
	tracking.Get("/sessions/:id", h.GetSession)
	tracking.Delete("/sessions/:id", h.EndSession)

	app.Get("/ws/tracking/admin/:session_id", h.upgradeWS, websocket.New(h.AdminWebSocket))
	app.Get("/ws/tracking/device/:session_id", h.upgradeWS, websocket.New(h.DeviceWebSocket))
}

func (h *TrackingHandler) upgradeWS(c *fiber.Ctx) error {
	if websocket.IsWebSocketUpgrade(c) {
		c.Locals("allowed", true)
		return c.Next()
	}
	return fiber.ErrUpgradeRequired
}

// ==================== REST endpoints ====================

func (h *TrackingHandler) CreateSession(c *fiber.Ctx) error {
	var req struct {
		DeviceID string `json:"device_id"`
	}
	if err := c.BodyParser(&req); err != nil || req.DeviceID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "device_id is required"})
	}

	// Close any existing session for this device
	h.sessions.mutex.Lock()
	if existing := h.sessions.byDevice[req.DeviceID]; existing != nil && existing.Status != "ended" {
		existing.Status = "ended"
		delete(h.sessions.byDevice, existing.DeviceID)
		if existing.AdminConn != nil {
			existing.AdminConn.Close()
		}
		if existing.DeviceConn != nil {
			existing.DeviceConn.Close()
		}
	}
	h.sessions.mutex.Unlock()

	sessionID := uuid.New().String()
	today := time.Now().Format("2006-01-02")

	session := &TrackingSession{
		ID:             sessionID,
		DeviceID:       req.DeviceID,
		AdminID:        "admin",
		Status:         "waiting",
		CreatedAt:      time.Now(),
		Trail:          make([]LocationPoint, 0, 512),
		DailyStartDate: today,
	}

	h.sessions.mutex.Lock()
	h.sessions.sessions[sessionID] = session
	h.sessions.byDevice[req.DeviceID] = session
	h.sessions.mutex.Unlock()

	// Send START_TRACKING command to the device
	deviceUUID, err := uuid.Parse(req.DeviceID)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid device_id"})
	}
	cmdReq := &models.CreateCommandRequest{
		CommandType: "START_TRACKING",
		Payload: map[string]interface{}{
			"session_id": sessionID,
		},
	}
	if _, err := h.commandService.CreateCommand(c.Context(), deviceUUID, cmdReq, nil); err != nil {
		log.Printf("Warning: failed to send START_TRACKING command: %v", err)
	}

	// Auto-expire
	go func() {
		time.Sleep(trackingSessionTimeout)
		h.sessions.mutex.Lock()
		defer h.sessions.mutex.Unlock()
		if s, ok := h.sessions.sessions[sessionID]; ok && s.Status == "waiting" {
			s.Status = "ended"
			delete(h.sessions.byDevice, s.DeviceID)
		}
	}()

	return c.Status(201).JSON(models.SuccessResponse(fiber.Map{
		"session_id": sessionID,
		"device_id":  req.DeviceID,
		"status":     "waiting",
		"ws_url":     "/ws/tracking/admin/" + sessionID,
	}))
}

func (h *TrackingHandler) GetSession(c *fiber.Ctx) error {
	id := c.Params("id")
	h.sessions.mutex.RLock()
	s := h.sessions.sessions[id]
	h.sessions.mutex.RUnlock()
	if s == nil {
		return c.Status(404).JSON(fiber.Map{"error": "session not found"})
	}
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return c.JSON(fiber.Map{
		"session_id":        s.ID,
		"device_id":         s.DeviceID,
		"status":            s.Status,
		"point_count":       s.PointCount,
		"daily_distance_km": math.Round(s.DailyDistance*100) / 100,
		"total_distance_km": math.Round(s.TotalDistance*100) / 100,
		"created_at":        s.CreatedAt,
	})
}

func (h *TrackingHandler) EndSession(c *fiber.Ctx) error {
	id := c.Params("id")
	h.sessions.mutex.Lock()
	s := h.sessions.sessions[id]
	if s != nil {
		s.Status = "ended"
		delete(h.sessions.byDevice, s.DeviceID)
		if s.AdminConn != nil {
			s.AdminConn.Close()
		}
		if s.DeviceConn != nil {
			s.DeviceConn.Close()
		}
	}
	h.sessions.mutex.Unlock()

	if s == nil {
		return c.Status(404).JSON(fiber.Map{"error": "session not found"})
	}

	// Send STOP_TRACKING to device
	deviceUUID, err := uuid.Parse(s.DeviceID)
	if err == nil {
		cmdReq := &models.CreateCommandRequest{
			CommandType: "STOP_TRACKING",
			Payload:     map[string]interface{}{},
		}
		h.commandService.CreateCommand(c.Context(), deviceUUID, cmdReq, nil)
	}

	return c.JSON(fiber.Map{
		"status":            "ended",
		"daily_distance_km": math.Round(s.DailyDistance*100) / 100,
		"total_distance_km": math.Round(s.TotalDistance*100) / 100,
		"point_count":       s.PointCount,
	})
}

// ==================== Admin WebSocket ====================

func (h *TrackingHandler) AdminWebSocket(c *websocket.Conn) {
	sessionID := c.Params("session_id")

	h.sessions.mutex.Lock()
	session := h.sessions.sessions[sessionID]
	if session == nil || session.Status == "ended" {
		h.sessions.mutex.Unlock()
		c.WriteJSON(TrackingSignalMessage{Type: "error", Payload: json.RawMessage(`"session not found"`)})
		c.Close()
		return
	}
	session.AdminConn = c
	h.sessions.mutex.Unlock()

	log.Printf("Admin connected to tracking session %s", sessionID)

	// Send initial info + any existing trail
	session.mutex.RLock()
	initPayload, _ := json.Marshal(fiber.Map{
		"session_id":        sessionID,
		"device_id":         session.DeviceID,
		"trail":             session.Trail,
		"daily_distance_km": math.Round(session.DailyDistance*100) / 100,
		"total_distance_km": math.Round(session.TotalDistance*100) / 100,
		"point_count":       session.PointCount,
	})
	session.mutex.RUnlock()

	c.SetWriteDeadline(time.Now().Add(trackingWriteDeadline))
	c.WriteJSON(TrackingSignalMessage{Type: "session_info", SessionID: sessionID, Payload: initPayload})
	c.SetWriteDeadline(time.Time{})

	defer func() {
		h.sessions.mutex.Lock()
		if session.AdminConn == c {
			session.AdminConn = nil
		}
		h.sessions.mutex.Unlock()
		c.Close()
	}()

	// Admin read loop (for control messages)
	for {
		msgType, data, err := c.ReadMessage()
		if err != nil {
			break
		}
		if msgType == fws.TextMessage {
			var msg TrackingSignalMessage
			if json.Unmarshal(data, &msg) == nil {
				switch msg.Type {
				case "stop":
					// Admin requests stop
					session.mutex.RLock()
					dc := session.DeviceConn
					session.mutex.RUnlock()
					if dc != nil {
						dc.SetWriteDeadline(time.Now().Add(trackingDeviceDeadline))
						dc.WriteJSON(TrackingSignalMessage{Type: "stop_tracking"})
						dc.SetWriteDeadline(time.Time{})
					}
				case "get_trail":
					// Admin requests full trail
					session.mutex.RLock()
					trailPayload, _ := json.Marshal(fiber.Map{
						"trail":             session.Trail,
						"daily_distance_km": math.Round(session.DailyDistance*100) / 100,
						"total_distance_km": math.Round(session.TotalDistance*100) / 100,
					})
					session.mutex.RUnlock()
					c.SetWriteDeadline(time.Now().Add(trackingWriteDeadline))
					c.WriteJSON(TrackingSignalMessage{Type: "trail_data", Payload: trailPayload})
					c.SetWriteDeadline(time.Time{})
				}
			}
		}
	}
}

// ==================== Device WebSocket ====================

func (h *TrackingHandler) DeviceWebSocket(c *websocket.Conn) {
	sessionID := c.Params("session_id")

	h.sessions.mutex.Lock()
	session := h.sessions.sessions[sessionID]
	if session == nil || session.Status == "ended" {
		h.sessions.mutex.Unlock()
		c.WriteJSON(TrackingSignalMessage{Type: "error", Payload: json.RawMessage(`"session not found"`)})
		c.Close()
		return
	}
	session.DeviceConn = c
	session.Status = "tracking"
	h.sessions.mutex.Unlock()

	log.Printf("Device connected to tracking session %s", sessionID)

	// Notify admin
	session.mutex.RLock()
	ac := session.AdminConn
	session.mutex.RUnlock()
	if ac != nil {
		ac.SetWriteDeadline(time.Now().Add(trackingWriteDeadline))
		ac.WriteJSON(TrackingSignalMessage{Type: "device_connected"})
		ac.SetWriteDeadline(time.Time{})
	}

	defer func() {
		h.sessions.mutex.Lock()
		if session.DeviceConn == c {
			session.DeviceConn = nil
		}
		h.sessions.mutex.Unlock()

		session.mutex.RLock()
		ac := session.AdminConn
		session.mutex.RUnlock()
		if ac != nil {
			ac.SetWriteDeadline(time.Now().Add(trackingWriteDeadline))
			ac.WriteJSON(TrackingSignalMessage{Type: "device_disconnected"})
			ac.SetWriteDeadline(time.Time{})
		}
		c.Close()
	}()

	// Device sends location JSON frames
	for {
		msgType, data, err := c.ReadMessage()
		if err != nil {
			break
		}
		if msgType != fws.TextMessage {
			continue
		}

		// Try single point
		var point LocationPoint
		if err := json.Unmarshal(data, &point); err != nil {
			// Try batch of points (for 2G reconnect flush)
			var batch []LocationPoint
			if err2 := json.Unmarshal(data, &batch); err2 != nil {
				continue
			}
			for _, p := range batch {
				h.processPoint(session, &p)
			}
			continue
		}
		h.processPoint(session, &point)
	}
}

// processPoint adds a location point to the trail, updates distance, and relays to admin.
func (h *TrackingHandler) processPoint(session *TrackingSession, point *LocationPoint) {
	session.mutex.Lock()

	// Daily reset check
	today := time.Now().Format("2006-01-02")
	if today != session.DailyStartDate {
		session.DailyDistance = 0
		session.DailyStartDate = today
	}

	// Calculate distance from last point
	if session.LastPoint != nil {
		dist := haversine(
			session.LastPoint.Lat, session.LastPoint.Lng,
			point.Lat, point.Lng,
		)

		// Only add distance if accuracy is reasonable (< 50m) and distance > 3m
		// This filters GPS jitter while stationary
		if point.Accuracy < 50 && dist > 0.003 {
			session.DailyDistance += dist
			session.TotalDistance += dist
		}
	}

	session.LastPoint = point
	session.PointCount++

	// Keep trail bounded (last 10000 points ≈ hours of tracking at 3s intervals)
	if len(session.Trail) >= 10000 {
		session.Trail = session.Trail[1:]
	}
	session.Trail = append(session.Trail, *point)

	dailyDist := math.Round(session.DailyDistance*100) / 100
	totalDist := math.Round(session.TotalDistance*100) / 100
	pointCount := session.PointCount

	ac := session.AdminConn
	session.mutex.Unlock()

	// Relay to admin
	if ac != nil {
		payload, _ := json.Marshal(fiber.Map{
			"point":             point,
			"daily_distance_km": dailyDist,
			"total_distance_km": totalDist,
			"point_count":       pointCount,
		})
		ac.SetWriteDeadline(time.Now().Add(trackingWriteDeadline))
		ac.WriteJSON(TrackingSignalMessage{Type: "location_update", Payload: payload})
		ac.SetWriteDeadline(time.Time{})
	}
}

// ==================== Haversine distance ====================

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKm * c
}

func toRad(deg float64) float64 {
	return deg * math.Pi / 180
}
