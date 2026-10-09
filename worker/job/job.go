// Package job turns a queue message into work: it downloads the uploaded
// video named in the message, runs the processing step on it, and stores the
// result in the output bucket.
package job

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

const (
	downloadTimeout = 10 * time.Minute
	uploadTimeout   = 10 * time.Minute
)

// S3 is the part of the S3 client the handler needs.
type S3 interface {
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// Output is a file produced by processing, to be stored in the output bucket.
type Output struct {
	Path        string
	ContentType string
}

// ProcessFunc processes the source video at sourcePath and writes its result
// into outDir. Both are deleted once the result has been uploaded.
type ProcessFunc func(ctx context.Context, sourcePath, outDir string) (Output, error)

// Handler downloads each video named in a message, processes it, and uploads
// the result.
type Handler struct {
	s3           S3
	outputBucket string
	process      ProcessFunc
}

func NewHandler(client S3, outputBucket string, process ProcessFunc) *Handler {
	return &Handler{s3: client, outputBucket: outputBucket, process: process}
}

// Handle processes one queue message whose body is an S3 event notification.
// It returns an error if any video in the message fails, so that the whole
// message is retried.
func (h *Handler) Handle(ctx context.Context, msg types.Message) error {
	sources, err := parseEvent(aws.ToString(msg.Body))
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		slog.InfoContext(ctx, "message names no uploaded video, nothing to do",
			"message_id", aws.ToString(msg.MessageId))
		return nil
	}
	for _, src := range sources {
		if err := h.run(ctx, src); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) run(ctx context.Context, src source) error {
	log := slog.With("bucket", src.Bucket, "key", src.Key)

	// Writing the result into the bucket that triggers jobs would create a new
	// upload event for it, and the worker would process its own output forever.
	if src.Bucket == h.outputBucket {
		return errors.New("source bucket is also the output bucket, refusing to process")
	}

	// Each job gets its own directory, removed however the job ends.
	dir, err := os.MkdirTemp("", "job-")
	if err != nil {
		return fmt.Errorf("create job directory: %w", err)
	}
	defer os.RemoveAll(dir)

	// The local name is fixed; only the extension comes from the key.
	sourcePath := filepath.Join(dir, "source"+path.Ext(src.Key))
	size, err := h.download(ctx, src, sourcePath)
	if err != nil {
		return fmt.Errorf("download s3://%s/%s: %w", src.Bucket, src.Key, err)
	}

	log.InfoContext(ctx, "video downloaded, processing", "stage", "process", "bytes", size)
	out, err := h.process(ctx, sourcePath, dir)
	if err != nil {
		return fmt.Errorf("process s3://%s/%s: %w", src.Bucket, src.Key, err)
	}

	// The output sits beside the source's key, so a redelivered message
	// overwrites the same object instead of creating a second one.
	outputKey := path.Join(path.Dir(src.Key), filepath.Base(out.Path))
	if err := h.upload(ctx, out, outputKey); err != nil {
		return fmt.Errorf("upload s3://%s/%s: %w", h.outputBucket, outputKey, err)
	}
	log.InfoContext(ctx, "processing complete", "stage", "upload",
		"output_bucket", h.outputBucket, "output_key", outputKey)
	return nil
}

func (h *Handler) upload(ctx context.Context, out Output, key string) error {
	ctx, cancel := context.WithTimeout(ctx, uploadTimeout)
	defer cancel()

	file, err := os.Open(out.Path)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = h.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(h.outputBucket),
		Key:         aws.String(key),
		Body:        file,
		ContentType: aws.String(out.ContentType),
	})
	return err
}

// download streams the object to dst and returns the number of bytes written.
func (h *Handler) download(ctx context.Context, src source, dst string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	out, err := h.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(src.Bucket),
		Key:    aws.String(src.Key),
	})
	if err != nil {
		return 0, err
	}
	defer out.Body.Close()

	file, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	size, err := io.Copy(file, out.Body)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, err
	}
	// A connection that drops cleanly mid-body would otherwise look complete.
	if want := aws.ToInt64(out.ContentLength); out.ContentLength != nil && size != want {
		return 0, fmt.Errorf("got %d bytes, object is %d", size, want)
	}
	return size, nil
}
