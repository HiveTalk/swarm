package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Storage struct {
	client     *s3.Client
	bucket     string
	publicURL  string
	serviceURL string
}

type S3Config struct {
	Endpoint        string
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	PublicURL       string
	ServiceURL      string
}

func NewS3Storage(cfg S3Config) (*S3Storage, error) {
	// Create custom credentials provider
	credProvider := credentials.NewStaticCredentialsProvider(
		cfg.AccessKeyID,
		cfg.SecretAccessKey,
		"",
	)

	// Load AWS config with custom endpoint
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithCredentialsProvider(credProvider),
		awsconfig.WithRegion(cfg.Region),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create S3 client with custom endpoint for Tigris
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		o.Region = cfg.Region
		o.UsePathStyle = false
	})

	// Verify bucket access
	_, err = client.HeadBucket(context.Background(), &s3.HeadBucketInput{
		Bucket: aws.String(cfg.Bucket),
	})
	if err != nil {
		log.Printf("Warning: Could not verify S3 bucket access: %v", err)
	}

	log.Printf("S3 storage initialized: endpoint=%s bucket=%s", cfg.Endpoint, cfg.Bucket)

	return &S3Storage{
		client:     client,
		bucket:     cfg.Bucket,
		publicURL:  cfg.PublicURL,
		serviceURL: cfg.ServiceURL,
	}, nil
}

func (s *S3Storage) StoreBlob(ctx context.Context, sha256 string, body []byte) error {
	contentType := detectContentType(body)

	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(sha256),
		Body:        bytes.NewReader(body),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("failed to upload blob to S3: %w", err)
	}

	log.Printf("S3: Stored blob %s (%d bytes, type: %s)", sha256, len(body), contentType)
	return nil
}

func (s *S3Storage) LoadBlob(ctx context.Context, sha256 string) (io.ReadSeeker, *url.URL, error) {
	// If public URL is configured, redirect to it (recommended for CDN/S3)
	if s.publicURL != "" {
		redirectURL, err := url.Parse(fmt.Sprintf("%s/%s", s.publicURL, sha256))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse redirect URL: %w", err)
		}
		log.Printf("S3: Redirecting to %s", redirectURL.String())
		return nil, redirectURL, nil
	}

	// Otherwise, stream the blob directly
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(sha256),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get blob from S3: %w", err)
	}

	// Read into memory for ReadSeeker interface
	data, err := io.ReadAll(result.Body)
	result.Body.Close()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read blob data: %w", err)
	}

	return bytes.NewReader(data), nil, nil
}

func (s *S3Storage) DeleteBlob(ctx context.Context, sha256 string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(sha256),
	})
	if err != nil {
		return fmt.Errorf("failed to delete blob from S3: %w", err)
	}

	log.Printf("S3: Deleted blob %s", sha256)
	return nil
}

func (s *S3Storage) ListBlobs(ctx context.Context) ([]BlobInfo, error) {
	var blobs []BlobInfo

	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list S3 objects: %w", err)
		}

		for _, obj := range page.Contents {
			key := aws.ToString(obj.Key)
			// Only include valid SHA256 hashes (64 hex chars)
			if len(key) == 64 && isValidHex(key) {
				blobURL := s.serviceURL + "/" + key
				if s.publicURL != "" {
					blobURL = s.publicURL + "/" + key
				}

				// Get content type from S3 object metadata
				contentType := "application/octet-stream"
				headResult, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
					Bucket: aws.String(s.bucket),
					Key:    aws.String(key),
				})
				if err == nil && headResult.ContentType != nil {
					contentType = *headResult.ContentType
				}

				blobs = append(blobs, BlobInfo{
					SHA256:   key,
					Size:     aws.ToInt64(obj.Size),
					Type:     contentType,
					URL:      blobURL,
					Uploaded: obj.LastModified.Unix(),
				})
			}
		}
	}

	return blobs, nil
}

type BlobInfo struct {
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	Uploaded int64  `json:"uploaded"`
}

func isValidHex(s string) bool {
	for _, char := range s {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func detectContentType(data []byte) string {
	if len(data) < 512 {
		return "application/octet-stream"
	}
	// Use Go's built-in content type detection
	return getContentType(data[:512])
}

func getContentType(header []byte) string {
	// Check for common file signatures
	if len(header) >= 8 {
		// PNG
		if header[0] == 0x89 && header[1] == 0x50 && header[2] == 0x4E && header[3] == 0x47 {
			return "image/png"
		}
		// JPEG
		if header[0] == 0xFF && header[1] == 0xD8 && header[2] == 0xFF {
			return "image/jpeg"
		}
		// GIF
		if header[0] == 0x47 && header[1] == 0x49 && header[2] == 0x46 {
			return "image/gif"
		}
		// WebP
		if header[0] == 0x52 && header[1] == 0x49 && header[2] == 0x46 && header[3] == 0x46 &&
			header[8] == 0x57 && header[9] == 0x45 && header[10] == 0x42 && header[11] == 0x50 {
			return "image/webp"
		}
		// MP4
		if header[4] == 0x66 && header[5] == 0x74 && header[6] == 0x79 && header[7] == 0x70 {
			return "video/mp4"
		}
		// WebM
		if header[0] == 0x1A && header[1] == 0x45 && header[2] == 0xDF && header[3] == 0xA3 {
			return "video/webm"
		}
		// PDF
		if header[0] == 0x25 && header[1] == 0x50 && header[2] == 0x44 && header[3] == 0x46 {
			return "application/pdf"
		}
	}
	return "application/octet-stream"
}

func getS3ConfigFromEnv() *S3Config {
	endpoint := os.Getenv("S3_ENDPOINT")
	bucket := os.Getenv("S3_BUCKET")
	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")

	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		return nil
	}

	region := os.Getenv("S3_REGION")
	if region == "" {
		region = "auto"
	}

	publicURL := os.Getenv("S3_PUBLIC_URL")

	return &S3Config{
		Endpoint:        endpoint,
		Bucket:          bucket,
		Region:          region,
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
		PublicURL:       publicURL,
	}
}
