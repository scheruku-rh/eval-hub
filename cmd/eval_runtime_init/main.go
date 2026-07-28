package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	envBucket         = "TEST_DATA_S3_BUCKET"
	envKey            = "TEST_DATA_S3_KEY"
	envTimeout        = "TEST_DATA_S3_TIMEOUT"
	envInitMode       = "TEST_DATA_INIT_MODE"
	initModeCompare   = "compare"
	secretDir         = "/var/run/secrets/test-data" // #nosec G101 -- K8s secret mount path
	destDir           = "/test_data"
	regionOptionalKey = "AWS_DEFAULT_REGION"
	endpointKey       = "AWS_S3_ENDPOINT"
	accessKeyIDKey    = "AWS_ACCESS_KEY_ID"
	secretAccessKey   = "AWS_SECRET_ACCESS_KEY" // #nosec G101 -- env var name, not a credential value
	defaultTimeout    = 10 * time.Minute
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	if err := run(); err != nil {
		logger.Error("eval-runtime-init failed", "error", err)
		os.Exit(1)
	}
	logger.Info("eval-runtime-init completed")
}

func run() error {
	bucket := strings.TrimSpace(os.Getenv(envBucket))
	keyPrefix := strings.TrimSpace(os.Getenv(envKey))
	if bucket == "" || keyPrefix == "" {
		return fmt.Errorf("%s and %s are required", envBucket, envKey)
	}

	keyPrefix = strings.TrimPrefix(keyPrefix, "/")
	timeout := defaultTimeout
	if raw := strings.TrimSpace(os.Getenv(envTimeout)); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("invalid %s: %w", envTimeout, err)
		}
		timeout = parsed
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	accessKey := readSecret(accessKeyIDKey)
	secretKey := readSecret(secretAccessKey)
	region := readSecret(regionOptionalKey)
	endpoint := readSecret(endpointKey)

	if accessKey == "" {
		return fmt.Errorf("missing required secret %s", accessKeyIDKey)
	}
	if secretKey == "" {
		return fmt.Errorf("missing required secret %s", secretAccessKey)
	}
	if region == "" {
		return fmt.Errorf("missing required secret %s", regionOptionalKey)
	}
	if endpoint == "" {
		return fmt.Errorf("missing required secret %s", endpointKey)
	}

	cfg, err := loadAWSConfig(ctx, region, accessKey, secretKey)
	if err != nil {
		return err
	}

	client := s3.NewFromConfig(cfg, func(options *s3.Options) {
		if endpoint != "" {
			options.BaseEndpoint = aws.String(endpoint)
			options.UsePathStyle = true
		}
	})

	tm := transfermanager.New(client)

	if strings.TrimSpace(os.Getenv(envInitMode)) == initModeCompare {
		return runCompare(ctx, client, tm, bucket, keyPrefix)
	}
	return runTransferManager(ctx, client, tm, bucket, keyPrefix)
}

// runTransferManager downloads using the S3 Transfer Manager (default path).
func runTransferManager(ctx context.Context, client *s3.Client, tm *transfermanager.Client, bucket, keyPrefix string) error {
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		return fmt.Errorf("create dest dir: %w", err)
	}
	destRoot, err := os.OpenRoot(destDir)
	if err != nil {
		return fmt.Errorf("open dest root: %w", err)
	}
	defer func() { _ = destRoot.Close() }()

	slog.Info("starting transfer-manager download", "bucket", bucket, "key", keyPrefix)
	start := time.Now()
	files, bytes, err := downloadAll(ctx, client, bucket, keyPrefix, func(ctx context.Context, key string) (int64, error) {
		return downloadObject(ctx, tm, destRoot, bucket, keyPrefix, key)
	})
	if err != nil {
		return err
	}
	slog.Info("transfer-manager download complete",
		"files", files,
		"mb", bytes/(1024*1024),
		"elapsed_ms", time.Since(start).Milliseconds())
	return nil
}

// runCompare downloads the dataset twice — first with the original sequential
// GetObject (timing only, to a temp dir) then with the Transfer Manager (to
// destDir). Both elapsed times are logged so you can compare directly.
func runCompare(ctx context.Context, client *s3.Client, tm *transfermanager.Client, bucket, keyPrefix string) error {
	// --- sequential pass (timing only) ---
	seqDir, err := os.MkdirTemp("", "eval-init-seq-*")
	if err != nil {
		return fmt.Errorf("create temp dir for sequential pass: %w", err)
	}
	defer func() { _ = os.RemoveAll(seqDir) }()

	seqRoot, err := os.OpenRoot(seqDir)
	if err != nil {
		return fmt.Errorf("open seq root: %w", err)
	}

	slog.Info("compare: starting sequential (GetObject) download", "bucket", bucket, "key", keyPrefix)
	seqStart := time.Now()
	seqFiles, seqBytes, err := downloadAll(ctx, client, bucket, keyPrefix, func(ctx context.Context, key string) (int64, error) {
		return downloadObjectSequential(ctx, client, seqRoot, bucket, keyPrefix, key)
	})
	_ = seqRoot.Close()
	if err != nil {
		return fmt.Errorf("sequential pass failed: %w", err)
	}
	seqElapsed := time.Since(seqStart)
	slog.Info("compare: sequential complete",
		"files", seqFiles,
		"mb", seqBytes/(1024*1024),
		"elapsed_ms", seqElapsed.Milliseconds())

	// --- transfer manager pass (actual data written to destDir) ---
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		return fmt.Errorf("create dest dir: %w", err)
	}
	destRoot, err := os.OpenRoot(destDir)
	if err != nil {
		return fmt.Errorf("open dest root: %w", err)
	}
	defer func() { _ = destRoot.Close() }()

	slog.Info("compare: starting transfer-manager download", "bucket", bucket, "key", keyPrefix)
	tmStart := time.Now()
	tmFiles, tmBytes, err := downloadAll(ctx, client, bucket, keyPrefix, func(ctx context.Context, key string) (int64, error) {
		return downloadObject(ctx, tm, destRoot, bucket, keyPrefix, key)
	})
	if err != nil {
		return fmt.Errorf("transfer-manager pass failed: %w", err)
	}
	tmElapsed := time.Since(tmStart)
	slog.Info("compare: transfer-manager complete",
		"files", tmFiles,
		"mb", tmBytes/(1024*1024),
		"elapsed_ms", tmElapsed.Milliseconds())

	slog.Info("compare: results",
		"sequential_ms", seqElapsed.Milliseconds(),
		"transfer_manager_ms", tmElapsed.Milliseconds(),
		"speedup", fmt.Sprintf("%.2fx", float64(seqElapsed)/float64(tmElapsed)))
	return nil
}

// downloadAll paginates the bucket prefix and calls fn for each object key.
// Returns total file count and bytes written.
func downloadAll(ctx context.Context, client *s3.Client, bucket, keyPrefix string, fn func(context.Context, string) (int64, error)) (fileCount, totalBytes int64, err error) {
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(keyPrefix),
	})
	found := false
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return 0, 0, fmt.Errorf("list objects: %w", err)
		}
		for _, obj := range page.Contents {
			if obj.Key == nil || *obj.Key == "" || strings.HasSuffix(*obj.Key, "/") {
				continue
			}
			found = true
			written, err := fn(ctx, *obj.Key)
			if err != nil {
				return fileCount, totalBytes, err
			}
			fileCount++
			totalBytes += written
		}
	}
	if !found {
		return 0, 0, fmt.Errorf("no objects found for s3://%s/%s", bucket, keyPrefix)
	}
	return fileCount, totalBytes, nil
}

func loadAWSConfig(ctx context.Context, region, accessKey, secretKey string) (aws.Config, error) {
	opts := []func(*config.LoadOptions) error{}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	if accessKey != "" && secretKey != "" {
		provider := credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")
		opts = append(opts, config.WithCredentialsProvider(provider))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load aws config: %w", err)
	}
	return cfg, nil
}

// downloadObject downloads a single object using the S3 Transfer Manager.
func downloadObject(ctx context.Context, tm *transfermanager.Client, destRoot *os.Root, bucket, prefix, key string) (int64, error) {
	rel, err := relativeDestPath(prefix, key)
	if err != nil {
		return 0, err
	}
	if dir := path.Dir(rel); dir != "." {
		if err := destRoot.MkdirAll(dir, 0o750); err != nil {
			return 0, fmt.Errorf("create dir for %q: %w", key, err)
		}
	}
	file, err := destRoot.Create(rel)
	if err != nil {
		return 0, fmt.Errorf("create file %q: %w", key, err)
	}
	defer func() { _ = file.Close() }()

	out, err := tm.DownloadObject(ctx, &transfermanager.DownloadObjectInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String(key),
		WriterAt: file,
	})
	if err != nil {
		return 0, fmt.Errorf("download object %q: %w", key, err)
	}
	if out.ContentLength == nil {
		return 0, nil
	}
	return *out.ContentLength, nil
}

// downloadObjectSequential downloads a single object using the original sequential GetObject.
func downloadObjectSequential(ctx context.Context, client *s3.Client, destRoot *os.Root, bucket, prefix, key string) (int64, error) {
	rel, err := relativeDestPath(prefix, key)
	if err != nil {
		return 0, err
	}
	if dir := path.Dir(rel); dir != "." {
		if err := destRoot.MkdirAll(dir, 0o750); err != nil {
			return 0, fmt.Errorf("create dir for %q: %w", key, err)
		}
	}
	resp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return 0, fmt.Errorf("get object %q: %w", key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	file, err := destRoot.Create(rel)
	if err != nil {
		return 0, fmt.Errorf("create file %q: %w", key, err)
	}
	defer func() { _ = file.Close() }()
	written, err := io.Copy(file, resp.Body)
	if err != nil {
		return 0, fmt.Errorf("write file %q: %w", key, err)
	}
	return written, nil
}

func relativeDestPath(prefix, key string) (string, error) {
	rel := strings.TrimPrefix(key, prefix)
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		rel = path.Base(key)
	}
	rel = filepath.FromSlash(rel)
	if rel == "." || rel == "/" {
		return "", errors.New("invalid object key for destination path")
	}
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("object key escapes destination directory: %q", key)
	}
	return filepath.ToSlash(rel), nil
}

func readSecret(name string) string {
	if name == "" {
		return ""
	}
	content, err := os.ReadFile(filepath.Join(secretDir, name)) // #nosec G304 -- name is a fixed secret key
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}
