package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/lavale1012/ss-go-wrkr/awsclient"
	"github.com/lavale1012/ss-go-wrkr/consumer"
	"github.com/lavale1012/ss-go-wrkr/job"
	"github.com/lavale1012/ss-go-wrkr/media"
)

const awsInitTimeout = 10 * time.Second

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
	if err := media.CheckTools(); err != nil {
		return err
	}

	initCtx, cancel := context.WithTimeout(context.Background(), awsInitTimeout)
	err := awsclient.Init(initCtx)
	cancel()
	if err != nil {
		return err
	}

	// SIGTERM is what Kubernetes sends on scale-down. The first signal stops
	// polling and lets the current message finish; handling is then restored
	// to the default so a second signal exits immediately.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop)

	cfg := awsclient.Config()
	jobs := job.NewHandler(s3.NewFromConfig(cfg), outputBucket, process)

	slog.Info("worker started", "queue_url", queueURL, "output_bucket", outputBucket)
	consumer.New(sqs.NewFromConfig(cfg), queueURL, jobs.Handle).Run(ctx)
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
