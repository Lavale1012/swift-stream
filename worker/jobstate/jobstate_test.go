package jobstate

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeDynamoDB returns a fixed error from UpdateItem.
type fakeDynamoDB struct{ err error }

func (f fakeDynamoDB) UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return &dynamodb.UpdateItemOutput{}, f.err
}

func TestConditionalCheckFailure(t *testing.T) {
	conflict := &types.ConditionalCheckFailedException{Message: aws.String("job is completed")}
	s := New(fakeDynamoDB{err: conflict}, "jobs")

	done, err := s.Start(context.Background(), "abc", "raw", "uploads/abc/source.mp4", 1)
	if err != nil || !done {
		t.Errorf("Start on a completed job = %v, %v; want done and no error", done, err)
	}
	if err := s.Fail(context.Background(), "abc", "boom"); err != nil {
		t.Errorf("Fail on a completed job = %v, want nil", err)
	}
}

func TestOtherErrorsAreReturned(t *testing.T) {
	throttled := errors.New("ProvisionedThroughputExceeded")
	s := New(fakeDynamoDB{err: throttled}, "jobs")

	if done, err := s.Start(context.Background(), "abc", "raw", "k", 1); !errors.Is(err, throttled) || done {
		t.Errorf("Start = %v, %v; want the error and not done", done, err)
	}
	if err := s.Complete(context.Background(), "abc", "out", "k"); !errors.Is(err, throttled) {
		t.Errorf("Complete = %v, want the error", err)
	}
	if err := s.Fail(context.Background(), "abc", "boom"); !errors.Is(err, throttled) {
		t.Errorf("Fail = %v, want the error", err)
	}
}

// TestAgainstDynamoDB runs the store against a real DynamoDB API, which is the
// only way to check the expressions themselves. Point DYNAMODB_ENDPOINT at
// DynamoDB Local or a moto server to run it, for example:
//
//	docker run -p 8000:8000 amazon/dynamodb-local
//	DYNAMODB_ENDPOINT=http://localhost:8000 go test ./jobstate/
func TestAgainstDynamoDB(t *testing.T) {
	endpoint := os.Getenv("DYNAMODB_ENDPOINT")
	if endpoint == "" {
		t.Skip("set DYNAMODB_ENDPOINT to run against DynamoDB Local")
	}
	ctx := context.Background()
	client := dynamodb.New(dynamodb.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(endpoint),
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "local", SecretAccessKey: "local"}, nil
		}),
	})
	table := "jobs-" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")) + "-" + time.Now().Format("150405.000")
	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:            aws.String(table),
		BillingMode:          types.BillingModePayPerRequest,
		AttributeDefinitions: []types.AttributeDefinition{{AttributeName: aws.String("job_id"), AttributeType: types.ScalarAttributeTypeS}},
		KeySchema:            []types.KeySchemaElement{{AttributeName: aws.String("job_id"), KeyType: types.KeyTypeHash}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(table)}) })

	s := New(client, table)
	get := func() map[string]string {
		t.Helper()
		out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
			TableName: aws.String(table),
			Key:       map[string]types.AttributeValue{"job_id": str("abc")},
		})
		if err != nil {
			t.Fatal(err)
		}
		item := map[string]string{}
		for k, v := range out.Item {
			switch v := v.(type) {
			case *types.AttributeValueMemberS:
				item[k] = v.Value
			case *types.AttributeValueMemberN:
				item[k] = v.Value
			}
		}
		return item
	}

	// First attempt fails.
	if done, err := s.Start(ctx, "abc", "raw", "uploads/abc/source.mp4", 1); err != nil || done {
		t.Fatalf("Start = %v, %v", done, err)
	}
	if err := s.Fail(ctx, "abc", "ffmpeg exited 1"); err != nil {
		t.Fatal(err)
	}
	item := get()
	if item["status"] != StatusFailed || item["error"] != "ffmpeg exited 1" || item["attempt"] != "1" {
		t.Errorf("after a failed attempt: %v", item)
	}

	// Second attempt succeeds and clears the error.
	if done, err := s.Start(ctx, "abc", "raw", "uploads/abc/source.mp4", 2); err != nil || done {
		t.Fatalf("Start = %v, %v", done, err)
	}
	if item := get(); item["status"] != StatusProcessing || item["attempt"] != "2" || item["error"] != "" {
		t.Errorf("after the retry started: %v", item)
	}
	if err := s.Complete(ctx, "abc", "processed", "uploads/abc/audio.mp3"); err != nil {
		t.Fatal(err)
	}
	item = get()
	if item["status"] != StatusCompleted || item["output_key"] != "uploads/abc/audio.mp3" || item["completed_at"] == "" {
		t.Errorf("after completing: %v", item)
	}

	// A redelivered message is recognised, and a late failure is ignored.
	if done, err := s.Start(ctx, "abc", "raw", "uploads/abc/source.mp4", 3); err != nil || !done {
		t.Errorf("Start on a completed job = %v, %v; want done", done, err)
	}
	if err := s.Fail(ctx, "abc", "late duplicate"); err != nil {
		t.Fatal(err)
	}
	if after := get(); after["status"] != StatusCompleted || after["attempt"] != "2" || after["error"] != "" {
		t.Errorf("completed job was changed: %v", after)
	}
}
