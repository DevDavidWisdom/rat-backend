package storage

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type MinIOConfig struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
}

type MinIOClient struct {
	client *minio.Client
	bucket string
	creds  *credentials.Credentials
}

func NewMinIOClient(cfg *MinIOConfig) (*MinIOClient, error) {
	creds := credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, "")
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  creds,
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to check bucket: %w", err)
	}
	if !exists {
		err = client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to create bucket: %w", err)
		}
		log.Printf("Created MinIO bucket: %s", cfg.Bucket)
	}

	return &MinIOClient{
		client: client,
		bucket: cfg.Bucket,
		creds:  creds,
	}, nil
}

func (m *MinIOClient) UploadFile(ctx context.Context, objectKey string, reader io.Reader, size int64, contentType string) error {
	_, err := m.client.PutObject(ctx, m.bucket, objectKey, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	return err
}

func (m *MinIOClient) GetPresignedURL(ctx context.Context, objectKey string, expiry time.Duration) (string, error) {
	url, err := m.client.PresignedGetObject(ctx, m.bucket, objectKey, expiry, nil)
	if err != nil {
		return "", err
	}
	return url.String(), nil
}

func (m *MinIOClient) DeleteFile(ctx context.Context, objectKey string) error {
	return m.client.RemoveObject(ctx, m.bucket, objectKey, minio.RemoveObjectOptions{})
}

func (m *MinIOClient) GetExternalPresignedURL(ctx context.Context, objectKey string, expiry time.Duration, externalEndpoint string, useSSL bool) (string, error) {
	extClient, err := minio.New(externalEndpoint, &minio.Options{
		Creds:  m.creds,
		Secure: useSSL,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create external MinIO client: %w", err)
	}
	url, err := extClient.PresignedGetObject(ctx, m.bucket, objectKey, expiry, nil)
	if err != nil {
		return "", err
	}
	return url.String(), nil
}
