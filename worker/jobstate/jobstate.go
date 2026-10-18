// Package jobstate records each job's progress in DynamoDB, so the status
// page can show it and a redelivered message for a finished job can be
// recognised and skipped.
//
// One item per job, keyed by job_id:
//
//	status         processing | completed | failed
//	source_bucket  where the uploaded video is
//	source_key
//	attempt        SQS receive count of the latest attempt
//	started_at     when the latest attempt started (RFC 3339, UTC)
//	completed_at   set once completed
//	output_bucket  set once completed
//	output_key
//	error          why the latest attempt failed; removed on a new attempt
//	updated_at
package jobstate

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Values of the status attribute.
const (
	StatusProcessing = "processing"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
)

const writeTimeout = 10 * time.Second

// notCompleted lets a write through unless the job has already completed.
const notCompleted = "attribute_not_exists(#job_id) OR #status <> :completed"

// DynamoDB is the part of the DynamoDB client the store needs.
type DynamoDB interface {
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

// Store writes job state to one DynamoDB table.
type Store struct {
	client DynamoDB
	table  string
	now    func() time.Time
}

func New(client DynamoDB, table string) *Store {
	return &Store{client: client, table: table, now: time.Now}
}

// Start marks the job as processing for this attempt. If the job has already
// completed it changes nothing and reports done.
func (s *Store) Start(ctx context.Context, jobID, sourceBucket, sourceKey string, attempt int) (done bool, err error) {
	err = s.update(ctx, jobID,
		"SET #status = :processing, #source_bucket = :source_bucket, #source_key = :source_key, "+
			"#attempt = :attempt, #started_at = :now, #updated_at = :now REMOVE #error",
		notCompleted,
		map[string]types.AttributeValue{
			":processing":    str(StatusProcessing),
			":completed":     str(StatusCompleted),
			":source_bucket": str(sourceBucket),
			":source_key":    str(sourceKey),
			":attempt":       &types.AttributeValueMemberN{Value: strconv.Itoa(attempt)},
		},
		"status", "source_bucket", "source_key", "attempt", "started_at", "updated_at", "error", "job_id")
	var conflict *types.ConditionalCheckFailedException
	if errors.As(err, &conflict) {
		return true, nil
	}
	return false, err
}

// Complete marks the job as completed and records where its output is.
func (s *Store) Complete(ctx context.Context, jobID, outputBucket, outputKey string) error {
	return s.update(ctx, jobID,
		"SET #status = :completed, #output_bucket = :output_bucket, #output_key = :output_key, "+
			"#completed_at = :now, #updated_at = :now REMOVE #error",
		"",
		map[string]types.AttributeValue{
			":completed":     str(StatusCompleted),
			":output_bucket": str(outputBucket),
			":output_key":    str(outputKey),
		},
		"status", "output_bucket", "output_key", "completed_at", "updated_at", "error")
}

// Fail records why the latest attempt failed. SQS may still retry the job,
// in which case the next Start sets it back to processing. A job that has
// already completed is left alone, so a late duplicate cannot mark it failed.
func (s *Store) Fail(ctx context.Context, jobID, reason string) error {
	err := s.update(ctx, jobID,
		"SET #status = :failed, #error = :error, #updated_at = :now",
		notCompleted,
		map[string]types.AttributeValue{
			":failed":    str(StatusFailed),
			":completed": str(StatusCompleted),
			":error":     str(reason),
		},
		"status", "error", "updated_at", "job_id")
	var conflict *types.ConditionalCheckFailedException
	if errors.As(err, &conflict) {
		return nil
	}
	return err
}

// update runs one UpdateItem on the job's item. Every attribute name goes
// through a #placeholder because some, such as status, are DynamoDB reserved
// words; names lists the ones the expressions use, since DynamoDB rejects
// unused placeholders.
func (s *Store) update(ctx context.Context, jobID, update, condition string, values map[string]types.AttributeValue, names ...string) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()

	values[":now"] = str(s.now().UTC().Format(time.RFC3339))
	in := &dynamodb.UpdateItemInput{
		TableName:                 aws.String(s.table),
		Key:                       map[string]types.AttributeValue{"job_id": str(jobID)},
		UpdateExpression:          aws.String(update),
		ExpressionAttributeNames:  map[string]string{},
		ExpressionAttributeValues: values,
	}
	if condition != "" {
		in.ConditionExpression = aws.String(condition)
	}
	for _, name := range names {
		in.ExpressionAttributeNames["#"+name] = name
	}
	_, err := s.client.UpdateItem(ctx, in)
	return err
}

func str(s string) types.AttributeValue {
	return &types.AttributeValueMemberS{Value: s}
}
