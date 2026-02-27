package services

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/mdm-system/backend/internal/models"
	"github.com/mdm-system/backend/internal/repository"
	"github.com/mdm-system/backend/pkg/mqtt"
)

// CommandResultHook is called after a command result is processed
type CommandResultHook func(ctx context.Context, cmd *models.Command, result map[string]interface{})

type CommandService struct {
	commandRepo *repository.CommandRepository
	deviceRepo  *repository.DeviceRepository
	mqttClient  *mqtt.Client
	resultHooks map[string]CommandResultHook // commandType -> hook
}

func NewCommandService(
	commandRepo *repository.CommandRepository,
	deviceRepo *repository.DeviceRepository,
	mqttClient *mqtt.Client,
) *CommandService {
	return &CommandService{
		commandRepo: commandRepo,
		deviceRepo:  deviceRepo,
		mqttClient:  mqttClient,
		resultHooks: make(map[string]CommandResultHook),
	}
}

// RegisterResultHook registers a callback for a specific command type
func (s *CommandService) RegisterResultHook(commandType string, hook CommandResultHook) {
	s.resultHooks[commandType] = hook
}

// MQTTCommand is the message format sent to devices
type MQTTCommand struct {
	ID          string                 `json:"id"`
	Type        string                 `json:"type"`
	Payload     map[string]interface{} `json:"payload,omitempty"`
	Timestamp   int64                  `json:"timestamp"`
	TimeoutSecs int                    `json:"timeout_secs"`
}

func (s *CommandService) CreateCommand(ctx context.Context, deviceID uuid.UUID, req *models.CreateCommandRequest, issuedBy *uuid.UUID) (*models.Command, error) {
	// Verify device exists
	_, err := s.deviceRepo.GetByID(ctx, deviceID)
	if err != nil {
		return nil, err
	}

	cmd := &models.Command{
		DeviceID:       deviceID,
		IssuedBy:       issuedBy,
		CommandType:    req.CommandType,
		Payload:        req.Payload,
		Priority:       req.Priority,
		TimeoutSeconds: req.TimeoutSeconds,
	}

	if cmd.TimeoutSeconds == 0 {
		cmd.TimeoutSeconds = 300 // Default 5 minutes
	}

	err = s.commandRepo.Create(ctx, cmd)
	if err != nil {
		return nil, err
	}

	// Dispatch to MQTT
	err = s.dispatchCommand(ctx, deviceID, cmd)
	if err != nil {
		// Update status but don't fail - device might be offline
		_ = s.commandRepo.UpdateStatus(ctx, cmd.ID, models.CommandStatusQueued, nil, nil)
	} else {
		_ = s.commandRepo.UpdateStatus(ctx, cmd.ID, models.CommandStatusDelivered, nil, nil)
	}

	return s.commandRepo.GetByID(ctx, cmd.ID)
}

func (s *CommandService) CreateBulkCommands(ctx context.Context, req *models.BulkCommandRequest, issuedBy *uuid.UUID) ([]models.Command, error) {
	var commands []models.Command

	for _, deviceID := range req.DeviceIDs {
		cmd := &models.Command{
			DeviceID:       deviceID,
			IssuedBy:       issuedBy,
			CommandType:    req.CommandType,
			Payload:        req.Payload,
			Priority:       req.Priority,
			TimeoutSeconds: req.TimeoutSeconds,
		}

		if cmd.TimeoutSeconds == 0 {
			cmd.TimeoutSeconds = 300
		}

		err := s.commandRepo.Create(ctx, cmd)
		if err != nil {
			continue // Skip failed devices
		}

		// Dispatch to MQTT
		err = s.dispatchCommand(ctx, deviceID, cmd)
		if err != nil {
			_ = s.commandRepo.UpdateStatus(ctx, cmd.ID, models.CommandStatusQueued, nil, nil)
		} else {
			_ = s.commandRepo.UpdateStatus(ctx, cmd.ID, models.CommandStatusDelivered, nil, nil)
		}

		fullCmd, _ := s.commandRepo.GetByID(ctx, cmd.ID)
		if fullCmd != nil {
			commands = append(commands, *fullCmd)
		}
	}

	return commands, nil
}

func (s *CommandService) dispatchCommand(ctx context.Context, deviceID uuid.UUID, cmd *models.Command) error {
	if s.mqttClient == nil {
		return nil // MQTT not configured
	}

	topic := "devices/" + deviceID.String() + "/commands"

	mqttCmd := MQTTCommand{
		ID:          cmd.ID.String(),
		Type:        cmd.CommandType,
		Payload:     cmd.Payload,
		Timestamp:   time.Now().Unix(),
		TimeoutSecs: cmd.TimeoutSeconds,
	}

	payload, err := json.Marshal(mqttCmd)
	if err != nil {
		return err
	}

	return s.mqttClient.Publish(topic, payload)
}

func (s *CommandService) GetCommand(ctx context.Context, id uuid.UUID) (*models.Command, error) {
	return s.commandRepo.GetByID(ctx, id)
}

func (s *CommandService) ListDeviceCommands(ctx context.Context, deviceID uuid.UUID, limit int) ([]models.Command, error) {
	return s.commandRepo.ListByDevice(ctx, deviceID, limit)
}

func (s *CommandService) GetPendingCommands(ctx context.Context, deviceID uuid.UUID) ([]models.Command, error) {
	return s.commandRepo.GetPendingByDevice(ctx, deviceID)
}

func (s *CommandService) UpdateCommandStatus(ctx context.Context, id uuid.UUID, status models.CommandStatus, result map[string]interface{}, errMsg *string) error {
	return s.commandRepo.UpdateStatus(ctx, id, status, result, errMsg)
}

func (s *CommandService) ProcessCommandResponse(ctx context.Context, commandID string, status string, result map[string]interface{}, errMsg *string) error {
	id, err := uuid.Parse(commandID)
	if err != nil {
		return err
	}

	var cmdStatus models.CommandStatus
	switch status {
	case "executing":
		cmdStatus = models.CommandStatusExecuting
	case "completed", "success":
		cmdStatus = models.CommandStatusCompleted
	case "failed", "error":
		cmdStatus = models.CommandStatusFailed
	default:
		cmdStatus = models.CommandStatus(status)
	}

	updateErr := s.commandRepo.UpdateStatus(ctx, id, cmdStatus, result, errMsg)

	// Fire result hooks for completed commands
	if updateErr == nil && (status == "completed" || status == "success") && len(s.resultHooks) > 0 {
		if cmd, err := s.commandRepo.GetByID(ctx, id); err == nil && cmd != nil {
			if hook, ok := s.resultHooks[cmd.CommandType]; ok {
				go hook(ctx, cmd, result)
			}
		}
	}

	return updateErr
}

func (s *CommandService) ProcessTimeouts(ctx context.Context) error {
	timedOut, err := s.commandRepo.GetTimedOut(ctx)
	if err != nil {
		return err
	}

	for _, cmd := range timedOut {
		errMsg := "Command timed out"
		_ = s.commandRepo.UpdateStatus(ctx, cmd.ID, models.CommandStatusTimeout, nil, &errMsg)
	}

	return nil
}
