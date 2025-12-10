package utils

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// LocalStorage implements StorageService for local disk.
type LocalStorage struct {
	UploadDir string
}

func (ls *LocalStorage) SaveFile(filename string, data []byte, contentType string) (string, error) {
	fullPath := filepath.Join(ls.UploadDir, filename)
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return "", err
	}
	return "/uploads/" + filename, nil
}

func (ls *LocalStorage) DeleteFile(path string) error {
	// Path is like "/uploads/filename.ext"
	filename := filepath.Base(path)
	fullPath := filepath.Join(ls.UploadDir, filename)
	err := os.Remove(fullPath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// S3Storage implements StorageService for S3-compatible object storage.
type S3Storage struct {
	Client     *minio.Client
	BucketName string
	PublicURL  string
}

func NewS3Storage(endpoint, accessKey, secretKey, bucket, region, publicURL string, useSSL bool) (*S3Storage, error) {
	// Strip scheme if present
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")

	var creds *credentials.Credentials
	if accessKey == "" || secretKey == "" {
		// Use IAM role credentials if keys are not provided
		creds = credentials.NewIAM("")
	} else {
		creds = credentials.NewStaticV4(accessKey, secretKey, "")
	}

	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  creds,
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		return nil, err
	}

	// Ensure bucket exists
	ctx := context.Background()
	exists, err := minioClient.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to check bucket existence: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("bucket %s does not exist", bucket)
	}

	if publicURL == "" {
		protocol := "http"
		if useSSL {
			protocol = "https"
		}
		publicURL = fmt.Sprintf("%s://%s.%s", protocol, bucket, endpoint)
	}
	// Trim trailing slash from PublicURL
	publicURL = strings.TrimSuffix(publicURL, "/")

	return &S3Storage{
		Client:     minioClient,
		BucketName: bucket,
		PublicURL:  publicURL,
	}, nil
}

func (s3 *S3Storage) SaveFile(filename string, data []byte, contentType string) (string, error) {
	ctx := context.Background()
	reader := bytes.NewReader(data)
	_, err := s3.Client.PutObject(ctx, s3.BucketName, filename, reader, int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%s", s3.PublicURL, filename), nil
}

func (s3 *S3Storage) DeleteFile(path string) error {
	// Path is the full URL like "https://bucket.../filename.ext"
	// We need to extract the filename (key).
	// Simple assumption: filename is the last part of the URL.
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return nil
	}
	key := parts[len(parts)-1]

	ctx := context.Background()
	return s3.Client.RemoveObject(ctx, s3.BucketName, key, minio.RemoveObjectOptions{})
}
