package handlers

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/mdm-system/backend/internal/config"
	"github.com/mdm-system/backend/internal/models"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// StreamingHandler handles JPEG-over-WebSocket screen streaming + input relay.
// Device sends binary JPEG frames → backend relays to viewer.
// Viewer sends JSON input events → backend relays to device.
type StreamingHandler struct {
	cfg      *config.Config
	sessions *SessionManager
}

// StreamSession represents an active streaming session
type StreamSession struct {
	ID         string          `json:"id"`
	DeviceID   string          `json:"device_id"`
	ViewerID   string          `json:"viewer_id"`
	Status     string          `json:"status"` // waiting, connecting, streaming, ended
	CreatedAt  time.Time       `json:"created_at"`
	DeviceConn *websocket.Conn `json:"-"`
	ViewerConn *websocket.Conn `json:"-"`
	mutex      sync.RWMutex
	Quality    string `json:"quality"` // low, medium, high, auto
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	FPS        int    `json:"fps"`
	FrameCount int64  `json:"frame_count"`
}

// SessionManager manages active streaming sessions
type SessionManager struct {
	sessions map[string]*StreamSession
	byDevice map[string]*StreamSession
	mutex    sync.RWMutex
}

// SignalMessage represents a JSON control message
type SignalMessage struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id,omitempty"`
	DeviceID  string          `json:"device_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// InputEvent represents input from viewer to device
type InputEvent struct {
	Type      string  `json:"type"`   // touch, key, scroll
	Action    string  `json:"action"` // down, up, move, tap, long_press, swipe
	X         float64 `json:"x,omitempty"`
	Y         float64 `json:"y,omitempty"`
	KeyCode   int     `json:"key_code,omitempty"`
	Character string  `json:"character,omitempty"`
	DeltaX    float64 `json:"delta_x,omitempty"`
	DeltaY    float64 `json:"delta_y,omitempty"`
}

// StreamingConfig defines streaming quality settings sent to device
type StreamingConfig struct {
	Quality     string `json:"quality"`
	MaxDim      int    `json:"max_dim"`
	JpegQuality int    `json:"jpeg_quality"`
	IntervalMs  int    `json:"interval_ms"`
}

func NewStreamingHandler(cfg *config.Config) *StreamingHandler {
	return &StreamingHandler{
		cfg: cfg,
		sessions: &SessionManager{
			sessions: make(map[string]*StreamSession),
			byDevice: make(map[string]*StreamSession),
		},
	}
}

func (h *StreamingHandler) RegisterRoutes(app *fiber.App) {
	streaming := app.Group("/api/v1/streaming")

	streaming.Post("/sessions", h.CreateSession)
	streaming.Get("/sessions/:id", h.GetSession)
	streaming.Delete("/sessions/:id", h.EndSession)
	streaming.Get("/sessions", h.ListSessions)

	// WebSocket endpoint for dashboard viewer
	app.Get("/ws/viewer/:session_id", h.upgradeWebSocket, websocket.New(h.ViewerWebSocket))
}

// RegisterDeviceWS sets up WebSocket for Android device
func (h *StreamingHandler) RegisterDeviceWS(app *fiber.App) {
	app.Get("/ws/device/:session_id", h.upgradeWebSocket, websocket.New(h.DeviceWebSocket))
}

func (h *StreamingHandler) upgradeWebSocket(c *fiber.Ctx) error {
	if websocket.IsWebSocketUpgrade(c) {
		c.Locals("allowed", true)
		return c.Next()
	}
	return fiber.ErrUpgradeRequired
}

// CreateSession creates a new streaming session
func (h *StreamingHandler) CreateSession(c *fiber.Ctx) error {
	var req struct {
		DeviceID string `json:"device_id"`
		Quality  string `json:"quality"`
		ViewerID string `json:"viewer_id"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}

	if req.Quality == "" {
		req.Quality = "auto"
	}
	if req.ViewerID == "" {
		req.ViewerID = "admin"
	}

	// Check if device already has active session
	h.sessions.mutex.RLock()
	existing := h.sessions.byDevice[req.DeviceID]
	h.sessions.mutex.RUnlock()

	if existing != nil && existing.Status != "ended" {
		// End previous session
		h.sessions.mutex.Lock()
		existing.Status = "ended"
		delete(h.sessions.byDevice, existing.DeviceID)
		if existing.DeviceConn != nil {
			existing.DeviceConn.Close()
		}
		if existing.ViewerConn != nil {
			existing.ViewerConn.Close()
		}
		h.sessions.mutex.Unlock()
	}

	session := &StreamSession{
		ID:        uuid.New().String(),
		DeviceID:  req.DeviceID,
		ViewerID:  req.ViewerID,
		Status:    "waiting",
		CreatedAt: time.Now(),
		Quality:   req.Quality,
	}

	h.sessions.mutex.Lock()
	h.sessions.sessions[session.ID] = session
	h.sessions.byDevice[req.DeviceID] = session
	h.sessions.mutex.Unlock()

	// Session expires after 5 minutes if not connected
	go func() {
		time.Sleep(5 * time.Minute)
		h.sessions.mutex.Lock()
		defer h.sessions.mutex.Unlock()
		if s, ok := h.sessions.sessions[session.ID]; ok && s.Status == "waiting" {
			s.Status = "ended"
			delete(h.sessions.byDevice, s.DeviceID)
		}
	}()

	return c.Status(201).JSON(models.SuccessResponse(fiber.Map{
		"session_id": session.ID,
		"device_id":  session.DeviceID,
		"status":     session.Status,
		"ws_url":     "/ws/viewer/" + session.ID,
	}))
}

// GetSession returns session status
func (h *StreamingHandler) GetSession(c *fiber.Ctx) error {
	sessionID := c.Params("id")

	h.sessions.mutex.RLock()
	session := h.sessions.sessions[sessionID]
	h.sessions.mutex.RUnlock()

	if session == nil {
		return c.Status(404).JSON(fiber.Map{"error": "session not found"})
	}

	return c.JSON(fiber.Map{
		"session_id":  session.ID,
		"device_id":   session.DeviceID,
		"status":      session.Status,
		"quality":     session.Quality,
		"width":       session.Width,
		"height":      session.Height,
		"frame_count": session.FrameCount,
		"created_at":  session.CreatedAt,
	})
}

// EndSession terminates a streaming session
func (h *StreamingHandler) EndSession(c *fiber.Ctx) error {
	sessionID := c.Params("id")

	h.sessions.mutex.Lock()
	session := h.sessions.sessions[sessionID]
	if session != nil {
		session.Status = "ended"
		delete(h.sessions.byDevice, session.DeviceID)

		if session.DeviceConn != nil {
			session.DeviceConn.Close()
		}
		if session.ViewerConn != nil {
			session.ViewerConn.Close()
		}
	}
	h.sessions.mutex.Unlock()

	if session == nil {
		return c.Status(404).JSON(fiber.Map{"error": "session not found"})
	}

	return c.JSON(fiber.Map{"status": "ended"})
}

// ListSessions lists active streaming sessions
func (h *StreamingHandler) ListSessions(c *fiber.Ctx) error {
	h.sessions.mutex.RLock()
	defer h.sessions.mutex.RUnlock()

	sessions := make([]fiber.Map, 0)
	for _, s := range h.sessions.sessions {
		if s.Status != "ended" {
			sessions = append(sessions, fiber.Map{
				"session_id": s.ID,
				"device_id":  s.DeviceID,
				"status":     s.Status,
				"quality":    s.Quality,
				"created_at": s.CreatedAt,
			})
		}
	}

	return c.JSON(sessions)
}

// ViewerWebSocket handles WebSocket connection from dashboard viewer.
// Reads JSON messages (input events) from viewer, forwards to device.
// Also watches for binary frames from device (via DeviceWebSocket) for relay.
func (h *StreamingHandler) ViewerWebSocket(c *websocket.Conn) {
	sessionID := c.Params("session_id")

	h.sessions.mutex.Lock()
	session := h.sessions.sessions[sessionID]
	if session == nil || session.Status == "ended" {
		h.sessions.mutex.Unlock()
		c.WriteJSON(SignalMessage{Type: "error", Payload: json.RawMessage(`"session not found"`)})
		c.Close()
		return
	}
	session.ViewerConn = c
	h.sessions.mutex.Unlock()

	log.Printf("Viewer connected to session %s", sessionID)

	// Send session info to viewer
	c.WriteJSON(SignalMessage{
		Type:      "session_info",
		SessionID: sessionID,
		DeviceID:  session.DeviceID,
	})

	defer func() {
		h.sessions.mutex.Lock()
		if session.ViewerConn == c {
			session.ViewerConn = nil
		}
		h.sessions.mutex.Unlock()
		c.Close()
	}()

	// Viewer only sends JSON text messages (input events, quality changes)
	for {
		msgType, data, err := c.ReadMessage()
		if err != nil {
			log.Printf("Viewer read error: %v", err)
			break
		}

		if msgType == fws.TextMessage {
			var msg SignalMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				log.Printf("Viewer JSON parse error: %v", err)
				continue
			}
			h.handleViewerMessage(session, &msg)
		}
	}
}

// DeviceWebSocket handles WebSocket connection from Android device.
// Reads both:
//   - Binary frames (JPEG screenshots) → relays to viewer as binary
//   - JSON messages (streaming_started, streaming_stopped) → relays to viewer as JSON
func (h *StreamingHandler) DeviceWebSocket(c *websocket.Conn) {
	sessionID := c.Params("session_id")

	h.sessions.mutex.Lock()
	session := h.sessions.sessions[sessionID]
	if session == nil || session.Status == "ended" {
		h.sessions.mutex.Unlock()
		c.WriteJSON(SignalMessage{Type: "error", Payload: json.RawMessage(`"session not found"`)})
		c.Close()
		return
	}
	session.DeviceConn = c
	session.Status = "connecting"
	h.sessions.mutex.Unlock()

	log.Printf("Device connected to session %s", sessionID)

	// Send quality config to device
	cfg := h.getStreamingConfig(session.Quality)
	cfgJSON, _ := json.Marshal(cfg)
	c.WriteJSON(SignalMessage{
		Type:    "config",
		Payload: cfgJSON,
	})

	defer func() {
		h.sessions.mutex.Lock()
		if session.DeviceConn == c {
			session.DeviceConn = nil
			session.Status = "ended"
			delete(h.sessions.byDevice, session.DeviceID)
		}
		h.sessions.mutex.Unlock()

		// Notify viewer that streaming ended
		session.mutex.RLock()
		vc := session.ViewerConn
		session.mutex.RUnlock()
		if vc != nil {
			vc.WriteJSON(SignalMessage{Type: "streaming_stopped"})
		}

		c.Close()
	}()

	for {
		msgType, data, err := c.ReadMessage()
		if err != nil {
			log.Printf("Device read error: %v", err)
			break
		}

		switch msgType {
		case fws.BinaryMessage:
			// Binary frame = JPEG screenshot from device → relay to viewer
			session.mutex.Lock()
			session.FrameCount++
			session.mutex.Unlock()

			session.mutex.RLock()
			viewerConn := session.ViewerConn
			session.mutex.RUnlock()

			if viewerConn != nil {
				if err := viewerConn.WriteMessage(fws.BinaryMessage, data); err != nil {
					log.Printf("Failed to relay frame to viewer: %v", err)
				}
			}

		case fws.TextMessage:
			// JSON control message from device
			var msg SignalMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				log.Printf("Device JSON parse error: %v", err)
				continue
			}
			h.handleDeviceMessage(session, &msg)
		}
	}
}

func (h *StreamingHandler) handleViewerMessage(session *StreamSession, msg *SignalMessage) {
	session.mutex.RLock()
	deviceConn := session.DeviceConn
	session.mutex.RUnlock()

	if deviceConn == nil {
		return
	}

	switch msg.Type {
	case "input":
		// Forward input events to device as JSON
		deviceConn.WriteJSON(msg)

	case "quality_change":
		// Update quality and notify device
		var quality string
		json.Unmarshal(msg.Payload, &quality)
		session.mutex.Lock()
		session.Quality = quality
		session.mutex.Unlock()

		cfg := h.getStreamingConfig(quality)
		cfgJSON, _ := json.Marshal(cfg)
		deviceConn.WriteJSON(SignalMessage{
			Type:    "config",
			Payload: cfgJSON,
		})
	}
}

func (h *StreamingHandler) handleDeviceMessage(session *StreamSession, msg *SignalMessage) {
	session.mutex.RLock()
	viewerConn := session.ViewerConn
	session.mutex.RUnlock()

	switch msg.Type {
	case "streaming_started":
		session.mutex.Lock()
		session.Status = "streaming"
		var dimensions struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		}
		json.Unmarshal(msg.Payload, &dimensions)
		session.Width = dimensions.Width
		session.Height = dimensions.Height
		session.mutex.Unlock()

		log.Printf("Streaming started for session %s (%dx%d)", session.ID, dimensions.Width, dimensions.Height)

		if viewerConn != nil {
			viewerConn.WriteJSON(msg)
		}

	case "streaming_stopped":
		session.mutex.Lock()
		session.Status = "ended"
		session.mutex.Unlock()

		if viewerConn != nil {
			viewerConn.WriteJSON(msg)
		}
	}
}

func (h *StreamingHandler) getStreamingConfig(quality string) StreamingConfig {
	switch quality {
	case "low":
		return StreamingConfig{
			Quality:     "low",
			MaxDim:      480,
			JpegQuality: 40,
			IntervalMs:  800,
		}
	case "medium":
		return StreamingConfig{
			Quality:     "medium",
			MaxDim:      720,
			JpegQuality: 60,
			IntervalMs:  500,
		}
	case "high":
		return StreamingConfig{
			Quality:     "high",
			MaxDim:      1080,
			JpegQuality: 80,
			IntervalMs:  300,
		}
	default: // auto
		return StreamingConfig{
			Quality:     "auto",
			MaxDim:      720,
			JpegQuality: 60,
			IntervalMs:  500,
		}
	}
}

// GetSessionByDevice retrieves active session for a device
func (h *StreamingHandler) GetSessionByDevice(ctx context.Context, deviceID string) *StreamSession {
	h.sessions.mutex.RLock()
	defer h.sessions.mutex.RUnlock()
	return h.sessions.byDevice[deviceID]
}
