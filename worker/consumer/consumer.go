// Package consumer receives job messages from SQS and hands them to a handler.
package consumer

import (
	"context"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

const (
	// longPollSeconds is the SQS maximum. A receive waits this long for a
	// message instead of returning empty straight away.
	longPollSeconds = 20
	receiveTimeout  = (longPollSeconds + 10) * time.Second
	deleteTimeout   = 10 * time.Second
	defaultBackoff  = 5 * time.Second
)

// SQS is the part of the SQS client the consumer needs.
type SQS interface {
	ReceiveMessage(ctx context.Context, params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, params *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
}

// Handler processes one message. Returning an error leaves the message on
// the queue to be delivered again.
type Handler func(ctx context.Context, msg types.Message) error

// Consumer polls one queue and runs a Handler for each message, one at a time.
type Consumer struct {
	client   SQS
	queueURL string
	handle   Handler
	backoff  time.Duration
}

func New(client SQS, queueURL string, handle Handler) *Consumer {
	return &Consumer{client: client, queueURL: queueURL, handle: handle, backoff: defaultBackoff}
}

// Run polls the queue until ctx is cancelled. A message that has already
// been received when ctx is cancelled is still processed to the end before
// Run returns.
func (c *Consumer) Run(ctx context.Context) {
	for ctx.Err() == nil {
		msgs, err := c.receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.ErrorContext(ctx, "receive from queue failed", "error", err)
			// Wait before retrying so a persistent failure does not spin.
			select {
			case <-ctx.Done():
				return
			case <-time.After(c.backoff):
			}
			continue
		}
		for _, msg := range msgs {
			// Shutdown must not interrupt a message that is in progress.
			c.process(context.WithoutCancel(ctx), msg)
		}
	}
}

func (c *Consumer) receive(ctx context.Context) ([]types.Message, error) {
	ctx, cancel := context.WithTimeout(ctx, receiveTimeout)
	defer cancel()
	out, err := c.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl: aws.String(c.queueURL),
		// One at a time: a message this worker holds but is not yet working
		// on would use up its visibility timeout while it waits.
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     longPollSeconds,
	})
	if err != nil {
		return nil, err
	}
	return out.Messages, nil
}

func (c *Consumer) process(ctx context.Context, msg types.Message) {
	log := slog.With("message_id", aws.ToString(msg.MessageId))

	log.InfoContext(ctx, "message received")
	if err := c.handle(ctx, msg); err != nil {
		// Not deleted: SQS makes it visible again after the visibility
		// timeout, and moves it to the dead-letter queue once it has been
		// received too many times.
		log.ErrorContext(ctx, "message failed, left on queue", "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, deleteTimeout)
	defer cancel()
	_, err := c.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(c.queueURL),
		ReceiptHandle: msg.ReceiptHandle,
	})
	if err != nil {
		// The work is done but SQS will deliver the message again.
		log.ErrorContext(ctx, "delete failed, message will be redelivered", "error", err)
		return
	}
	log.InfoContext(ctx, "message deleted")
}
