package models

import (
	"time"

	"github.com/google/uuid"
)

// ========== Authentication DTOs ==========

type LoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=6"`
}

type LoginResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	User         User   `json:"user"`
}

type RegisterRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=8"`
	Name     string `json:"name" validate:"required,min=2"`
}

type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

// ========== Device DTOs ==========

type CreateEnrollmentRequest struct {
	Name      string     `json:"name"`
	GroupID   *uuid.UUID `json:"group_id,omitempty"`
	PolicyID  *uuid.UUID `json:"policy_id,omitempty"`
	MaxUses   *int       `json:"max_uses,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type DeviceRegistrationRequest struct {
	EnrollmentToken string                 `json:"enrollment_token" validate:"required"`
	DeviceID        string                 `json:"device_id" validate:"required"`
	SerialNumber    string                 `json:"serial_number,omitempty"`
	Model           string                 `json:"model,omitempty"`
	Manufacturer    string                 `json:"manufacturer,omitempty"`
	AndroidVersion  string                 `json:"android_version,omitempty"`
	SDKVersion      int                    `json:"sdk_version,omitempty"`
	AgentVersion    string                 `json:"agent_version,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

type DeviceRegistrationResponse struct {
	DeviceID    uuid.UUID              `json:"device_id"`
	DeviceToken string                 `json:"device_token"`
	Policy      map[string]interface{} `json:"policy,omitempty"`
	MQTTConfig  MQTTConnectionConfig   `json:"mqtt_config"`
}

type MQTTConnectionConfig struct {
	Broker    string           `json:"broker"`
	Port      int              `json:"port"`
	Username  string           `json:"username"`
	Password  string           `json:"password"`
	ClientID  string           `json:"client_id"`
	UseTLS    bool             `json:"use_tls"`
	TopicBase string           `json:"topic_base"`
	Topics    MQTTTopicsConfig `json:"topics"`
}

type MQTTTopicsConfig struct {
	Commands  string `json:"commands"`
	Telemetry string `json:"telemetry"`
	Responses string `json:"responses"`
}

type UpdateDeviceRequest struct {
	Name     *string    `json:"name,omitempty"`
	GroupID  *uuid.UUID `json:"group_id,omitempty"`
	PolicyID *uuid.UUID `json:"policy_id,omitempty"`
	Tags     []string   `json:"tags,omitempty"`
}

type DeviceListResponse struct {
	Devices    []Device `json:"devices"`
	Total      int64    `json:"total"`
	Page       int      `json:"page"`
	PageSize   int      `json:"page_size"`
	TotalPages int      `json:"total_pages"`
}

type DeviceFilter struct {
	OrganizationID  *uuid.UUID     `json:"organization_id,omitempty"`
	GroupID         *uuid.UUID     `json:"group_id,omitempty"`
	EnrollmentToken string         `json:"enrollment_token,omitempty"`
	Status          []DeviceStatus `json:"status,omitempty"`
	Search          string         `json:"search,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
	IssamSearch     string         `json:"issam_search,omitempty"`
	IssamFilter     string         `json:"issam_filter,omitempty"` // "has", "missing", or ""
	LastSeenFrom    *time.Time     `json:"last_seen_from,omitempty"`
	LastSeenTo      *time.Time     `json:"last_seen_to,omitempty"`
	AgentVersion    string         `json:"agent_version,omitempty"`
	Page            int            `json:"page"`
	PageSize        int            `json:"page_size"`
}

// ========== Command DTOs ==========

type CreateCommandRequest struct {
	CommandType    string                 `json:"command_type" validate:"required"`
	Payload        map[string]interface{} `json:"payload,omitempty"`
	Priority       int                    `json:"priority,omitempty"`
	TimeoutSeconds int                    `json:"timeout_seconds,omitempty"`
}

type BulkCommandRequest struct {
	DeviceIDs      []uuid.UUID            `json:"device_ids" validate:"required,min=1"`
	CommandType    string                 `json:"command_type" validate:"required"`
	Payload        map[string]interface{} `json:"payload,omitempty"`
	Priority       int                    `json:"priority,omitempty"`
	TimeoutSeconds int                    `json:"timeout_seconds,omitempty"`
}

type CommandResponse struct {
	Command
	DeviceName *string `json:"device_name,omitempty"`
}

// ========== Telemetry DTOs ==========

type TelemetryReport struct {
	DeviceID         string   `json:"device_id"`
	Timestamp        int64    `json:"timestamp"`
	BatteryLevel     *int     `json:"battery_level,omitempty"`
	BatteryCharging  *bool    `json:"battery_charging,omitempty"`
	StorageTotal     *int64   `json:"storage_total,omitempty"`
	StorageAvailable *int64   `json:"storage_available,omitempty"`
	MemoryTotal      *int64   `json:"memory_total,omitempty"`
	MemoryAvailable  *int64   `json:"memory_available,omitempty"`
	NetworkType      *string  `json:"network_type,omitempty"`
	NetworkStrength  *int     `json:"network_strength,omitempty"`
	IPAddress        *string  `json:"ip_address,omitempty"`
	Latitude         *float64 `json:"latitude,omitempty"`
	Longitude        *float64 `json:"longitude,omitempty"`
	RunningApps      []string `json:"running_apps,omitempty"`
	AgentVersion     *string  `json:"agent_version,omitempty"`

	// v1.1 extended telemetry
	WifiSsid      *string `json:"wifi_ssid,omitempty"`
	WifiRssi      *int    `json:"wifi_rssi,omitempty"`
	ChargingType  *string `json:"charging_type,omitempty"`
	ForegroundApp *string `json:"foreground_app,omitempty"`
	CurrentUrl    *string `json:"current_url,omitempty"`
	LinkSpeedMbps *int    `json:"link_speed_mbps,omitempty"`

	// v1.3 lock status
	IsDeviceLocked *bool `json:"is_device_locked,omitempty"`
}

// ========== Group DTOs ==========

type CreateGroupRequest struct {
	Name          string     `json:"name" validate:"required,min=1"`
	Description   *string    `json:"description,omitempty"`
	ParentGroupID *uuid.UUID `json:"parent_group_id,omitempty"`
}

type UpdateGroupRequest struct {
	Name          *string                `json:"name,omitempty"`
	Description   *string                `json:"description,omitempty"`
	ParentGroupID *uuid.UUID             `json:"parent_group_id,omitempty"`
	Settings      map[string]interface{} `json:"settings,omitempty"`
}

// ========== Policy DTOs ==========

type CreatePolicyRequest struct {
	Name        string                 `json:"name" validate:"required,min=1"`
	Description *string                `json:"description,omitempty"`
	Rules       map[string]interface{} `json:"rules" validate:"required"`
	IsDefault   bool                   `json:"is_default,omitempty"`
	Priority    int                    `json:"priority,omitempty"`
}

type UpdatePolicyRequest struct {
	Name        *string                `json:"name,omitempty"`
	Description *string                `json:"description,omitempty"`
	Rules       map[string]interface{} `json:"rules,omitempty"`
	IsDefault   *bool                  `json:"is_default,omitempty"`
	Priority    *int                   `json:"priority,omitempty"`
}

// ========== Stats DTOs ==========

type DashboardStats struct {
	TotalDevices   int64            `json:"total_devices"`
	OnlineDevices  int64            `json:"online_devices"`
	OfflineDevices int64            `json:"offline_devices"`
	PendingDevices int64            `json:"pending_devices"`
	TotalGroups    int64            `json:"total_groups"`
	TotalCommands  int64            `json:"total_commands"`
	StatusCounts   map[string]int64 `json:"status_counts"`
}

// ========== API Response Wrappers ==========

type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *APIError   `json:"error,omitempty"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func SuccessResponse(data interface{}) APIResponse {
	return APIResponse{
		Success: true,
		Data:    data,
	}
}

func ErrorResponse(code string, message string) APIResponse {
	return APIResponse{
		Success: false,
		Error: &APIError{
			Code:    code,
			Message: message,
		},
	}
}
