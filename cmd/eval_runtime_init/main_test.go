package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestRelativeDestPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prefix  string
		key     string
		want    string
		wantErr string
	}{
		{
			name:   "nested under prefix",
			prefix: "datasets/run-1",
			key:    "datasets/run-1/examples.jsonl",
			want:   "examples.jsonl",
		},
		{
			name:   "nested subdirectory",
			prefix: "datasets/run-1",
			key:    "datasets/run-1/subdir/file.txt",
			want:   "subdir/file.txt",
		},
		{
			name:   "prefix only uses basename",
			prefix: "datasets/run-1",
			key:    "datasets/run-1",
			want:   "run-1",
		},
		{
			name:    "path traversal rejected",
			prefix:  "datasets/run-1",
			key:     "datasets/run-1/../../etc/passwd",
			wantErr: "escapes destination directory",
		},
		{
			name:    "dot only rejected",
			prefix:  "datasets",
			key:     "datasets/.",
			wantErr: "invalid object key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := relativeDestPath(tt.prefix, tt.key)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("relativeDestPath() = (%q, nil), want error containing %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("relativeDestPath() error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("relativeDestPath() = %v, want (%q, nil)", err, tt.want)
			}
			if got != tt.want {
				t.Fatalf("relativeDestPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDownloadObjectRejectsInvalidKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	destRoot, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("OpenRoot() = %v", err)
	}
	defer func() { _ = destRoot.Close() }()

	_, err = downloadObject(context.Background(), transfermanager.New(nil), destRoot, "bucket", "datasets/run-1", "datasets/run-1/../../etc/passwd")
	if err == nil {
		t.Fatal("downloadObject() = nil, want relative path error")
	}
}

func TestDownloadObjectWritesNestedFile(t *testing.T) {
	t.Parallel()

	const objectKey = "data/nested/file.txt"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/bucket/"+objectKey {
			_, _ = io.Copy(w, strings.NewReader("hello"))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("ak", "sk", "")),
	)
	if err != nil {
		t.Fatalf("LoadDefaultConfig() = %v", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(srv.URL)
		o.UsePathStyle = true
	})
	tm := transfermanager.New(client)

	dir := t.TempDir()
	destRoot, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("OpenRoot() = %v", err)
	}
	defer func() { _ = destRoot.Close() }()

	written, err := downloadObject(ctx, tm, destRoot, "bucket", "data/", objectKey)
	if err != nil {
		t.Fatalf("downloadObject() = %v, want nil error", err)
	}
	if written != int64(len("hello")) {
		t.Fatalf("downloadObject() wrote %d bytes, want %d", written, len("hello"))
	}

	got, err := os.ReadFile(filepath.Join(dir, "nested", "file.txt"))
	if err != nil {
		t.Fatalf("ReadFile() = %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("file contents = %q, want %q", got, "hello")
	}
}

// benchmarkEnv reads the real S3 credentials from env vars.
// Returns nil if any required var is missing — benchmarks skip when nil.
func benchmarkEnv() (bucket, keyPrefix, endpoint, region, accessKey, secretKey string, ok bool) {
	bucket = os.Getenv(envBucket)
	keyPrefix = os.Getenv(envKey)
	endpoint = os.Getenv(endpointKey)
	region = os.Getenv(regionOptionalKey)
	accessKey = os.Getenv(accessKeyIDKey)
	secretKey = os.Getenv(secretAccessKey)
	ok = bucket != "" && keyPrefix != "" && endpoint != "" && region != "" && accessKey != "" && secretKey != ""
	return
}

// BenchmarkDownloadSequential measures the original sequential GetObject approach.
// Run with real S3 credentials set as env vars:
//
//	TEST_DATA_S3_BUCKET=mlpipeline \
//	TEST_DATA_S3_KEY=offline_blimp \
//	AWS_S3_ENDPOINT=http://minio.minio.svc.cluster.local:9000 \
//	AWS_DEFAULT_REGION=us-west-1 \
//	AWS_ACCESS_KEY_ID=xxx \
//	AWS_SECRET_ACCESS_KEY=yyy \
//	go test -bench=BenchmarkDownload -benchtime=3x -v ./cmd/eval_runtime_init/...
func BenchmarkDownloadSequential(b *testing.B) {
	bucket, keyPrefix, endpoint, region, accessKey, secretKey, ok := benchmarkEnv()
	if !ok {
		b.Skip("skipping: set TEST_DATA_S3_BUCKET, TEST_DATA_S3_KEY, AWS_S3_ENDPOINT, AWS_DEFAULT_REGION, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY to run")
	}

	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		b.Fatalf("LoadDefaultConfig: %v", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dir := b.TempDir()
		destRoot, err := os.OpenRoot(dir)
		if err != nil {
			b.Fatalf("OpenRoot: %v", err)
		}

		start := time.Now()
		paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
			Bucket: aws.String(bucket),
			Prefix: aws.String(keyPrefix),
		})
		var totalBytes int64
		var fileCount int64
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				b.Fatalf("NextPage: %v", err)
			}
			for _, obj := range page.Contents {
				if obj.Key == nil || strings.HasSuffix(*obj.Key, "/") {
					continue
				}
				rel, err := relativeDestPath(keyPrefix, *obj.Key)
				if err != nil {
					b.Fatalf("relativeDestPath: %v", err)
				}
				if dir := filepath.Dir(rel); dir != "." {
					_ = destRoot.MkdirAll(dir, 0o750)
				}
				f, err := destRoot.Create(rel)
				if err != nil {
					b.Fatalf("Create: %v", err)
				}
				resp, err := client.GetObject(ctx, &s3.GetObjectInput{
					Bucket: aws.String(bucket),
					Key:    obj.Key,
				})
				if err != nil {
					_ = f.Close()
					b.Fatalf("GetObject: %v", err)
				}
				n, _ := io.Copy(f, resp.Body)
				_ = resp.Body.Close()
				_ = f.Close()
				totalBytes += n
				fileCount++
			}
		}
		elapsed := time.Since(start)
		_ = destRoot.Close()
		b.ReportMetric(float64(elapsed.Milliseconds()), "ms/op")
		b.ReportMetric(float64(totalBytes/(1024*1024)), "mb")
		b.Logf("sequential: %d files, %d MB in %s", fileCount, totalBytes/(1024*1024), elapsed)
	}
}

// BenchmarkDownloadTransferManager measures the Transfer Manager approach.
func BenchmarkDownloadTransferManager(b *testing.B) {
	bucket, keyPrefix, endpoint, region, accessKey, secretKey, ok := benchmarkEnv()
	if !ok {
		b.Skip("skipping: set TEST_DATA_S3_BUCKET, TEST_DATA_S3_KEY, AWS_S3_ENDPOINT, AWS_DEFAULT_REGION, AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY to run")
	}

	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		b.Fatalf("LoadDefaultConfig: %v", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	tm := transfermanager.New(client)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dir := b.TempDir()
		destRoot, err := os.OpenRoot(dir)
		if err != nil {
			b.Fatalf("OpenRoot: %v", err)
		}

		start := time.Now()
		paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
			Bucket: aws.String(bucket),
			Prefix: aws.String(keyPrefix),
		})
		var totalBytes int64
		var fileCount int64
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				b.Fatalf("NextPage: %v", err)
			}
			for _, obj := range page.Contents {
				if obj.Key == nil || strings.HasSuffix(*obj.Key, "/") {
					continue
				}
				n, err := downloadObject(ctx, tm, destRoot, bucket, keyPrefix, *obj.Key)
				if err != nil {
					b.Fatalf("downloadObject: %v", err)
				}
				totalBytes += n
				fileCount++
			}
		}
		elapsed := time.Since(start)
		_ = destRoot.Close()
		b.ReportMetric(float64(elapsed.Milliseconds()), "ms/op")
		b.ReportMetric(float64(totalBytes/(1024*1024)), "mb")
		b.Logf("transfer-manager: %d files, %d MB in %s", fileCount, totalBytes/(1024*1024), elapsed)
	}
}
