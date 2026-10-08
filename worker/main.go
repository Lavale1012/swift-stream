package main

import (
	"context"
	"log"
	"time"

	"github.com/lavale1012/ss-go-wrkr/awsclient"
	"github.com/lavale1012/ss-go-wrkr/server"
)

const awsInitTimeout = 10 * time.Second

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), awsInitTimeout)
	err := awsclient.Init(ctx)
	cancel()
	if err != nil {
		log.Fatal(err)
	}

	if err := server.Run(); err != nil {
		log.Fatal(err)
	}
}
