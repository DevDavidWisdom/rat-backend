package services

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/mdm-system/backend/internal/models"
	"github.com/mdm-system/backend/pkg/mqtt"
)

type TelemetryService struct {
	mqttClient      *mqtt.Client
	deviceService   *DeviceService
	geofenceService *GeofenceService
}

func NewTelemetryService(mqttClient *mqtt.Client, deviceService *DeviceService, geofenceService *GeofenceService) *TelemetryService {
	return &TelemetryService{
		mqttClient:      mqttClient,
		deviceService:   deviceService,
		geofenceService: geofenceService,
	}
}

func (s *TelemetryService) Start(ctx context.Context) error {
	// Subscribe to all telemetry from devices
	// Topic format: devices/{deviceUUID}/telemetry
	err := s.mqttClient.Subscribe("devices/+/telemetry", s.handleTelemetry)
	if err != nil {
		return err
	}
	log.Println("Telemetry service started, subscribed to devices/+/telemetry")
	return nil
}

func (s *TelemetryService) handleTelemetry(topic string, payload []byte) {
	// Extract device UUID from topic
	// Topic format: devices/{deviceUUID}/telemetry
	parts := strings.Split(topic, "/")
	if len(parts) < 3 {
		return
	}
	deviceIDStr := parts[1]
	deviceID, err := uuid.Parse(deviceIDStr)
	if err != nil {
		log.Printf("Error parsing device ID from topic %s: %v", topic, err)
		return
	}

	var msg map[string]interface{}
	if err := json.Unmarshal(payload, &msg); err != nil {
		log.Printf("Error unmarshaling telemetry from %s: %v", topic, err)
		return
	}

	msgType, _ := msg["type"].(string)

	switch msgType {
	case "TELEMETRY_LOCATION":
		s.handleLocationTelemetry(deviceID, msg["data"])
	default:
		// Handle other telemetry types (battery, storage, etc.)
		var report models.TelemetryReport
		if err := json.Unmarshal(payload, &report); err == nil {
			s.deviceService.UpdateTelemetry(context.Background(), deviceIDStr, &report)
		}
	}
}

func (s *TelemetryService) handleLocationTelemetry(deviceID uuid.UUID, data interface{}) {
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		return
	}

	loc := models.DeviceLocation{
		DeviceID:  deviceID,
		Latitude:  dataMap["lat"].(float64),
		Longitude: dataMap["lng"].(float64),
		Accuracy:  float32(dataMap["accuracy"].(float64)),
	}

	if alt, ok := dataMap["altitude"].(float64); ok {
		loc.Altitude = alt
	}
	if speed, ok := dataMap["speed"].(float64); ok {
		loc.Speed = float32(speed)
	}
	if bearing, ok := dataMap["bearing"].(float64); ok {
		loc.Bearing = float32(bearing)
	}

	// 1. Update in GeofenceService to check for breaches
	err := s.geofenceService.ProcessLocationUpdate(context.Background(), deviceID, loc)
	if err != nil {
		log.Printf("Error processing location update for %s: %v", deviceID, err)
	}

	// 2. Update device last known location in DeviceService/Repo
	// (Assuming DeviceService.UpdateTelemetry handles this if models.TelemetryReport includes location)
}
