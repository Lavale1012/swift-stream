// Package awsclient loads the AWS configuration shared by the worker.
package awsclient

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

var cfg aws.Config

// Init loads credentials and region from the default chain (environment
// variables, the ~/.aws files, or an attached IAM role) and confirms they
// work by asking STS who we are. Call it once at startup, before Config.
func Init(ctx context.Context) error {
	loaded, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("load aws config: %w", err)
	}
	if loaded.Region == "" {
		return errors.New("aws region not set: set AWS_REGION or a region in ~/.aws/config")
	}

	id, err := sts.NewFromConfig(loaded).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return fmt.Errorf("verify aws credentials: %w", err)
	}

	cfg = loaded
	log.Printf("aws: authenticated as %s in %s", aws.ToString(id.Arn), loaded.Region)
	return nil
}

// Config returns the configuration loaded by Init. Pass it to a service
// constructor, for example s3.NewFromConfig(awsclient.Config()).
func Config() aws.Config {
	return cfg
}
