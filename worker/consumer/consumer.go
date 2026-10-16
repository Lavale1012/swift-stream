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
	// visibilityTimeout is how long SQS hides a received message from other
	// workers. It is kept short and renewed on every heartbeat while the
	// message is being worked on, so a worker that dies holds its message for
	// at most this long.
	visibilityTimeout = 2 * time.Minute
	defaultHeartbeat  = visibilityTimeout / 2
	sqsCallTimeout    = 10 * time.Second
	defaultBackoff    = 5 * time.Second
)

// SQS is the part of the SQS client the consumer needs.
type SQS interface {
	ReceiveMessage(ctx context.Context, params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, params *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
	ChangeMessageVisibility(ctx context.Context, params *sqs.ChangeMessageVisibilityInput, optFns ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error)
}

// Handler processes one message. Returning an error leaves the message on
// the queue to be delivered again. ctx is cancelled when the worker shuts
// down; the handler should then stop and return, and the message is handed
// back to the queue for another worker.
type Handler func(ctx context.Context, msg types.Message) error

// Consumer polls one queue and runs a Handler for each message, one at a time.
type Consumer struct {
	client    SQS
	queueURL  string
	handle    Handler
	backoff   time.Duration
	heartbeat time.Duration
}

func New(client SQS, queueURL string, handle Handler) *Consumer {
	return &Consumer{
		client:    client,
		queueURL:  queueURL,
		handle:    handle,
		backoff:   defaultBackoff,
		heartbeat: defaultHeartbeat,
	}
}

// Run polls the queue until ctx is cancelled. If ctx is cancelled while a
// message is being handled, the handler is stopped and the message is
// released so another worker can pick it up straight away.
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
			c.process(ctx, msg)
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
		VisibilityTimeout:   int32(visibilityTimeout / time.Second),
		MessageSystemAttributeNames: []types.MessageSystemAttributeName{
			types.MessageSystemAttributeNameApproximateReceiveCount,
		},
	})
	if err != nil {
		return nil, err
	}
	return out.Messages, nil
}

func (c *Consumer) process(ctx context.Context, msg types.Message) {
	log := slog.With(
		"message_id", aws.ToString(msg.MessageId),
		"attempt", msg.Attributes[string(types.MessageSystemAttributeNameApproximateReceiveCount)],
	)
	log.InfoContext(ctx, "message received")

	stopHeartbeat := c.startHeartbeat(ctx, msg, log)
	err := c.handle(ctx, msg)
	stopHeartbeat()

	shuttingDown := ctx.Err() != nil
	// The calls below must run even though shutdown may have cancelled ctx.
	ctx = context.WithoutCancel(ctx)
	switch {
	case err == nil:
		c.delete(ctx, msg, log)
	case shuttingDown:
		if err := c.setVisibility(ctx, msg, 0); err != nil {
			log.ErrorContext(ctx, "release failed, message returns after its visibility timeout", "error", err)
			return
		}
		log.InfoContext(ctx, "shutting down, message released for another worker")
	default:
		// Not deleted: once the heartbeat stops, SQS makes the message visible
		// again within visibilityTimeout, and moves it to the dead-letter
		// queue once it has been received too many times.
		log.ErrorContext(ctx, "message failed, left on queue", "error", err)
	}
}

// startHeartbeat keeps extending the message's visibility timeout until the
// returned function is called, so SQS does not hand the message to another
// worker while this one is still working on it.
func (c *Consumer) startHeartbeat(ctx context.Context, msg types.Message, log *slog.Logger) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(c.heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.setVisibility(ctx, msg, visibilityTimeout); err != nil {
					log.WarnContext(ctx, "extend visibility failed", "error", err)
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// setVisibility hides the message for d from now. Zero makes it visible to
// other workers immediately.
func (c *Consumer) setVisibility(ctx context.Context, msg types.Message, d time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, sqsCallTimeout)
	defer cancel()
	_, err := c.client.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
		QueueUrl:          aws.String(c.queueURL),
		ReceiptHandle:     msg.ReceiptHandle,
		VisibilityTimeout: int32(d / time.Second),
	})
	return err
}

func (c *Consumer) delete(ctx context.Context, msg types.Message, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, sqsCallTimeout)
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
