package handlers

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/mdm-system/backend/internal/config"
	"github.com/mdm-system/backend/internal/models"
	"github.com/mdm-system/backend/internal/repository"
	"github.com/mdm-system/backend/internal/services"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// ==================== Constants for 2G/3G optimization ====================

const (
	// Write deadline for device WebSocket writes. A slow 2G device that can't
	// accept audio within this window is skipped for that chunk (non-blocking).
	deviceWriteDeadline = 500 * time.Millisecond

	// Admin WebSocket write deadline (dashboard is usually on good WiFi/broadband).
	adminWriteDeadline = 2 * time.Second

	// Session auto-expire timeout for idle broadcasts.
	broadcastIdleTimeout = 15 * time.Minute

	// Single session auto-expire timeout.
	singleSessionTimeout = 5 * time.Minute

	// Maximum audio chunk size in bytes. Anything larger gets dropped.
	// 8kHz 16-bit mono, 2048 samples = 4096 bytes per chunk.
	maxAudioChunkBytes = 8192

	// WebSocket ping interval for keeping alive on flaky mobile networks.
	wsPingInterval = 10 * time.Second
)

// ==================== Handler ====================

type AudioHandler struct {
	cfg            *config.Config
	sessions       *AudioSessionManager
	broadcasts     *BroadcastSessionManager
	listenSessions *ListenSessionManager
	commandService *services.CommandService
	deviceRepo     *repository.DeviceRepository
}

// ==================== Single-device session ====================

type AudioSession struct {
	ID         string          `json:"id"`
	DeviceID   string          `json:"device_id"`
	AdminID    string          `json:"admin_id"`
	Status     string          `json:"status"`
	CreatedAt  time.Time       `json:"created_at"`
	AdminConn  *websocket.Conn `json:"-"`
	DeviceConn *websocket.Conn `json:"-"`
	mutex      sync.RWMutex
	BytesSent  int64 `json:"bytes_sent"`
}

type AudioSessionManager struct {
	sessions map[string]*AudioSession
	byDevice map[string]*AudioSession
	mutex    sync.RWMutex
}

// ==================== Broadcast session ====================

type BroadcastSession struct {
	ID             string                     `json:"id"`
	AdminID        string                     `json:"admin_id"`
	TargetType     string                     `json:"target_type"`
	TargetID       string                     `json:"target_id,omitempty"`
	Status         string                     `json:"status"`
	CreatedAt      time.Time                  `json:"created_at"`
	AdminConn      *websocket.Conn            `json:"-"`
	DeviceConns    map[string]*websocket.Conn `json:"-"`
	DeviceSessions map[string]string          `json:"-"`
	mutex          sync.RWMutex
	BytesSent      int64 `json:"bytes_sent"`
	DeviceCount    int   `json:"device_count"`
	ConnectedCount int   `json:"connected_count"`
	SkippedDevices int   `json:"skipped_devices"` // offline devices not targeted
}

type BroadcastSessionManager struct {
	sessions map[string]*BroadcastSession
	mutex    sync.RWMutex
}

// ==================== Listen session (device mic → admin speaker) ====================

type ListenSession struct {
	ID            string          `json:"id"`
	DeviceID      string          `json:"device_id"`
	AdminID       string          `json:"admin_id"`
	Status        string          `json:"status"`
	CreatedAt     time.Time       `json:"created_at"`
	AdminConn     *websocket.Conn `json:"-"`
	DeviceConn    *websocket.Conn `json:"-"`
	mutex         sync.RWMutex
	BytesReceived int64 `json:"bytes_received"`
}

type ListenSessionManager struct {
	sessions map[string]*ListenSession
	byDevice map[string]*ListenSession
	mutex    sync.RWMutex
}

type AudioSignalMessage struct {
	Type      string          `json:"type"`
	SessionID string          `json:"session_id,omitempty"`
	DeviceID  string          `json:"device_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

func NewAudioHandler(cfg *config.Config, commandService *services.CommandService, deviceRepo *repository.DeviceRepository) *AudioHandler {
	return &AudioHandler{
		cfg:            cfg,
		commandService: commandService,
		deviceRepo:     deviceRepo,
		sessions: &AudioSessionManager{
			sessions: make(map[string]*AudioSession),
			byDevice: make(map[string]*AudioSession),
		},
		broadcasts: &BroadcastSessionManager{
			sessions: make(map[string]*BroadcastSession),
		},
		listenSessions: &ListenSessionManager{
			sessions: make(map[string]*ListenSession),
			byDevice: make(map[string]*ListenSession),
		},
	}
}

func (h *AudioHandler) RegisterRoutes(app *fiber.App) {
	audio := app.Group("/api/v1/audio")

	audio.Post("/sessions", h.CreateSession)
	audio.Get("/sessions/:id", h.GetSession)
	audio.Delete("/sessions/:id", h.EndSession)

	audio.Post("/broadcast", h.CreateBroadcast)
	audio.Get("/broadcast/:id", h.GetBroadcast)
	audio.Delete("/broadcast/:id", h.EndBroadcast)

	audio.Post("/listen", h.CreateListenSession)
	audio.Get("/listen/:id", h.GetListenSession)
	audio.Delete("/listen/:id", h.EndListenSession)

	app.Get("/ws/audio/admin/:session_id", h.upgradeWebSocket, websocket.New(h.AdminWebSocket))
	app.Get("/ws/audio/device/:session_id", h.upgradeWebSocket, websocket.New(h.DeviceWebSocket))
	app.Get("/ws/audio/broadcast/:session_id", h.upgradeWebSocket, websocket.New(h.BroadcastAdminWebSocket))
	app.Get("/ws/audio/listen/admin/:session_id", h.upgradeWebSocket, websocket.New(h.ListenAdminWebSocket))
	app.Get("/ws/audio/listen/device/:session_id", h.upgradeWebSocket, websocket.New(h.ListenDeviceWebSocket))
}

func (h *AudioHandler) upgradeWebSocket(c *fiber.Ctx) error {
	if websocket.IsWebSocketUpgrade(c) {
		c.Locals("allowed", true)
		return c.Next()
	}
	return fiber.ErrUpgradeRequired
}

// ==================== Helper: safe non-blocking write ====================

// safeWriteBinary writes a binary message with a deadline. Returns false if write
// fails (connection dead or too slow). This prevents one slow 2G device from
// blocking the fan-out loop.
func safeWriteBinary(conn *websocket.Conn, data []byte, deadline time.Duration) bool {
	if conn == nil {
		return false
	}
	conn.SetWriteDeadline(time.Now().Add(deadline))
	err := conn.WriteMessage(fws.BinaryMessage, data)
	conn.SetWriteDeadline(time.Time{}) // clear deadline
	if err != nil {
		return false
	}
	return true
}

// safeWriteJSON writes a JSON message with a deadline. Returns false on failure.
func safeWriteJSON(conn *websocket.Conn, v interface{}, deadline time.Duration) bool {
	if conn == nil {
		return false
	}
	conn.SetWriteDeadline(time.Now().Add(deadline))
	err := conn.WriteJSON(v)
	conn.SetWriteDeadline(time.Time{})
	if err != nil {
		return false
	}
	return true
}

// ==================== Single-device endpoints ====================

func (h *AudioHandler) CreateSession(c *fiber.Ctx) error {
	var req struct {
		DeviceID string `json:"device_id"`
		AdminID  string `json:"admin_id"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.AdminID == "" {
		req.AdminID = "admin"
	}

	// Clean up any existing session for this device
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

	session := &AudioSession{
		ID:        uuid.New().String(),
		DeviceID:  req.DeviceID,
		AdminID:   req.AdminID,
		Status:    "waiting",
		CreatedAt: time.Now(),
	}

	h.sessions.mutex.Lock()
	h.sessions.sessions[session.ID] = session
	h.sessions.byDevice[req.DeviceID] = session
	h.sessions.mutex.Unlock()

	// Auto-expire
	go func() {
		time.Sleep(singleSessionTimeout)
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
		"ws_url":     "/ws/audio/admin/" + session.ID,
	}))
}

func (h *AudioHandler) GetSession(c *fiber.Ctx) error {
	sessionID := c.Params("id")
	h.sessions.mutex.RLock()
	session := h.sessions.sessions[sessionID]
	h.sessions.mutex.RUnlock()
	if session == nil {
		return c.Status(404).JSON(fiber.Map{"error": "session not found"})
	}
	return c.JSON(fiber.Map{
		"session_id": session.ID,
		"device_id":  session.DeviceID,
		"status":     session.Status,
		"bytes_sent": session.BytesSent,
		"created_at": session.CreatedAt,
	})
}

func (h *AudioHandler) EndSession(c *fiber.Ctx) error {
	sessionID := c.Params("id")
	h.sessions.mutex.Lock()
	session := h.sessions.sessions[sessionID]
	if session != nil {
		session.Status = "ended"
		delete(h.sessions.byDevice, session.DeviceID)
		if session.AdminConn != nil {
			session.AdminConn.Close()
		}
		if session.DeviceConn != nil {
			session.DeviceConn.Close()
		}
	}
	h.sessions.mutex.Unlock()
	if session == nil {
		return c.Status(404).JSON(fiber.Map{"error": "session not found"})
	}
	return c.JSON(fiber.Map{"status": "ended"})
}

// ==================== Broadcast endpoints ====================

// CreateBroadcast creates a broadcast session targeting ONLY online devices.
// Offline/pending devices are silently skipped — they won't stall the broadcast.
func (h *AudioHandler) CreateBroadcast(c *fiber.Ctx) error {
	var req struct {
		TargetType      string `json:"target_type"`
		GroupID         string `json:"group_id,omitempty"`
		EnrollmentToken string `json:"enrollment_token,omitempty"`
		AdminID         string `json:"admin_id"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.AdminID == "" {
		req.AdminID = "admin"
	}
	if req.TargetType != "group" && req.TargetType != "all" && req.TargetType != "enrollment" {
		return c.Status(400).JSON(fiber.Map{"error": "target_type must be 'all', 'group', or 'enrollment'"})
	}

	orgID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	// ──── Resolve ONLINE devices only ────
	// Offline/pending devices are excluded so they don't receive commands
	// they can't act on and don't inflate the "expected" device count.
	var onlineIDs []uuid.UUID
	var allIDs []uuid.UUID
	var err error

	switch req.TargetType {
	case "group":
		if req.GroupID == "" {
			return c.Status(400).JSON(fiber.Map{"error": "group_id required for target_type 'group'"})
		}
		groupUUID, parseErr := uuid.Parse(req.GroupID)
		if parseErr != nil {
			return c.Status(400).JSON(fiber.Map{"error": "invalid group_id"})
		}
		allIDs, err = h.deviceRepo.GetIDsByGroupID(c.Context(), groupUUID)
		if err != nil {
			log.Printf("Error resolving group targets: %v", err)
			return c.Status(500).JSON(fiber.Map{"error": "failed to resolve target devices"})
		}
		onlineIDs, err = h.deviceRepo.GetOnlineIDsByGroupID(c.Context(), groupUUID)
	case "enrollment":
		if req.EnrollmentToken == "" {
			return c.Status(400).JSON(fiber.Map{"error": "enrollment_token required for target_type 'enrollment'"})
		}
		allIDs, err = h.deviceRepo.GetIDsByEnrollmentToken(c.Context(), req.EnrollmentToken)
		if err != nil {
			log.Printf("Error resolving enrollment targets: %v", err)
			return c.Status(500).JSON(fiber.Map{"error": "failed to resolve target devices"})
		}
		onlineIDs, err = h.deviceRepo.GetOnlineIDsByEnrollmentToken(c.Context(), req.EnrollmentToken)
	case "all":
		allIDs, err = h.deviceRepo.GetAllIDs(c.Context(), orgID)
		if err != nil {
			log.Printf("Error resolving all targets: %v", err)
			return c.Status(500).JSON(fiber.Map{"error": "failed to resolve target devices"})
		}
		onlineIDs, err = h.deviceRepo.GetOnlineIDs(c.Context(), orgID)
	}

	if err != nil {
		log.Printf("Error resolving online broadcast targets: %v", err)
		return c.Status(500).JSON(fiber.Map{"error": "failed to resolve online devices"})
	}

	if len(onlineIDs) == 0 {
		return c.Status(400).JSON(fiber.Map{
			"error":          "no online devices found for target",
			"total_devices":  len(allIDs),
			"online_devices": 0,
		})
	}

	skipped := len(allIDs) - len(onlineIDs)
	broadcastID := uuid.New().String()

	broadcast := &BroadcastSession{
		ID:             broadcastID,
		AdminID:        req.AdminID,
		TargetType:     req.TargetType,
		TargetID:       req.GroupID,
		Status:         "waiting",
		CreatedAt:      time.Now(),
		DeviceConns:    make(map[string]*websocket.Conn),
		DeviceSessions: make(map[string]string),
		DeviceCount:    len(onlineIDs),
		SkippedDevices: skipped,
	}

	for _, did := range onlineIDs {
		perDeviceSessionID := broadcastID + ":" + did.String()
		broadcast.DeviceSessions[did.String()] = perDeviceSessionID
	}

	h.broadcasts.mutex.Lock()
	h.broadcasts.sessions[broadcastID] = broadcast
	h.broadcasts.mutex.Unlock()

	// Send START_AUDIO commands — fire-and-forget, don't block on slow MQTT
	go func() {
		for _, did := range onlineIDs {
			perDeviceSessionID := broadcastID + ":" + did.String()
			payload := map[string]interface{}{
				"session_id":   perDeviceSessionID,
				"broadcast_id": broadcastID,
			}
			cmdReq := &models.CreateCommandRequest{
				CommandType: "START_AUDIO",
				Payload:     payload,
			}
			_, cmdErr := h.commandService.CreateCommand(c.Context(), did, cmdReq, nil)
			if cmdErr != nil {
				log.Printf("Failed to send START_AUDIO to device %s: %v", did, cmdErr)
			}
		}
		log.Printf("Broadcast %s: sent START_AUDIO to %d online devices (%d offline skipped)",
			broadcastID, len(onlineIDs), skipped)
	}()

	// Auto-expire
	go func() {
		time.Sleep(broadcastIdleTimeout)
		h.broadcasts.mutex.Lock()
		defer h.broadcasts.mutex.Unlock()
		if s, ok := h.broadcasts.sessions[broadcastID]; ok && s.Status == "waiting" {
			s.Status = "ended"
		}
	}()

	return c.Status(201).JSON(models.SuccessResponse(fiber.Map{
		"broadcast_id":    broadcastID,
		"target_type":     req.TargetType,
		"group_id":        req.GroupID,
		"device_count":    len(onlineIDs),
		"skipped_offline": skipped,
		"status":          "waiting",
		"ws_url":          "/ws/audio/broadcast/" + broadcastID,
	}))
}

func (h *AudioHandler) GetBroadcast(c *fiber.Ctx) error {
	broadcastID := c.Params("id")
	h.broadcasts.mutex.RLock()
	b := h.broadcasts.sessions[broadcastID]
	h.broadcasts.mutex.RUnlock()
	if b == nil {
		return c.Status(404).JSON(fiber.Map{"error": "broadcast session not found"})
	}
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	return c.JSON(fiber.Map{
		"broadcast_id":    b.ID,
		"target_type":     b.TargetType,
		"target_id":       b.TargetID,
		"status":          b.Status,
		"device_count":    b.DeviceCount,
		"connected_count": b.ConnectedCount,
		"skipped_offline": b.SkippedDevices,
		"bytes_sent":      b.BytesSent,
		"created_at":      b.CreatedAt,
	})
}

func (h *AudioHandler) EndBroadcast(c *fiber.Ctx) error {
	broadcastID := c.Params("id")
	h.broadcasts.mutex.Lock()
	b := h.broadcasts.sessions[broadcastID]
	if b != nil {
		b.Status = "ended"
		if b.AdminConn != nil {
			b.AdminConn.Close()
		}
		b.mutex.RLock()
		for _, dc := range b.DeviceConns {
			if dc != nil {
				dc.Close()
			}
		}
		b.mutex.RUnlock()
	}
	h.broadcasts.mutex.Unlock()
	if b == nil {
		return c.Status(404).JSON(fiber.Map{"error": "broadcast session not found"})
	}
	return c.JSON(fiber.Map{"status": "ended"})
}

// ==================== Single-device WebSocket handlers ====================

func (h *AudioHandler) AdminWebSocket(c *websocket.Conn) {
	sessionID := c.Params("session_id")
	h.sessions.mutex.Lock()
	session := h.sessions.sessions[sessionID]
	if session == nil || session.Status == "ended" {
		h.sessions.mutex.Unlock()
		safeWriteJSON(c, AudioSignalMessage{Type: "error", Payload: json.RawMessage(`"session not found"`)}, adminWriteDeadline)
		c.Close()
		return
	}
	session.AdminConn = c
	h.sessions.mutex.Unlock()

	log.Printf("Admin connected to audio session %s", sessionID)
	safeWriteJSON(c, AudioSignalMessage{Type: "session_info", SessionID: sessionID, DeviceID: session.DeviceID}, adminWriteDeadline)

	defer func() {
		h.sessions.mutex.Lock()
		if session.AdminConn == c {
			session.AdminConn = nil
		}
		h.sessions.mutex.Unlock()
		c.Close()
	}()

	for {
		msgType, data, err := c.ReadMessage()
		if err != nil {
			break
		}

		switch msgType {
		case fws.BinaryMessage:
			// Drop oversized chunks
			if len(data) > maxAudioChunkBytes {
				continue
			}

			session.mutex.Lock()
			session.BytesSent += int64(len(data))
			session.mutex.Unlock()

			session.mutex.RLock()
			dc := session.DeviceConn
			session.mutex.RUnlock()

			if dc != nil {
				// Non-blocking write with deadline — if device is on slow
				// network, skip this chunk rather than blocking admin
				if !safeWriteBinary(dc, data, deviceWriteDeadline) {
					log.Printf("Audio session %s: device write timeout, chunk dropped", sessionID)
				}
			}

		case fws.TextMessage:
			var msg AudioSignalMessage
			if json.Unmarshal(data, &msg) == nil && msg.Type == "stop" {
				session.mutex.RLock()
				dc := session.DeviceConn
				session.mutex.RUnlock()
				if dc != nil {
					safeWriteJSON(dc, AudioSignalMessage{Type: "audio_stopped"}, deviceWriteDeadline)
				}
			}
		}
	}
}

func (h *AudioHandler) DeviceWebSocket(c *websocket.Conn) {
	sessionID := c.Params("session_id")

	// Check broadcast session first (format: broadcastID:deviceUUID)
	if h.tryBroadcastDeviceConnect(c, sessionID) {
		return
	}

	// Single-device session
	h.sessions.mutex.Lock()
	session := h.sessions.sessions[sessionID]
	if session == nil || session.Status == "ended" {
		h.sessions.mutex.Unlock()
		safeWriteJSON(c, AudioSignalMessage{Type: "error", Payload: json.RawMessage(`"session not found"`)}, deviceWriteDeadline)
		c.Close()
		return
	}
	session.DeviceConn = c
	session.Status = "streaming"
	h.sessions.mutex.Unlock()

	log.Printf("Device connected to audio session %s", sessionID)

	// Notify admin
	session.mutex.RLock()
	ac := session.AdminConn
	session.mutex.RUnlock()
	if ac != nil {
		safeWriteJSON(ac, AudioSignalMessage{Type: "device_ready"}, adminWriteDeadline)
	}

	defer func() {
		h.sessions.mutex.Lock()
		if session.DeviceConn == c {
			session.DeviceConn = nil
			session.Status = "ended"
			delete(h.sessions.byDevice, session.DeviceID)
		}
		h.sessions.mutex.Unlock()

		session.mutex.RLock()
		ac := session.AdminConn
		session.mutex.RUnlock()
		if ac != nil {
			safeWriteJSON(ac, AudioSignalMessage{Type: "device_disconnected"}, adminWriteDeadline)
		}
		c.Close()
	}()

	for {
		msgType, data, err := c.ReadMessage()
		if err != nil {
			break
		}
		if msgType == fws.TextMessage {
			var msg AudioSignalMessage
			if json.Unmarshal(data, &msg) == nil {
				session.mutex.RLock()
				ac := session.AdminConn
				session.mutex.RUnlock()
				if ac != nil {
					safeWriteJSON(ac, msg, adminWriteDeadline)
				}
			}
		}
	}
}

// ==================== Broadcast WebSocket handlers ====================

// BroadcastAdminWebSocket handles the admin side. Binary audio from the admin
// mic is fanned out to every connected device WebSocket. Each device write uses
// a tight deadline so one slow 2G device can't stall the entire fan-out.
func (h *AudioHandler) BroadcastAdminWebSocket(c *websocket.Conn) {
	broadcastID := c.Params("session_id")

	h.broadcasts.mutex.Lock()
	b := h.broadcasts.sessions[broadcastID]
	if b == nil || b.Status == "ended" {
		h.broadcasts.mutex.Unlock()
		safeWriteJSON(c, AudioSignalMessage{Type: "error", Payload: json.RawMessage(`"broadcast not found"`)}, adminWriteDeadline)
		c.Close()
		return
	}
	b.AdminConn = c
	h.broadcasts.mutex.Unlock()

	log.Printf("Admin connected to broadcast %s (%d online devices, %d offline skipped)",
		broadcastID, b.DeviceCount, b.SkippedDevices)

	infoPayload, _ := json.Marshal(fiber.Map{
		"device_count":    b.DeviceCount,
		"connected_count": b.ConnectedCount,
		"skipped_offline": b.SkippedDevices,
	})
	safeWriteJSON(c, AudioSignalMessage{Type: "broadcast_info", SessionID: broadcastID, Payload: infoPayload}, adminWriteDeadline)

	defer func() {
		h.broadcasts.mutex.Lock()
		if b.AdminConn == c {
			b.AdminConn = nil
		}
		h.broadcasts.mutex.Unlock()
		c.Close()
	}()

	// Track consecutive dropped-frame counts per device to detect dead connections
	dropCounts := make(map[string]int)

	for {
		msgType, data, err := c.ReadMessage()
		if err != nil {
			log.Printf("Broadcast admin read error: %v", err)
			break
		}

		switch msgType {
		case fws.BinaryMessage:
			// Drop oversized chunks
			if len(data) > maxAudioChunkBytes {
				continue
			}

			b.mutex.Lock()
			b.BytesSent += int64(len(data))
			b.mutex.Unlock()

			// Fan-out to all connected devices with non-blocking writes.
			// Snapshot the map under read lock, then write outside lock so
			// a slow device doesn't hold the mutex.
			b.mutex.RLock()
			targets := make(map[string]*websocket.Conn, len(b.DeviceConns))
			for k, v := range b.DeviceConns {
				targets[k] = v
			}
			b.mutex.RUnlock()

			var disconnected []string
			for devID, dc := range targets {
				if !safeWriteBinary(dc, data, deviceWriteDeadline) {
					dropCounts[devID]++
					// After 10 consecutive failed writes, consider device dead
					if dropCounts[devID] >= 10 {
						disconnected = append(disconnected, devID)
						log.Printf("Broadcast %s: device %s unresponsive, disconnecting", broadcastID, devID)
					}
				} else {
					dropCounts[devID] = 0 // reset on success
				}
			}

			// Remove dead devices
			if len(disconnected) > 0 {
				b.mutex.Lock()
				for _, devID := range disconnected {
					if dc, ok := b.DeviceConns[devID]; ok {
						dc.Close()
						delete(b.DeviceConns, devID)
						b.ConnectedCount--
					}
					delete(dropCounts, devID)
				}
				connCount := b.ConnectedCount
				b.mutex.Unlock()

				// Notify admin of disconnections
				for _, devID := range disconnected {
					payload, _ := json.Marshal(fiber.Map{
						"device_id":       devID,
						"connected_count": connCount,
						"device_count":    b.DeviceCount,
						"reason":          "unresponsive",
					})
					safeWriteJSON(c, AudioSignalMessage{Type: "device_disconnected", Payload: payload}, adminWriteDeadline)
				}
			}

		case fws.TextMessage:
			var msg AudioSignalMessage
			if json.Unmarshal(data, &msg) == nil && msg.Type == "stop" {
				b.mutex.RLock()
				for _, dc := range b.DeviceConns {
					if dc != nil {
						safeWriteJSON(dc, AudioSignalMessage{Type: "audio_stopped"}, deviceWriteDeadline)
					}
				}
				b.mutex.RUnlock()
			}
		}
	}
}

// tryBroadcastDeviceConnect handles a device connecting to a broadcast session.
// Session IDs with format "broadcastID:deviceUUID" are broadcast per-device sessions.
func (h *AudioHandler) tryBroadcastDeviceConnect(c *websocket.Conn, sessionID string) bool {
	idx := -1
	for i := 0; i < len(sessionID); i++ {
		if sessionID[i] == ':' {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return false
	}

	broadcastID := sessionID[:idx]
	deviceUUID := sessionID[idx+1:]

	h.broadcasts.mutex.Lock()
	b := h.broadcasts.sessions[broadcastID]
	if b == nil || b.Status == "ended" {
		h.broadcasts.mutex.Unlock()
		return false
	}

	expected, ok := b.DeviceSessions[deviceUUID]
	if !ok || expected != sessionID {
		h.broadcasts.mutex.Unlock()
		return false
	}

	b.mutex.Lock()
	// If device already has a connection (reconnect), close old one gracefully
	if oldConn, exists := b.DeviceConns[deviceUUID]; exists && oldConn != nil {
		oldConn.Close()
		// Don't decrement ConnectedCount — we're replacing
	} else {
		b.ConnectedCount++
	}
	b.DeviceConns[deviceUUID] = c
	connCount := b.ConnectedCount
	if b.Status == "waiting" {
		b.Status = "streaming"
	}
	b.mutex.Unlock()
	h.broadcasts.mutex.Unlock()

	log.Printf("Device %s connected to broadcast %s (%d/%d)", deviceUUID, broadcastID, connCount, b.DeviceCount)

	// Notify admin
	b.mutex.RLock()
	ac := b.AdminConn
	b.mutex.RUnlock()
	if ac != nil {
		payload, _ := json.Marshal(fiber.Map{
			"device_id":       deviceUUID,
			"connected_count": connCount,
			"device_count":    b.DeviceCount,
		})
		safeWriteJSON(ac, AudioSignalMessage{Type: "device_connected", Payload: payload}, adminWriteDeadline)
	}

	defer func() {
		b.mutex.Lock()
		// Only decrement if this connection is still the active one
		if b.DeviceConns[deviceUUID] == c {
			delete(b.DeviceConns, deviceUUID)
			b.ConnectedCount--
		}
		remaining := b.ConnectedCount
		b.mutex.Unlock()

		b.mutex.RLock()
		ac := b.AdminConn
		b.mutex.RUnlock()
		if ac != nil {
			payload, _ := json.Marshal(fiber.Map{
				"device_id":       deviceUUID,
				"connected_count": remaining,
				"device_count":    b.DeviceCount,
			})
			safeWriteJSON(ac, AudioSignalMessage{Type: "device_disconnected", Payload: payload}, adminWriteDeadline)
		}
		c.Close()
	}()

	// Device read loop (for acks/status)
	for {
		msgType, data, err := c.ReadMessage()
		if err != nil {
			break
		}
		if msgType == fws.TextMessage {
			var msg AudioSignalMessage
			if json.Unmarshal(data, &msg) == nil {
				b.mutex.RLock()
				ac := b.AdminConn
				b.mutex.RUnlock()
				if ac != nil {
					msg.DeviceID = deviceUUID
					safeWriteJSON(ac, msg, adminWriteDeadline)
				}
			}
		}
	}

	return true
}

// ==================== Listen session endpoints (device mic → admin speaker) ====================

func (h *AudioHandler) CreateListenSession(c *fiber.Ctx) error {
	var req struct {
		DeviceID string `json:"device_id"`
		AdminID  string `json:"admin_id"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.DeviceID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "device_id required"})
	}
	if req.AdminID == "" {
		req.AdminID = "admin"
	}

	// Clean up any existing listen session for this device
	h.listenSessions.mutex.Lock()
	if existing := h.listenSessions.byDevice[req.DeviceID]; existing != nil && existing.Status != "ended" {
		existing.Status = "ended"
		delete(h.listenSessions.byDevice, existing.DeviceID)
		if existing.AdminConn != nil {
			existing.AdminConn.Close()
		}
		if existing.DeviceConn != nil {
			existing.DeviceConn.Close()
		}
	}
	h.listenSessions.mutex.Unlock()

	session := &ListenSession{
		ID:        uuid.New().String(),
		DeviceID:  req.DeviceID,
		AdminID:   req.AdminID,
		Status:    "waiting",
		CreatedAt: time.Now(),
	}

	h.listenSessions.mutex.Lock()
	h.listenSessions.sessions[session.ID] = session
	h.listenSessions.byDevice[req.DeviceID] = session
	h.listenSessions.mutex.Unlock()

	// Auto-expire
	go func() {
		time.Sleep(singleSessionTimeout)
		h.listenSessions.mutex.Lock()
		defer h.listenSessions.mutex.Unlock()
		if s, ok := h.listenSessions.sessions[session.ID]; ok && s.Status == "waiting" {
			s.Status = "ended"
			delete(h.listenSessions.byDevice, s.DeviceID)
		}
	}()

	return c.Status(201).JSON(models.SuccessResponse(fiber.Map{
		"session_id": session.ID,
		"device_id":  session.DeviceID,
		"status":     session.Status,
		"ws_url":     "/ws/audio/listen/admin/" + session.ID,
	}))
}

func (h *AudioHandler) GetListenSession(c *fiber.Ctx) error {
	sessionID := c.Params("id")
	h.listenSessions.mutex.RLock()
	session := h.listenSessions.sessions[sessionID]
	h.listenSessions.mutex.RUnlock()
	if session == nil {
		return c.Status(404).JSON(fiber.Map{"error": "session not found"})
	}
	return c.JSON(fiber.Map{
		"session_id":     session.ID,
		"device_id":      session.DeviceID,
		"status":         session.Status,
		"bytes_received": session.BytesReceived,
		"created_at":     session.CreatedAt,
	})
}

func (h *AudioHandler) EndListenSession(c *fiber.Ctx) error {
	sessionID := c.Params("id")
	h.listenSessions.mutex.Lock()
	session := h.listenSessions.sessions[sessionID]
	if session != nil {
		session.Status = "ended"
		delete(h.listenSessions.byDevice, session.DeviceID)
		if session.AdminConn != nil {
			session.AdminConn.Close()
		}
		if session.DeviceConn != nil {
			session.DeviceConn.Close()
		}
	}
	h.listenSessions.mutex.Unlock()
	if session == nil {
		return c.Status(404).JSON(fiber.Map{"error": "session not found"})
	}
	return c.JSON(fiber.Map{"status": "ended"})
}

// ==================== Listen WebSocket handlers ====================

// ListenAdminWebSocket: admin receives audio from device mic.
func (h *AudioHandler) ListenAdminWebSocket(c *websocket.Conn) {
	sessionID := c.Params("session_id")
	h.listenSessions.mutex.Lock()
	session := h.listenSessions.sessions[sessionID]
	if session == nil || session.Status == "ended" {
		h.listenSessions.mutex.Unlock()
		safeWriteJSON(c, AudioSignalMessage{Type: "error", Payload: json.RawMessage(`"session not found"`)}, adminWriteDeadline)
		c.Close()
		return
	}
	session.AdminConn = c
	h.listenSessions.mutex.Unlock()

	log.Printf("Admin connected to listen session %s (device %s)", sessionID, session.DeviceID)
	safeWriteJSON(c, AudioSignalMessage{Type: "session_info", SessionID: sessionID, DeviceID: session.DeviceID}, adminWriteDeadline)

	defer func() {
		h.listenSessions.mutex.Lock()
		if session.AdminConn == c {
			session.AdminConn = nil
		}
		h.listenSessions.mutex.Unlock()
		c.Close()
	}()

	for {
		msgType, data, err := c.ReadMessage()
		if err != nil {
			break
		}
		if msgType == fws.TextMessage {
			var msg AudioSignalMessage
			if json.Unmarshal(data, &msg) == nil && msg.Type == "stop" {
				session.mutex.RLock()
				dc := session.DeviceConn
				session.mutex.RUnlock()
				if dc != nil {
					safeWriteJSON(dc, AudioSignalMessage{Type: "listen_stopped"}, deviceWriteDeadline)
				}
			}
		}
	}
}

// ListenDeviceWebSocket: device sends mic audio to admin.
func (h *AudioHandler) ListenDeviceWebSocket(c *websocket.Conn) {
	sessionID := c.Params("session_id")

	h.listenSessions.mutex.Lock()
	session := h.listenSessions.sessions[sessionID]
	if session == nil || session.Status == "ended" {
		h.listenSessions.mutex.Unlock()
		safeWriteJSON(c, AudioSignalMessage{Type: "error", Payload: json.RawMessage(`"session not found"`)}, deviceWriteDeadline)
		c.Close()
		return
	}
	session.DeviceConn = c
	session.Status = "streaming"
	h.listenSessions.mutex.Unlock()

	log.Printf("Device connected to listen session %s", sessionID)

	// Notify admin
	session.mutex.RLock()
	ac := session.AdminConn
	session.mutex.RUnlock()
	if ac != nil {
		safeWriteJSON(ac, AudioSignalMessage{Type: "device_ready"}, adminWriteDeadline)
	}

	defer func() {
		h.listenSessions.mutex.Lock()
		if session.DeviceConn == c {
			session.DeviceConn = nil
			session.Status = "ended"
			delete(h.listenSessions.byDevice, session.DeviceID)
		}
		h.listenSessions.mutex.Unlock()

		session.mutex.RLock()
		ac := session.AdminConn
		session.mutex.RUnlock()
		if ac != nil {
			safeWriteJSON(ac, AudioSignalMessage{Type: "device_disconnected"}, adminWriteDeadline)
		}
		c.Close()
	}()

	for {
		msgType, data, err := c.ReadMessage()
		if err != nil {
			break
		}

		switch msgType {
		case fws.BinaryMessage:
			if len(data) > maxAudioChunkBytes {
				continue
			}

			session.mutex.Lock()
			session.BytesReceived += int64(len(data))
			session.mutex.Unlock()

			session.mutex.RLock()
			ac := session.AdminConn
			session.mutex.RUnlock()

			if ac != nil {
				if !safeWriteBinary(ac, data, adminWriteDeadline) {
					log.Printf("Listen session %s: admin write timeout, chunk dropped", sessionID)
				}
			}

		case fws.TextMessage:
			var msg AudioSignalMessage
			if json.Unmarshal(data, &msg) == nil {
				session.mutex.RLock()
				ac := session.AdminConn
				session.mutex.RUnlock()
				if ac != nil {
					safeWriteJSON(ac, msg, adminWriteDeadline)
				}
			}
		}
	}
}
