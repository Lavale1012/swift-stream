package main

import (
	"context"
	"log"
	"time"

	"github.com/lavale1012/ss-go-wrkr/awsclient"
)

const awsInitTimeout = 10 * time.Second

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), awsInitTimeout)
	err := awsclient.Init(ctx)
	cancel()
	if err != nil {
		log.Fatal(err)
	}

	// The job loop (consume SQS, run FFmpeg) is not written yet. The HTTP API
	// lives in ../server.
}
