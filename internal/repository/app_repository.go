package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mdm-system/backend/internal/models"
)

type AppRepository struct {
	pool *pgxpool.Pool
}

func NewAppRepository(pool *pgxpool.Pool) *AppRepository {
	return &AppRepository{pool: pool}
}

func (r *AppRepository) Create(ctx context.Context, app *models.AppPackage) error {
	query := `
		INSERT INTO app_repository (
			id, organization_id, package_name, app_name, version_code,
			version_name, apk_path, apk_size, apk_hash, icon_path,
			description, is_system_app, is_mandatory, uploaded_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING created_at, updated_at
	`
	app.ID = uuid.New()
	return r.pool.QueryRow(ctx, query,
		app.ID, app.OrganizationID, app.PackageName, app.AppName, app.VersionCode,
		app.VersionName, app.APKPath, app.APKSize, app.APKHash, app.IconPath,
		app.Description, app.IsSystemApp, app.IsMandatory, app.UploadedBy,
	).Scan(&app.CreatedAt, &app.UpdatedAt)
}

func (r *AppRepository) List(ctx context.Context, orgID uuid.UUID) ([]models.AppPackage, error) {
	query := `
		SELECT id, organization_id, package_name, app_name, version_code,
		       version_name, apk_path, apk_size, apk_hash, icon_path,
		       description, is_system_app, is_mandatory, uploaded_by,
		       created_at, updated_at
		FROM app_repository
		WHERE organization_id = $1
		ORDER BY app_name ASC
	`
	rows, err := r.pool.Query(ctx, query, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var apps []models.AppPackage
	for rows.Next() {
		var a models.AppPackage
		err := rows.Scan(
			&a.ID, &a.OrganizationID, &a.PackageName, &a.AppName, &a.VersionCode,
			&a.VersionName, &a.APKPath, &a.APKSize, &a.APKHash, &a.IconPath,
			&a.Description, &a.IsSystemApp, &a.IsMandatory, &a.UploadedBy,
			&a.CreatedAt, &a.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		apps = append(apps, a)
	}
	return apps, nil
}

func (r *AppRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.AppPackage, error) {
	query := `
		SELECT id, organization_id, package_name, app_name, version_code,
		       version_name, apk_path, apk_size, apk_hash, icon_path,
		       description, is_system_app, is_mandatory, uploaded_by,
		       created_at, updated_at
		FROM app_repository
		WHERE id = $1
	`
	var a models.AppPackage
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&a.ID, &a.OrganizationID, &a.PackageName, &a.AppName, &a.VersionCode,
		&a.VersionName, &a.APKPath, &a.APKSize, &a.APKHash, &a.IconPath,
		&a.Description, &a.IsSystemApp, &a.IsMandatory, &a.UploadedBy,
		&a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

func (r *AppRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM app_repository WHERE id = $1`
	result, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *AppRepository) GetByPackageAndVersion(ctx context.Context, orgID uuid.UUID, packageName string, versionCode int) (*models.AppPackage, error) {
	query := `
		SELECT id, organization_id, package_name, app_name, version_code,
		       version_name, apk_path, apk_size, apk_hash, icon_path,
		       description, is_system_app, is_mandatory, uploaded_by,
		       created_at, updated_at
		FROM app_repository
		WHERE organization_id = $1 AND package_name = $2 AND version_code = $3
	`
	var a models.AppPackage
	err := r.pool.QueryRow(ctx, query, orgID, packageName, versionCode).Scan(
		&a.ID, &a.OrganizationID, &a.PackageName, &a.AppName, &a.VersionCode,
		&a.VersionName, &a.APKPath, &a.APKSize, &a.APKHash, &a.IconPath,
		&a.Description, &a.IsSystemApp, &a.IsMandatory, &a.UploadedBy,
		&a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}
