package consumer

import (
	"context"
	"errors"
	"slices"
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
	// visibility records the VisibilityTimeout of each ChangeMessageVisibility
	// call, per receipt handle.
	visibility map[string][]int32
	// drained is closed when the script runs out, so a test knows every
	// scripted message has been handled.
	drained chan struct{}
}

func newFakeSQS(script ...receiveResult) *fakeSQS {
	return &fakeSQS{script: script, drained: make(chan struct{}), visibility: map[string][]int32{}}
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

func (f *fakeSQS) ChangeMessageVisibility(_ context.Context, in *sqs.ChangeMessageVisibilityInput, _ ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	handle := aws.ToString(in.ReceiptHandle)
	f.visibility[handle] = append(f.visibility[handle], in.VisibilityTimeout)
	return &sqs.ChangeMessageVisibilityOutput{}, nil
}

func (f *fakeSQS) visibilityChanges(handle string) []int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.visibility[handle])
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

			if !slices.Equal(handled, tt.wantHandled) {
				t.Errorf("handled %v, want %v", handled, tt.wantHandled)
			}
			if got := f.deletedHandles(); !slices.Equal(got, tt.wantDeleted) {
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
	if in.VisibilityTimeout != 120 {
		t.Errorf("visibility timeout = %ds, want 120s", in.VisibilityTimeout)
	}
	if !slices.Contains(in.MessageSystemAttributeNames, types.MessageSystemAttributeNameApproximateReceiveCount) {
		t.Errorf("attributes = %v, want the receive count", in.MessageSystemAttributeNames)
	}
	if got := aws.ToString(f.deletes[0].QueueUrl); got != testQueueURL {
		t.Errorf("delete queue = %q", got)
	}
}

func TestRunExtendsVisibilityWhileHandling(t *testing.T) {
	f := newFakeSQS(receiveResult{msgs: []types.Message{message("a")}})
	c := New(f, testQueueURL, func(context.Context, types.Message) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	})
	c.heartbeat = 10 * time.Millisecond

	runUntilDrained(t, c, f)

	changes := f.visibilityChanges("receipt-a")
	if len(changes) < 3 {
		t.Fatalf("visibility extended %d times during a 100ms job with a 10ms heartbeat, want several", len(changes))
	}
	for _, v := range changes {
		if v != 120 {
			t.Errorf("visibility set to %ds, want 120s", v)
		}
	}
	// The heartbeat must stop with the handler, or it would keep a deleted
	// or failed message's receipt handle alive.
	time.Sleep(50 * time.Millisecond)
	if after := f.visibilityChanges("receipt-a"); len(after) != len(changes) {
		t.Errorf("visibility changed %d more times after the handler returned", len(after)-len(changes))
	}
}

// shutDownDuring runs the consumer on one message and cancels it, as SIGTERM
// would, once the handler has started. It returns after Run does.
func shutDownDuring(t *testing.T, f *fakeSQS, handle Handler) {
	t.Helper()
	started := make(chan struct{})
	c := New(f, testQueueURL, func(ctx context.Context, msg types.Message) error {
		close(started)
		return handle(ctx, msg)
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
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after shutdown")
	}
	if len(f.receives) != 1 {
		t.Errorf("received %d times, want no receive after shutdown", len(f.receives))
	}
}

func TestRunReleasesMessageOnShutdown(t *testing.T) {
	f := newFakeSQS(receiveResult{msgs: []types.Message{message("a")}})
	shutDownDuring(t, f, func(ctx context.Context, _ types.Message) error {
		<-ctx.Done()
		return ctx.Err()
	})

	if got := f.visibilityChanges("receipt-a"); !slices.Equal(got, []int32{0}) {
		t.Errorf("visibility changes = %v, want one release to 0", got)
	}
	if got := f.deletedHandles(); len(got) != 0 {
		t.Errorf("deleted %v, want the message left for another worker", got)
	}
}

func TestRunDeletesMessageThatFinishesDuringShutdown(t *testing.T) {
	f := newFakeSQS(receiveResult{msgs: []types.Message{message("a")}})
	shutDownDuring(t, f, func(ctx context.Context, _ types.Message) error {
		<-ctx.Done()
		return nil // finished anyway
	})

	if got := f.deletedHandles(); !slices.Equal(got, []string{"receipt-a"}) {
		t.Errorf("deleted %v, want the finished message", got)
	}
	if got := f.visibilityChanges("receipt-a"); len(got) != 0 {
		t.Errorf("visibility changes = %v, want none for a finished message", got)
	}
}
