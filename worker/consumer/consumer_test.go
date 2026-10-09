package consumer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

const testQueueURL = "https://sqs.test/queue"

// receiveResult is one scripted reply to ReceiveMessage.
type receiveResult struct {
	msgs []types.Message
	err  error
}

// fakeSQS replays scripted receive results, then behaves like an empty queue:
// it blocks until the context ends, as a long poll would.
type fakeSQS struct {
	mu        sync.Mutex
	script    []receiveResult
	receives  []*sqs.ReceiveMessageInput
	deletes   []*sqs.DeleteMessageInput
	deleteErr error
	// drained is closed when the script runs out, so a test knows every
	// scripted message has been handled.
	drained chan struct{}
}

func newFakeSQS(script ...receiveResult) *fakeSQS {
	return &fakeSQS{script: script, drained: make(chan struct{})}
}

func (f *fakeSQS) ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	f.mu.Lock()
	f.receives = append(f.receives, in)
	if len(f.script) > 0 {
		next := f.script[0]
		f.script = f.script[1:]
		f.mu.Unlock()
		return &sqs.ReceiveMessageOutput{Messages: next.msgs}, next.err
	}
	select {
	case <-f.drained:
	default:
		close(f.drained)
	}
	f.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakeSQS) DeleteMessage(_ context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, in)
	return &sqs.DeleteMessageOutput{}, f.deleteErr
}

func (f *fakeSQS) deletedHandles() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var handles []string
	for _, d := range f.deletes {
		handles = append(handles, aws.ToString(d.ReceiptHandle))
	}
	return handles
}

func message(id string) types.Message {
	return types.Message{MessageId: aws.String(id), ReceiptHandle: aws.String("receipt-" + id)}
}

// runUntilDrained runs the consumer until every scripted receive has been
// handled, then cancels it and waits for Run to return.
func runUntilDrained(t *testing.T, c *Consumer, f *fakeSQS) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx)
	}()

	select {
	case <-f.drained:
	case <-time.After(5 * time.Second):
		t.Fatal("consumer did not work through the scripted messages")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRunDeletesOnlyMessagesThatSucceed(t *testing.T) {
	failure := errors.New("ffmpeg exited 1")
	tests := []struct {
		name        string
		script      []receiveResult
		failIDs     map[string]bool
		deleteErr   error
		wantHandled []string
		wantDeleted []string
	}{
		{
			name:        "success is deleted",
			script:      []receiveResult{{msgs: []types.Message{message("a")}}},
			wantHandled: []string{"a"},
			wantDeleted: []string{"receipt-a"},
		},
		{
			name:        "failure is left on the queue",
			script:      []receiveResult{{msgs: []types.Message{message("a")}}},
			failIDs:     map[string]bool{"a": true},
			wantHandled: []string{"a"},
			wantDeleted: nil,
		},
		{
			name: "a failure does not stop later messages",
			script: []receiveResult{
				{msgs: []types.Message{message("a")}},
				{msgs: []types.Message{message("b")}},
			},
			failIDs:     map[string]bool{"a": true},
			wantHandled: []string{"a", "b"},
			wantDeleted: []string{"receipt-b"},
		},
		{
			name: "empty receive is ignored",
			script: []receiveResult{
				{},
				{msgs: []types.Message{message("a")}},
			},
			wantHandled: []string{"a"},
			wantDeleted: []string{"receipt-a"},
		},
		{
			name: "receive error is retried",
			script: []receiveResult{
				{err: errors.New("network down")},
				{msgs: []types.Message{message("a")}},
			},
			wantHandled: []string{"a"},
			wantDeleted: []string{"receipt-a"},
		},
		{
			name: "delete error does not stop later messages",
			script: []receiveResult{
				{msgs: []types.Message{message("a")}},
				{msgs: []types.Message{message("b")}},
			},
			deleteErr:   errors.New("receipt handle expired"),
			wantHandled: []string{"a", "b"},
			wantDeleted: []string{"receipt-a", "receipt-b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeSQS(tt.script...)
			f.deleteErr = tt.deleteErr
			var handled []string
			c := New(f, testQueueURL, func(_ context.Context, msg types.Message) error {
				id := aws.ToString(msg.MessageId)
				handled = append(handled, id)
				if tt.failIDs[id] {
					return failure
				}
				return nil
			})
			c.backoff = time.Millisecond

			runUntilDrained(t, c, f)

			if !equal(handled, tt.wantHandled) {
				t.Errorf("handled %v, want %v", handled, tt.wantHandled)
			}
			if got := f.deletedHandles(); !equal(got, tt.wantDeleted) {
				t.Errorf("deleted %v, want %v", got, tt.wantDeleted)
			}
		})
	}
}

func TestRunRequestsOneMessageWithLongPolling(t *testing.T) {
	f := newFakeSQS(receiveResult{msgs: []types.Message{message("a")}})
	c := New(f, testQueueURL, func(context.Context, types.Message) error { return nil })

	runUntilDrained(t, c, f)

	in := f.receives[0]
	if got := aws.ToString(in.QueueUrl); got != testQueueURL {
		t.Errorf("receive queue = %q", got)
	}
	if in.MaxNumberOfMessages != 1 || in.WaitTimeSeconds != 20 {
		t.Errorf("max messages = %d, wait = %ds; want 1 and 20s", in.MaxNumberOfMessages, in.WaitTimeSeconds)
	}
	if got := aws.ToString(f.deletes[0].QueueUrl); got != testQueueURL {
		t.Errorf("delete queue = %q", got)
	}
}

// TestRunFinishesCurrentMessageOnShutdown cancels the consumer while the
// handler is running, as SIGTERM would.
func TestRunFinishesCurrentMessageOnShutdown(t *testing.T) {
	f := newFakeSQS(receiveResult{msgs: []types.Message{message("a")}})
	started := make(chan struct{})
	release := make(chan struct{})
	var handlerCtxErr error
	c := New(f, testQueueURL, func(ctx context.Context, _ types.Message) error {
		close(started)
		<-release
		handlerCtxErr = ctx.Err()
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx)
	}()

	<-started
	cancel()
	select {
	case <-done:
		t.Fatal("Run returned while the handler was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the handler finished")
	}

	if handlerCtxErr != nil {
		t.Errorf("handler context was cancelled by shutdown: %v", handlerCtxErr)
	}
	if got := f.deletedHandles(); !equal(got, []string{"receipt-a"}) {
		t.Errorf("deleted %v, want the in-flight message", got)
	}
	if len(f.receives) != 1 {
		t.Errorf("received %d times, want no receive after shutdown", len(f.receives))
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
