package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/lavale1012/ss-go-wrkr/awsclient"
	"github.com/lavale1012/ss-go-wrkr/consumer"
	"github.com/lavale1012/ss-go-wrkr/job"
	"github.com/lavale1012/ss-go-wrkr/jobstate"
	"github.com/lavale1012/ss-go-wrkr/media"
	"github.com/lavale1012/ss-go-wrkr/workdir"
)

const (
	awsInitTimeout = 10 * time.Second
	// maxAttempts is how many deliveries a message gets before the worker
	// gives up on it. It stands in for a dead-letter queue until there is one.
	maxAttempts = 5
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker exiting", "error", err)
		os.Exit(1)
	}
}

func run() error {
	queueURL := os.Getenv("JOB_QUEUE_URL")
	if queueURL == "" {
		return errors.New("JOB_QUEUE_URL is not set")
	}
	outputBucket := os.Getenv("OUTPUT_BUCKET")
	if outputBucket == "" {
		return errors.New("OUTPUT_BUCKET is not set")
	}
	jobsTable := os.Getenv("JOBS_TABLE")
	if jobsTable == "" {
		return errors.New("JOBS_TABLE is not set")
	}
	if err := media.CheckTools(); err != nil {
		return err
	}

	// Job files go under WORK_DIR, so on Kubernetes it can point at a volume
	// sized for videos. Several workers may share it.
	root := os.Getenv("WORK_DIR")
	if root == "" {
		root = filepath.Join(os.TempDir(), "swift-stream-worker")
	}
	scratch, removed, err := workdir.Open(root)
	if err != nil {
		return fmt.Errorf("open work directory: %w", err)
	}
	defer scratch.Close()
	if removed > 0 {
		slog.Info("removed job files left by workers that died", "directories", removed, "root", root)
	}

	initCtx, cancel := context.WithTimeout(context.Background(), awsInitTimeout)
	err = awsclient.Init(initCtx)
	cancel()
	if err != nil {
		return err
	}

	// SIGTERM is what Kubernetes sends on scale-down. The first signal stops
	// polling, stops the current job and releases its message to another
	// worker; handling is then restored to the default so a second signal
	// exits immediately.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop)

	cfg := awsclient.Config()
	store := jobstate.New(dynamodb.NewFromConfig(cfg), jobsTable)
	jobs := job.NewHandler(s3.NewFromConfig(cfg), store, outputBucket, scratch.Path, process)

	slog.Info("worker started", "queue_url", queueURL, "output_bucket", outputBucket,
		"jobs_table", jobsTable, "work_dir", scratch.Path)
	consumer.New(sqs.NewFromConfig(cfg), queueURL, jobs.Handle, maxAttempts).Run(ctx)
	slog.Info("worker stopped")
	return nil
}

// process turns a downloaded video into the file to store. For now that is
// only its audio track; the rest of the pipeline (probe, transcode, package)
// goes here.
func process(ctx context.Context, sourcePath, outDir string) (job.Output, error) {
	audioPath, err := media.ExtractAudio(ctx, sourcePath, outDir)
	if err != nil {
		return job.Output{}, err
	}
	return job.Output{Path: audioPath, ContentType: media.AudioContentType}, nil
}
