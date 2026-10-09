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
	jobs := job.NewHandler(s3.NewFromConfig(cfg), process)

	slog.Info("worker started", "queue_url", queueURL)
	consumer.New(sqs.NewFromConfig(cfg), queueURL, jobs.Handle).Run(ctx)
	slog.Info("worker stopped")
	return nil
}

// process is where FFmpeg will run on the downloaded video (probe, transcode,
// package). It does nothing yet, so every video succeeds.
func process(ctx context.Context, sourcePath string) error {
	return nil
}
