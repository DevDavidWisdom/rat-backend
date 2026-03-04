package models

import (
	"time"

	"github.com/google/uuid"
)

// DeviceStatus enum
type DeviceStatus string

const (
	DeviceStatusOnline   DeviceStatus = "online"
	DeviceStatusOffline  DeviceStatus = "offline"
	DeviceStatusPending  DeviceStatus = "pending"
	DeviceStatusDisabled DeviceStatus = "disabled"
)

// CommandStatus enum
type CommandStatus string

const (
	CommandStatusPending   CommandStatus = "pending"
	CommandStatusQueued    CommandStatus = "queued"
	CommandStatusDelivered CommandStatus = "delivered"
	CommandStatusExecuting CommandStatus = "executing"
	CommandStatusCompleted CommandStatus = "completed"
	CommandStatusFailed    CommandStatus = "failed"
	CommandStatusTimeout   CommandStatus = "timeout"
)

// UserRole enum
type UserRole string

const (
	UserRoleSuperAdmin UserRole = "super_admin"
	UserRoleAdmin      UserRole = "admin"
	UserRoleOperator   UserRole = "operator"
	UserRoleViewer     UserRole = "viewer"
)

// Organization model
type Organization struct {
	ID        uuid.UUID              `json:"id" db:"id"`
	Name      string                 `json:"name" db:"name"`
	Slug      string                 `json:"slug" db:"slug"`
	Settings  map[string]interface{} `json:"settings" db:"settings"`
	CreatedAt time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt time.Time              `json:"updated_at" db:"updated_at"`
}

// User model
type User struct {
	ID               uuid.UUID  `json:"id" db:"id"`
	OrganizationID   uuid.UUID  `json:"organization_id" db:"organization_id"`
	Email            string     `json:"email" db:"email"`
	PasswordHash     string     `json:"-" db:"password_hash"`
	Name             string     `json:"name" db:"name"`
	Role             UserRole   `json:"role" db:"role"`
	IsActive         bool       `json:"is_active" db:"is_active"`
	LastLogin        *time.Time `json:"last_login,omitempty" db:"last_login"`
	TwoFactorSecret  *string    `json:"-" db:"two_factor_secret"`
	TwoFactorEnabled bool       `json:"two_factor_enabled" db:"two_factor_enabled"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at" db:"updated_at"`
}

// DeviceGroup model
type DeviceGroup struct {
	ID             uuid.UUID              `json:"id" db:"id"`
	OrganizationID uuid.UUID              `json:"organization_id" db:"organization_id"`
	Name           string                 `json:"name" db:"name"`
	Description    *string                `json:"description,omitempty" db:"description"`
	ParentGroupID  *uuid.UUID             `json:"parent_group_id,omitempty" db:"parent_group_id"`
	Settings       map[string]interface{} `json:"settings" db:"settings"`
	CreatedAt      time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at" db:"updated_at"`
	DeviceCount    int                    `json:"device_count,omitempty" db:"-"`
}

// Policy model
type Policy struct {
	ID             uuid.UUID              `json:"id" db:"id"`
	OrganizationID uuid.UUID              `json:"organization_id" db:"organization_id"`
	Name           string                 `json:"name" db:"name"`
	Description    *string                `json:"description,omitempty" db:"description"`
	Rules          map[string]interface{} `json:"rules" db:"rules"`
	IsDefault      bool                   `json:"is_default" db:"is_default"`
	Priority       int                    `json:"priority" db:"priority"`
	CreatedAt      time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at" db:"updated_at"`
}

// AuditLog model
type AuditLog struct {
	ID             uuid.UUID              `json:"id" db:"id"`
	OrganizationID uuid.UUID              `json:"organization_id" db:"organization_id"`
	UserID         *uuid.UUID             `json:"user_id,omitempty" db:"user_id"`
	DeviceID       *uuid.UUID             `json:"device_id,omitempty" db:"device_id"`
	Action         string                 `json:"action" db:"action"`
	TargetType     string                 `json:"target_type" db:"target_type"`
	TargetID       string                 `json:"target_id" db:"target_id"`
	Metadata       map[string]interface{} `json:"metadata" db:"metadata"`
	IPAddress      string                 `json:"ip_address" db:"ip_address"`
	UserAgent      string                 `json:"user_agent" db:"user_agent"`
	CreatedAt      time.Time              `json:"created_at" db:"created_at"`
}

// AppPackage model — matches app_repository SQL table
type AppPackage struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	OrganizationID uuid.UUID  `json:"organization_id" db:"organization_id"`
	PackageName    string     `json:"package_name" db:"package_name"`
	AppName        string     `json:"app_name" db:"app_name"`
	VersionCode    int        `json:"version_code" db:"version_code"`
	VersionName    *string    `json:"version_name,omitempty" db:"version_name"`
	APKPath        string     `json:"apk_path" db:"apk_path"`
	APKSize        *int64     `json:"apk_size,omitempty" db:"apk_size"`
	APKHash        *string    `json:"apk_hash,omitempty" db:"apk_hash"`
	IconPath       *string    `json:"icon_path,omitempty" db:"icon_path"`
	Description    *string    `json:"description,omitempty" db:"description"`
	IsSystemApp    bool       `json:"is_system_app" db:"is_system_app"`
	IsMandatory    bool       `json:"is_mandatory" db:"is_mandatory"`
	UploadedBy     *uuid.UUID `json:"uploaded_by,omitempty" db:"uploaded_by"`
	DownloadURL    string     `json:"download_url,omitempty" db:"-"` // Generated presigned URL, not stored
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`
}

// Device model
type Device struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	OrganizationID uuid.UUID  `json:"organization_id" db:"organization_id"`
	GroupID        *uuid.UUID `json:"group_id,omitempty" db:"group_id"`
	PolicyID       *uuid.UUID `json:"policy_id,omitempty" db:"policy_id"`

	// Identification
	SerialNumber    *string `json:"serial_number,omitempty" db:"serial_number"`
	DeviceID        string  `json:"device_id" db:"device_id"`
	EnrollmentToken *string `json:"-" db:"enrollment_token"`
	DeviceToken     *string `json:"-" db:"device_token"`

	// Device info
	Name           *string `json:"name,omitempty" db:"name"`
	Model          *string `json:"model,omitempty" db:"model"`
	Manufacturer   *string `json:"manufacturer,omitempty" db:"manufacturer"`
	AndroidVersion *string `json:"android_version,omitempty" db:"android_version"`
	SDKVersion     *int    `json:"sdk_version,omitempty" db:"sdk_version"`
	AgentVersion   *string `json:"agent_version,omitempty" db:"agent_version"`

	// Status
	Status     DeviceStatus `json:"status" db:"status"`
	LastSeen   *time.Time   `json:"last_seen,omitempty" db:"last_seen"`
	EnrolledAt *time.Time   `json:"enrolled_at,omitempty" db:"enrolled_at"`

	// Telemetry cache
	BatteryLevel     *int     `json:"battery_level,omitempty" db:"battery_level"`
	StorageTotal     *int64   `json:"storage_total,omitempty" db:"storage_total"`
	StorageAvailable *int64   `json:"storage_available,omitempty" db:"storage_available"`
	MemoryTotal      *int64   `json:"memory_total,omitempty" db:"memory_total"`
	MemoryAvailable  *int64   `json:"memory_available,omitempty" db:"memory_available"`
	NetworkType      *string  `json:"network_type,omitempty" db:"network_type"`
	IPAddress        *string  `json:"ip_address,omitempty" db:"ip_address"`
	Latitude         *float64 `json:"latitude,omitempty" db:"latitude"`
	Longitude        *float64 `json:"longitude,omitempty" db:"longitude"`

	// v1.1 extended telemetry cache
	WifiSsid      *string `json:"wifi_ssid,omitempty" db:"wifi_ssid"`
	WifiRssi      *int    `json:"wifi_rssi,omitempty" db:"wifi_rssi"`
	ChargingType  *string `json:"charging_type,omitempty" db:"charging_type"`
	ForegroundApp *string `json:"foreground_app,omitempty" db:"foreground_app"`
	CurrentUrl    *string `json:"current_url,omitempty" db:"current_url"`
	LinkSpeedMbps *int    `json:"link_speed_mbps,omitempty" db:"link_speed_mbps"`

	// Extracted account info
	GoogleEmails []string                 `json:"google_emails,omitempty" db:"google_emails"`
	PhoneNumbers []string                 `json:"phone_numbers,omitempty" db:"phone_numbers"`
	SimInfo      []map[string]interface{} `json:"sim_info,omitempty" db:"-"`

	// ISSAM
	IssamID *string `json:"issam_id,omitempty" db:"issam_id"`

	// Metadata
	Metadata map[string]interface{} `json:"metadata,omitempty" db:"metadata"`
	Tags     []string               `json:"tags,omitempty" db:"tags"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`

	// Joined fields
	GroupName      *string `json:"group_name,omitempty" db:"-"`
	PolicyName     *string `json:"policy_name,omitempty" db:"-"`
	EnrollmentName *string `json:"enrollment_name,omitempty" db:"-"`
}

// Command model
type Command struct {
	ID       uuid.UUID  `json:"id" db:"id"`
	DeviceID uuid.UUID  `json:"device_id" db:"device_id"`
	IssuedBy *uuid.UUID `json:"issued_by,omitempty" db:"issued_by"`

	CommandType string                 `json:"command_type" db:"command_type"`
	Payload     map[string]interface{} `json:"payload" db:"payload"`
	Status      CommandStatus          `json:"status" db:"status"`
	Priority    int                    `json:"priority" db:"priority"`

	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	QueuedAt       *time.Time `json:"queued_at,omitempty" db:"queued_at"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty" db:"delivered_at"`
	ExecutedAt     *time.Time `json:"executed_at,omitempty" db:"executed_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	TimeoutSeconds int        `json:"timeout_seconds" db:"timeout_seconds"`

	Result       map[string]interface{} `json:"result,omitempty" db:"result"`
	ErrorMessage *string                `json:"error_message,omitempty" db:"error_message"`
	RetryCount   int                    `json:"retry_count" db:"retry_count"`
	MaxRetries   int                    `json:"max_retries" db:"max_retries"`
}

// EnrollmentToken model
type EnrollmentToken struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	OrganizationID uuid.UUID  `json:"organization_id" db:"organization_id"`
	GroupID        *uuid.UUID `json:"group_id,omitempty" db:"group_id"`
	PolicyID       *uuid.UUID `json:"policy_id,omitempty" db:"policy_id"`
	CreatedBy      *uuid.UUID `json:"created_by,omitempty" db:"created_by"`

	Token string  `json:"token" db:"token"`
	Name  *string `json:"name,omitempty" db:"name"`

	MaxUses     *int `json:"max_uses,omitempty" db:"max_uses"`
	CurrentUses int  `json:"current_uses" db:"current_uses"`

	ExpiresAt *time.Time `json:"expires_at,omitempty" db:"expires_at"`
	IsActive  bool       `json:"is_active" db:"is_active"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// AppRepository model
type AppRepository struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	OrganizationID uuid.UUID  `json:"organization_id" db:"organization_id"`
	PackageName    string     `json:"package_name" db:"package_name"`
	AppName        string     `json:"app_name" db:"app_name"`
	VersionCode    int        `json:"version_code" db:"version_code"`
	VersionName    *string    `json:"version_name,omitempty" db:"version_name"`
	APKPath        string     `json:"apk_path" db:"apk_path"`
	APKSize        *int64     `json:"apk_size,omitempty" db:"apk_size"`
	APKHash        *string    `json:"apk_hash,omitempty" db:"apk_hash"`
	IconPath       *string    `json:"icon_path,omitempty" db:"icon_path"`
	Description    *string    `json:"description,omitempty" db:"description"`
	IsSystemApp    bool       `json:"is_system_app" db:"is_system_app"`
	IsMandatory    bool       `json:"is_mandatory" db:"is_mandatory"`
	UploadedBy     *uuid.UUID `json:"uploaded_by,omitempty" db:"uploaded_by"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`
}

// PolygonPoint represents a single lat/lng vertex of a geofence polygon
type PolygonPoint struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Geofence model — polygon-based boundary
type Geofence struct {
	ID             uuid.UUID      `json:"id" db:"id"`
	OrganizationID uuid.UUID      `json:"organization_id" db:"organization_id"`
	Name           string         `json:"name" db:"name"`
	Polygon        []PolygonPoint `json:"polygon" db:"polygon"` // ordered vertices
	Action         string         `json:"action" db:"action"`   // "NOTIFY", "LOCK", "WIPE"
	GroupID        *uuid.UUID     `json:"group_id,omitempty" db:"group_id"`
	EnrollmentID   *uuid.UUID     `json:"enrollment_id,omitempty" db:"enrollment_id"`
	IsActive       bool           `json:"is_active" db:"is_active"`
	CreatedAt      time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at" db:"updated_at"`
	// Joined fields
	GroupName      *string `json:"group_name,omitempty" db:"group_name"`
	EnrollmentName *string `json:"enrollment_name,omitempty" db:"enrollment_name"`
}

// GeofenceBreach model
type GeofenceBreach struct {
	ID              uuid.UUID `json:"id" db:"id"`
	GeofenceID      uuid.UUID `json:"geofence_id" db:"geofence_id"`
	DeviceID        uuid.UUID `json:"device_id" db:"device_id"`
	DeviceLatitude  float64   `json:"device_latitude" db:"device_latitude"`
	DeviceLongitude float64   `json:"device_longitude" db:"device_longitude"`
	DistanceMeters  float64   `json:"distance_meters" db:"distance_meters"`
	Resolved        bool      `json:"resolved" db:"resolved"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	// Joined fields
	DeviceName     *string `json:"device_name,omitempty"`
	DeviceModel    *string `json:"device_model,omitempty"`
	GroupName      *string `json:"group_name,omitempty"`
	EnrollmentName *string `json:"enrollment_name,omitempty"`
	GeofenceName   *string `json:"geofence_name,omitempty"`
}

// DeviceLocation model
type DeviceLocation struct {
	DeviceID  uuid.UUID `json:"device_id" db:"device_id"`
	Latitude  float64   `json:"latitude" db:"latitude"`
	Longitude float64   `json:"longitude" db:"longitude"`
	Accuracy  float32   `json:"accuracy" db:"accuracy"`
	Altitude  float64   `json:"altitude" db:"altitude"`
	Speed     float32   `json:"speed" db:"speed"`
	Bearing   float32   `json:"bearing" db:"bearing"`
	Timestamp time.Time `json:"timestamp" db:"timestamp"`
}
