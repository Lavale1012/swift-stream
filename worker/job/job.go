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
	"strconv"
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

// Store records each job's progress. jobstate.Store implements it.
type Store interface {
	// Start marks the job as processing, or reports done if it has already
	// completed.
	Start(ctx context.Context, jobID, sourceBucket, sourceKey string, attempt int) (done bool, err error)
	Complete(ctx context.Context, jobID, outputBucket, outputKey string) error
	Fail(ctx context.Context, jobID, reason string) error
}

// Handler downloads each video named in a message, processes it, and uploads
// the result, recording the job's progress as it goes.
type Handler struct {
	s3           S3
	store        Store
	outputBucket string
	process      ProcessFunc
}

func NewHandler(client S3, store Store, outputBucket string, process ProcessFunc) *Handler {
	return &Handler{s3: client, store: store, outputBucket: outputBucket, process: process}
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
	// SQS counts every delivery, including ones a crashed worker never
	// finished, so this is the true attempt number.
	attempt, _ := strconv.Atoi(msg.Attributes[string(types.MessageSystemAttributeNameApproximateReceiveCount)])
	for _, src := range sources {
		if err := h.run(ctx, src, attempt); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) run(ctx context.Context, src source, attempt int) error {
	// Writing the result into the bucket that triggers jobs would create a new
	// upload event for it, and the worker would process its own output forever.
	if src.Bucket == h.outputBucket {
		return errors.New("source bucket is also the output bucket, refusing to process")
	}
	jobID, err := jobID(src.Key)
	if err != nil {
		return err
	}
	log := slog.With("job_id", jobID, "bucket", src.Bucket, "key", src.Key, "attempt", attempt)

	done, err := h.store.Start(ctx, jobID, src.Bucket, src.Key, attempt)
	if err != nil {
		return fmt.Errorf("record start of job %s: %w", jobID, err)
	}
	if done {
		log.InfoContext(ctx, "job already completed, skipping redelivered message")
		return nil
	}

	outputKey, err := h.work(ctx, log, src)
	if err != nil {
		// A shutdown is not a failure: the message is released and another
		// worker starts the job again.
		if ctx.Err() == nil {
			if ferr := h.store.Fail(ctx, jobID, err.Error()); ferr != nil {
				log.ErrorContext(ctx, "could not record job failure", "error", ferr)
			}
		}
		return err
	}

	// The output is already uploaded, so record it even if shutdown has begun.
	if err := h.store.Complete(context.WithoutCancel(ctx), jobID, h.outputBucket, outputKey); err != nil {
		return fmt.Errorf("record completion of job %s: %w", jobID, err)
	}
	log.InfoContext(ctx, "processing complete", "stage", "upload",
		"output_bucket", h.outputBucket, "output_key", outputKey)
	return nil
}

// work downloads the source, processes it, uploads the result, and returns
// the result's key in the output bucket.
func (h *Handler) work(ctx context.Context, log *slog.Logger, src source) (string, error) {
	// Each job gets its own directory, removed however the job ends.
	dir, err := os.MkdirTemp("", "job-")
	if err != nil {
		return "", fmt.Errorf("create job directory: %w", err)
	}
	defer os.RemoveAll(dir)

	// The local name is fixed; only the extension comes from the key.
	sourcePath := filepath.Join(dir, "source"+path.Ext(src.Key))
	size, err := h.download(ctx, src, sourcePath)
	if err != nil {
		return "", fmt.Errorf("download s3://%s/%s: %w", src.Bucket, src.Key, err)
	}

	log.InfoContext(ctx, "video downloaded, processing", "stage", "process", "bytes", size)
	out, err := h.process(ctx, sourcePath, dir)
	if err != nil {
		return "", fmt.Errorf("process s3://%s/%s: %w", src.Bucket, src.Key, err)
	}

	// The output sits beside the source's key, so a redelivered message
	// overwrites the same object instead of creating a second one.
	outputKey := path.Join(path.Dir(src.Key), filepath.Base(out.Path))
	if err := h.upload(ctx, out, outputKey); err != nil {
		return "", fmt.Errorf("upload s3://%s/%s: %w", h.outputBucket, outputKey, err)
	}
	return outputKey, nil
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
	return size, nil
}
