package services

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/mdm-system/backend/internal/models"
	"github.com/mdm-system/backend/internal/repository"
)

var (
	ErrDeviceAlreadyExists = errors.New("device already registered")
	ErrInvalidEnrollment   = errors.New("invalid or expired enrollment token")
)

type DeviceService struct {
	deviceRepo     *repository.DeviceRepository
	enrollmentRepo *repository.EnrollmentRepository
	authService    *AuthService
	mqttBroker     string
	deviceBroker   string
	mqttPort       int
}

func NewDeviceService(
	deviceRepo *repository.DeviceRepository,
	enrollmentRepo *repository.EnrollmentRepository,
	authService *AuthService,
	mqttBroker string,
	deviceBroker string,
	mqttPort int,
) *DeviceService {
	return &DeviceService{
		deviceRepo:     deviceRepo,
		enrollmentRepo: enrollmentRepo,
		authService:    authService,
		mqttBroker:     mqttBroker,
		deviceBroker:   deviceBroker,
		mqttPort:       mqttPort,
	}
}

func (s *DeviceService) CreateEnrollment(ctx context.Context, req *models.CreateEnrollmentRequest, orgID uuid.UUID, userID uuid.UUID) (*models.EnrollmentToken, error) {
	enrollment := &models.EnrollmentToken{
		OrganizationID: orgID,
		GroupID:        req.GroupID,
		PolicyID:       req.PolicyID,
		CreatedBy:      &userID,
		Name:           &req.Name,
		MaxUses:        req.MaxUses,
		ExpiresAt:      req.ExpiresAt,
	}

	err := s.enrollmentRepo.Create(ctx, enrollment)
	if err != nil {
		return nil, err
	}

	return enrollment, nil
}

func (s *DeviceService) ListEnrollments(ctx context.Context, orgID uuid.UUID) ([]models.EnrollmentToken, error) {
	return s.enrollmentRepo.List(ctx, orgID)
}

func (s *DeviceService) DeactivateEnrollment(ctx context.Context, id uuid.UUID) error {
	return s.enrollmentRepo.Deactivate(ctx, id)
}

func (s *DeviceService) RegisterDevice(ctx context.Context, req *models.DeviceRegistrationRequest) (*models.DeviceRegistrationResponse, error) {
	// Validate enrollment token
	enrollment, err := s.enrollmentRepo.ValidateAndConsume(ctx, req.EnrollmentToken)
	if err != nil {
		return nil, ErrInvalidEnrollment
	}

	// Check if device already exists — re-enroll instead of rejecting
	existing, err := s.deviceRepo.GetByDeviceID(ctx, req.DeviceID)
	if err == nil && existing != nil {
		// Device already registered — re-issue token and return success
		// This handles cases where the app lost credentials or was reinstalled
		deviceToken, err := s.authService.GenerateDeviceToken(existing)
		if err != nil {
			return nil, err
		}
		existing.DeviceToken = &deviceToken
		err = s.deviceRepo.UpdateToken(ctx, existing.ID, deviceToken)
		if err != nil {
			return nil, err
		}

		// Update status to online
		s.deviceRepo.UpdateStatus(ctx, existing.ID, models.DeviceStatusOnline)

		// Build MQTT config for the existing device
		topicBase := "devices/" + existing.ID.String()
		mqttConfig := models.MQTTConnectionConfig{
			Broker:    s.deviceBroker,
			Port:      s.mqttPort,
			Username:  existing.ID.String(),
			Password:  deviceToken,
			ClientID:  "device_" + existing.ID.String(),
			UseTLS:    false,
			TopicBase: topicBase,
			Topics: models.MQTTTopicsConfig{
				Commands:  topicBase + "/commands",
				Telemetry: topicBase + "/telemetry",
				Responses: topicBase + "/responses",
			},
		}

		return &models.DeviceRegistrationResponse{
			DeviceID:    existing.ID,
			DeviceToken: deviceToken,
			MQTTConfig:  mqttConfig,
		}, nil
	}

	// Create device
	enrollToken := req.EnrollmentToken
	device := &models.Device{
		OrganizationID:  enrollment.OrganizationID,
		GroupID:         enrollment.GroupID,
		PolicyID:        enrollment.PolicyID,
		DeviceID:        req.DeviceID,
		SerialNumber:    &req.SerialNumber,
		EnrollmentToken: &enrollToken,
		Model:           &req.Model,
		Manufacturer:    &req.Manufacturer,
		AndroidVersion:  &req.AndroidVersion,
		SDKVersion:      &req.SDKVersion,
		AgentVersion:    &req.AgentVersion,
		Status:          models.DeviceStatusOnline,
		Metadata:        req.Metadata,
	}

	now := time.Now()
	device.EnrolledAt = &now
	device.LastSeen = &now

	// Set device name from model if not provided
	if device.Name == nil || *device.Name == "" {
		name := req.Model + " - " + req.DeviceID[:8]
		device.Name = &name
	}

	// Create device first so it gets a UUID
	err = s.deviceRepo.Create(ctx, device)
	if err != nil {
		return nil, err
	}

	// Generate device token AFTER Create so device.ID is set
	deviceToken, err := s.authService.GenerateDeviceToken(device)
	if err != nil {
		return nil, err
	}
	device.DeviceToken = &deviceToken

	// Update the device with the token
	err = s.deviceRepo.UpdateToken(ctx, device.ID, deviceToken)
	if err != nil {
		return nil, err
	}

	// Build MQTT config
	topicBase := "devices/" + device.ID.String()
	mqttConfig := models.MQTTConnectionConfig{
		Broker:    s.deviceBroker,
		Port:      s.mqttPort,
		Username:  device.ID.String(),
		Password:  deviceToken,
		ClientID:  "device_" + device.ID.String(),
		UseTLS:    false, // Enable in production
		TopicBase: topicBase,
		Topics: models.MQTTTopicsConfig{
			Commands:  topicBase + "/commands",
			Telemetry: topicBase + "/telemetry",
			Responses: topicBase + "/responses",
		},
	}

	response := &models.DeviceRegistrationResponse{
		DeviceID:    device.ID,
		DeviceToken: deviceToken,
		MQTTConfig:  mqttConfig,
	}

	return response, nil
}

func (s *DeviceService) GetDevice(ctx context.Context, id uuid.UUID) (*models.Device, error) {
	return s.deviceRepo.GetByID(ctx, id)
}

func (s *DeviceService) GetDeviceByDeviceID(ctx context.Context, deviceID string) (*models.Device, error) {
	return s.deviceRepo.GetByDeviceID(ctx, deviceID)
}

func (s *DeviceService) ListDevices(ctx context.Context, filter *models.DeviceFilter) (*models.DeviceListResponse, error) {
	devices, total, err := s.deviceRepo.List(ctx, filter)
	if err != nil {
		return nil, err
	}

	// Ensure non-nil slice so JSON marshals as [] not null
	if devices == nil {
		devices = []models.Device{}
	}

	totalPages := int(total) / filter.PageSize
	if int(total)%filter.PageSize > 0 {
		totalPages++
	}

	return &models.DeviceListResponse{
		Devices:    devices,
		Total:      total,
		Page:       filter.Page,
		PageSize:   filter.PageSize,
		TotalPages: totalPages,
	}, nil
}
func (s *DeviceService) ListAllDevices(ctx context.Context, orgID uuid.UUID) ([]models.Device, error) {
	return s.deviceRepo.ListAll(ctx, orgID)
}

func (s *DeviceService) GetIDsByStatus(ctx context.Context, orgID uuid.UUID, status models.DeviceStatus) ([]uuid.UUID, error) {
	return s.deviceRepo.GetIDsByStatus(ctx, orgID, status)
}

func (s *DeviceService) UpdateDevice(ctx context.Context, id uuid.UUID, req *models.UpdateDeviceRequest) (*models.Device, error) {
	device, err := s.deviceRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if req.Name != nil {
		device.Name = req.Name
	}
	if req.GroupID != nil {
		device.GroupID = req.GroupID
	}
	if req.PolicyID != nil {
		device.PolicyID = req.PolicyID
	}
	if req.Tags != nil {
		device.Tags = req.Tags
	}

	err = s.deviceRepo.Update(ctx, device)
	if err != nil {
		return nil, err
	}

	return s.deviceRepo.GetByID(ctx, id)
}

func (s *DeviceService) DeleteDevice(ctx context.Context, id uuid.UUID) error {
	// Get the enrollment token before deleting so we can free up the slot
	token, _ := s.deviceRepo.GetEnrollmentToken(ctx, id)

	err := s.deviceRepo.Delete(ctx, id)
	if err != nil {
		return err
	}

	// Decrement enrollment usage to free up the slot
	if token != nil && *token != "" {
		_ = s.enrollmentRepo.DecrementUses(ctx, *token)
	}

	return nil
}

func (s *DeviceService) UpdateTelemetry(ctx context.Context, deviceID string, telemetry *models.TelemetryReport) error {
	return s.deviceRepo.UpdateTelemetry(ctx, deviceID, telemetry)
}

func (s *DeviceService) UpdateStatus(ctx context.Context, id uuid.UUID, status models.DeviceStatus) error {
	return s.deviceRepo.UpdateStatus(ctx, id, status)
}

func (s *DeviceService) GetStats(ctx context.Context, orgID uuid.UUID) (*models.DashboardStats, error) {
	return s.deviceRepo.GetStats(ctx, orgID)
}

func (s *DeviceService) MarkOfflineDevices(ctx context.Context, timeout time.Duration) (int64, error) {
	return s.deviceRepo.MarkOfflineDevices(ctx, timeout)
}

// UpdateIssamID saves an ISSAM ID for a device (looked up by device_id string)
func (s *DeviceService) UpdateIssamID(ctx context.Context, deviceID string, issamID string) error {
	device, err := s.deviceRepo.GetByDeviceID(ctx, deviceID)
	if err != nil {
		return err
	}
	return s.deviceRepo.UpdateIssamID(ctx, device.ID, issamID)
}
