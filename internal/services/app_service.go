package services

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/mdm-system/backend/internal/config"
	"github.com/mdm-system/backend/internal/models"
	"github.com/mdm-system/backend/internal/repository"
	"github.com/mdm-system/backend/pkg/storage"
)

type AppService struct {
	appRepo        *repository.AppRepository
	deviceRepo     *repository.DeviceRepository
	commandService *CommandService
	auditService   *AuditService
	minioClient    *storage.MinIOClient
	cfg            *config.Config
}

func NewAppService(
	appRepo *repository.AppRepository,
	commandService *CommandService,
	auditService *AuditService,
	minioClient *storage.MinIOClient,
	deviceRepo *repository.DeviceRepository,
	cfg *config.Config,
) *AppService {
	return &AppService{
		appRepo:        appRepo,
		commandService: commandService,
		auditService:   auditService,
		minioClient:    minioClient,
		deviceRepo:     deviceRepo,
		cfg:            cfg,
	}
}

// UploadAPK handles APK file upload to MinIO and creates the app_repository record
func (s *AppService) UploadAPK(ctx context.Context, orgID uuid.UUID, uploaderID *uuid.UUID, file io.Reader, fileSize int64, appName string, packageName string, versionCode int, versionName string, description string, isMandatory bool) (*models.AppPackage, error) {
	if s.minioClient == nil {
		return nil, fmt.Errorf("file storage not configured")
	}

	// Generate object key
	objectKey := fmt.Sprintf("apks/%s/%s/%d/%s.apk", orgID.String(), packageName, versionCode, uuid.New().String()[:8])

	// Hash the file while uploading (use TeeReader)
	hasher := sha256.New()
	tee := io.TeeReader(file, hasher)

	// Upload to MinIO
	if err := s.minioClient.UploadFile(ctx, objectKey, tee, fileSize, "application/vnd.android.package-archive"); err != nil {
		return nil, fmt.Errorf("failed to upload APK: %w", err)
	}

	hash := fmt.Sprintf("%x", hasher.Sum(nil))

	app := &models.AppPackage{
		OrganizationID: orgID,
		PackageName:    packageName,
		AppName:        appName,
		VersionCode:    versionCode,
		VersionName:    &versionName,
		APKPath:        objectKey,
		APKSize:        &fileSize,
		APKHash:        &hash,
		Description:    &description,
		IsMandatory:    isMandatory,
		UploadedBy:     uploaderID,
	}

	if err := s.appRepo.Create(ctx, app); err != nil {
		// Clean up uploaded file on DB error
		_ = s.minioClient.DeleteFile(ctx, objectKey)
		return nil, fmt.Errorf("failed to save app record: %w", err)
	}

	// Generate download URL
	downloadURL, err := s.getExternalDownloadURL(ctx, objectKey)
	if err == nil {
		app.DownloadURL = downloadURL
	}

	log.Printf("APK uploaded: %s v%d (%s) -> %s", packageName, versionCode, appName, objectKey)
	return app, nil
}

func (s *AppService) ListApps(ctx context.Context, orgID uuid.UUID) ([]models.AppPackage, error) {
	apps, err := s.appRepo.List(ctx, orgID)
	if err != nil {
		return nil, err
	}
	// Attach external download URLs
	for i := range apps {
		url, err := s.getExternalDownloadURL(ctx, apps[i].APKPath)
		if err == nil {
			apps[i].DownloadURL = url
		}
	}
	return apps, nil
}

func (s *AppService) GetApp(ctx context.Context, id uuid.UUID) (*models.AppPackage, error) {
	app, err := s.appRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	url, err := s.getExternalDownloadURL(ctx, app.APKPath)
	if err == nil {
		app.DownloadURL = url
	}
	return app, nil
}

func (s *AppService) DeleteApp(ctx context.Context, id uuid.UUID) error {
	app, err := s.appRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	// Delete from MinIO
	if s.minioClient != nil && app.APKPath != "" {
		_ = s.minioClient.DeleteFile(ctx, app.APKPath)
	}
	return s.appRepo.Delete(ctx, id)
}

// DeployApp deploys an app to specified targets (device IDs, group IDs, enrollment tokens, or all)
func (s *AppService) DeployApp(ctx context.Context, orgID uuid.UUID, appID uuid.UUID, deviceIDs []uuid.UUID, groupIDs []uuid.UUID, enrollmentTokens []string, deployAll bool, issuerID *uuid.UUID) (int, error) {
	app, err := s.appRepo.GetByID(ctx, appID)
	if err != nil {
		return 0, fmt.Errorf("app not found: %w", err)
	}

	// Generate an external presigned URL that devices can access
	downloadURL, err := s.getExternalDownloadURL(ctx, app.APKPath)
	if err != nil {
		return 0, fmt.Errorf("failed to generate download URL: %w", err)
	}

	// Collect all target device IDs
	targetMap := make(map[uuid.UUID]bool)

	// Direct device IDs
	for _, id := range deviceIDs {
		targetMap[id] = true
	}

	// Resolve group IDs to device IDs
	for _, gid := range groupIDs {
		ids, err := s.deviceRepo.GetIDsByGroupID(ctx, gid)
		if err != nil {
			log.Printf("Warning: failed to resolve group %s: %v", gid, err)
			continue
		}
		for _, id := range ids {
			targetMap[id] = true
		}
	}

	// Resolve enrollment tokens to device IDs
	for _, token := range enrollmentTokens {
		ids, err := s.getDeviceIDsByEnrollmentToken(ctx, token)
		if err != nil {
			log.Printf("Warning: failed to resolve enrollment token %s: %v", token, err)
			continue
		}
		for _, id := range ids {
			targetMap[id] = true
		}
	}

	// Deploy to all devices in org
	if deployAll {
		devices, err := s.deviceRepo.ListAll(ctx, orgID)
		if err != nil {
			return 0, fmt.Errorf("failed to list all devices: %w", err)
		}
		for _, d := range devices {
			targetMap[d.ID] = true
		}
	}

	if len(targetMap) == 0 {
		return 0, fmt.Errorf("no target devices found")
	}

	// Convert to slice
	targets := make([]uuid.UUID, 0, len(targetMap))
	for id := range targetMap {
		targets = append(targets, id)
	}

	payload := map[string]interface{}{
		"package_name": app.PackageName,
		"app_name":     app.AppName,
		"version_code": app.VersionCode,
		"version_name": app.VersionName,
		"download_url": downloadURL,
		"apk_size":     app.APKSize,
		"apk_hash":     app.APKHash,
	}

	_, err = s.commandService.CreateBulkCommands(ctx, &models.BulkCommandRequest{
		DeviceIDs:   targets,
		CommandType: "INSTALL_APP",
		Payload:     payload,
		Priority:    10,
	}, issuerID)

	if err != nil {
		return 0, fmt.Errorf("failed to create install commands: %w", err)
	}

	log.Printf("Deployed %s v%d to %d devices", app.PackageName, app.VersionCode, len(targets))
	return len(targets), nil
}

// getDownloadURL generates an internal presigned URL (for dashboard)
func (s *AppService) getDownloadURL(ctx context.Context, objectKey string) (string, error) {
	if s.minioClient == nil || objectKey == "" {
		return "", fmt.Errorf("no storage client")
	}
	return s.minioClient.GetPresignedURL(ctx, objectKey, 24*time.Hour)
}

// getExternalDownloadURL generates a presigned URL accessible by devices on the LAN
func (s *AppService) getExternalDownloadURL(ctx context.Context, objectKey string) (string, error) {
	if s.minioClient == nil || objectKey == "" {
		return "", fmt.Errorf("no storage client")
	}
	extEndpoint := s.cfg.MinIO.ExternalEndpoint
	if extEndpoint == "" {
		extEndpoint = s.cfg.MinIO.Endpoint
	}
	return s.minioClient.GetExternalPresignedURL(ctx, objectKey, 24*time.Hour, extEndpoint, s.cfg.MinIO.ExternalUseSSL)
}

// getDeviceIDsByEnrollmentToken finds all devices that enrolled with a specific token
func (s *AppService) getDeviceIDsByEnrollmentToken(ctx context.Context, token string) ([]uuid.UUID, error) {
	return s.deviceRepo.GetIDsByEnrollmentToken(ctx, token)
}
