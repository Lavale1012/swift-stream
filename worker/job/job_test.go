package job

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

func eventBody(eventName, bucket, key string) string {
	return `{"Records":[{"eventName":"` + eventName + `","s3":{"bucket":{"name":"` + bucket + `"},"object":{"key":"` + key + `","size":11}}}]}`
}

func TestParseEvent(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    []source
		wantErr bool
	}{
		{
			name: "object created",
			body: eventBody("ObjectCreated:Put", "raw", "uploads/abc/source.mp4"),
			want: []source{{Bucket: "raw", Key: "uploads/abc/source.mp4"}},
		},
		{
			name: "multipart upload completed",
			body: eventBody("ObjectCreated:CompleteMultipartUpload", "raw", "uploads/abc/source.mp4"),
			want: []source{{Bucket: "raw", Key: "uploads/abc/source.mp4"}},
		},
		{
			name: "key is url-decoded",
			body: eventBody("ObjectCreated:Put", "raw", "uploads/my+clip%281%29.mp4"),
			want: []source{{Bucket: "raw", Key: "uploads/my clip(1).mp4"}},
		},
		{
			name: "two records",
			body: `{"Records":[
				{"eventName":"ObjectCreated:Put","s3":{"bucket":{"name":"raw"},"object":{"key":"a.mp4"}}},
				{"eventName":"ObjectCreated:Put","s3":{"bucket":{"name":"raw"},"object":{"key":"b.mp4"}}}]}`,
			want: []source{{Bucket: "raw", Key: "a.mp4"}, {Bucket: "raw", Key: "b.mp4"}},
		},
		{
			name: "s3 test event has no records",
			body: `{"Service":"Amazon S3","Event":"s3:TestEvent","Bucket":"raw"}`,
			want: nil,
		},
		{
			name: "object removed is ignored",
			body: eventBody("ObjectRemoved:Delete", "raw", "uploads/abc/source.mp4"),
			want: nil,
		},
		{name: "not json", body: "uploads/abc/source.mp4", wantErr: true},
		{name: "empty body", body: "", wantErr: true},
		{name: "missing key", body: eventBody("ObjectCreated:Put", "raw", ""), wantErr: true},
		{name: "missing bucket", body: eventBody("ObjectCreated:Put", "", "a.mp4"), wantErr: true},
		{name: "bad key encoding", body: eventBody("ObjectCreated:Put", "raw", "a%zz.mp4"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseEvent(tt.body)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// fakeS3 serves one object body and records what is uploaded.
type fakeS3 struct {
	body   io.Reader
	getErr error
	putErr error
	gets   []*s3.GetObjectInput
	puts   []*s3.PutObjectInput
	// putBodies holds what each upload sent, read during the call because the
	// file is gone once the job ends.
	putBodies []string
}

func (f *fakeS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.gets = append(f.gets, in)
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(f.body)}, nil
}

func (f *fakeS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.puts = append(f.puts, in)
	data, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	f.putBodies = append(f.putBodies, string(data))
	if f.putErr != nil {
		return nil, f.putErr
	}
	return &s3.PutObjectOutput{}, nil
}

// failingReader yields some bytes and then an error, like a dropped connection.
type failingReader struct{ sent bool }

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, "partial"), nil
	}
	return 0, errors.New("connection reset")
}

func message(body string) types.Message {
	return types.Message{
		MessageId:  aws.String("m1"),
		Body:       aws.String(body),
		Attributes: map[string]string{"ApproximateReceiveCount": "2"},
	}
}

// fakeStore records the job state calls it receives.
type fakeStore struct {
	done        bool
	startErr    error
	completeErr error

	calls      []string // "start", "complete" or "fail", in order
	started    string
	completed  string
	failReason string
}

func (f *fakeStore) Start(_ context.Context, jobID, bucket, key string, attempt int) (bool, error) {
	f.calls = append(f.calls, "start")
	f.started = fmt.Sprintf("%s s3://%s/%s attempt %d", jobID, bucket, key, attempt)
	return f.done, f.startErr
}

func (f *fakeStore) Complete(_ context.Context, jobID, bucket, key string) error {
	f.calls = append(f.calls, "complete")
	f.completed = fmt.Sprintf("%s s3://%s/%s", jobID, bucket, key)
	return f.completeErr
}

func (f *fakeStore) Fail(_ context.Context, jobID, reason string) error {
	f.calls = append(f.calls, "fail")
	f.failReason = jobID + ": " + reason
	return nil
}

// writeAudio is a ProcessFunc that stands in for FFmpeg.
func writeAudio(_ context.Context, _, outDir string) (Output, error) {
	out := filepath.Join(outDir, "audio.mp3")
	if err := os.WriteFile(out, []byte("audio bytes"), 0o600); err != nil {
		return Output{}, err
	}
	return Output{Path: out, ContentType: "audio/mpeg"}, nil
}

// video returns an S3 fake that serves a small source video. Each call needs
// its own, because a reader is used up after one download.
func video() *fakeS3 {
	return &fakeS3{body: strings.NewReader("video bytes")}
}

func TestHandleDownloadsProcessesAndUploads(t *testing.T) {
	tmp := t.TempDir()

	client := video()
	store := &fakeStore{}
	var gotPath, gotContent string
	h := NewHandler(client, store, "processed", tmp, func(ctx context.Context, sourcePath, outDir string) (Output, error) {
		gotPath = sourcePath
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			return Output{}, err
		}
		gotContent = string(data)
		if len(client.puts) != 0 {
			t.Error("uploaded before processing finished")
		}
		return writeAudio(ctx, sourcePath, outDir)
	})

	err := h.Handle(context.Background(), message(eventBody("ObjectCreated:Put", "raw", "uploads/abc/source.mov")))
	if err != nil {
		t.Fatal(err)
	}

	if len(client.gets) != 1 || aws.ToString(client.gets[0].Bucket) != "raw" || aws.ToString(client.gets[0].Key) != "uploads/abc/source.mov" {
		t.Errorf("GetObject calls = %+v", client.gets)
	}
	if gotContent != "video bytes" {
		t.Errorf("process saw %q, want the downloaded bytes", gotContent)
	}
	if filepath.Base(gotPath) != "source.mov" {
		t.Errorf("local file = %q, want source.mov", filepath.Base(gotPath))
	}

	if len(client.puts) != 1 {
		t.Fatalf("PutObject called %d times, want 1", len(client.puts))
	}
	put := client.puts[0]
	if got := aws.ToString(put.Bucket); got != "processed" {
		t.Errorf("upload bucket = %q, want processed", got)
	}
	if got := aws.ToString(put.Key); got != "uploads/abc/audio.mp3" {
		t.Errorf("upload key = %q, want uploads/abc/audio.mp3", got)
	}
	if got := aws.ToString(put.ContentType); got != "audio/mpeg" {
		t.Errorf("upload content type = %q", got)
	}
	if client.putBodies[0] != "audio bytes" {
		t.Errorf("uploaded %q, want the processed file", client.putBodies[0])
	}

	if !slices.Equal(store.calls, []string{"start", "complete"}) {
		t.Errorf("store calls = %v, want start then complete", store.calls)
	}
	if want := "abc s3://raw/uploads/abc/source.mov attempt 2"; store.started != want {
		t.Errorf("started %q, want %q", store.started, want)
	}
	if want := "abc s3://processed/uploads/abc/audio.mp3"; store.completed != want {
		t.Errorf("completed %q, want %q", store.completed, want)
	}
	assertEmpty(t, tmp)
}

func TestHandleSkipsCompletedJob(t *testing.T) {
	client := video()
	store := &fakeStore{done: true}
	h := NewHandler(client, store, "processed", t.TempDir(), func(context.Context, string, string) (Output, error) {
		t.Error("process called for a job that already completed")
		return Output{}, nil
	})

	err := h.Handle(context.Background(), message(eventBody("ObjectCreated:Put", "raw", "uploads/abc/source.mp4")))
	if err != nil {
		t.Errorf("err = %v, want nil so the redelivered message is deleted", err)
	}
	if len(client.gets) != 0 || len(client.puts) != 0 {
		t.Errorf("S3 calls: %d gets, %d puts; want none", len(client.gets), len(client.puts))
	}
	if !slices.Equal(store.calls, []string{"start"}) {
		t.Errorf("store calls = %v, want only start", store.calls)
	}
}

func TestHandleFailures(t *testing.T) {
	processErr := errors.New("ffmpeg exited 1")
	getErr := errors.New("NoSuchKey")
	putErr := errors.New("AccessDenied")
	startErr := errors.New("dynamodb unavailable")
	completeErr := errors.New("dynamodb throttled")
	upload := eventBody("ObjectCreated:Put", "raw", "uploads/abc/source.mp4")
	failingUpload := video()
	failingUpload.putErr = putErr
	tests := []struct {
		name        string
		client      *fakeS3
		store       *fakeStore
		body        string
		processErr  error
		wantErr     error
		wantGet     bool
		wantProcess bool
		wantPut     bool
		wantCalls   []string
	}{
		{
			name:      "s3 download error",
			client:    &fakeS3{getErr: getErr},
			body:      upload,
			wantErr:   getErr,
			wantGet:   true,
			wantCalls: []string{"start", "fail"},
		},
		{
			name:      "download cut off",
			client:    &fakeS3{body: &failingReader{}},
			body:      upload,
			wantGet:   true,
			wantCalls: []string{"start", "fail"},
		},
		{
			name:        "process error",
			client:      video(),
			body:        upload,
			processErr:  processErr,
			wantErr:     processErr,
			wantGet:     true,
			wantProcess: true,
			wantCalls:   []string{"start", "fail"},
		},
		{
			name:        "s3 upload error",
			client:      failingUpload,
			body:        upload,
			wantErr:     putErr,
			wantGet:     true,
			wantProcess: true,
			wantPut:     true,
			wantCalls:   []string{"start", "fail"},
		},
		{
			name:      "job state cannot be written",
			client:    video(),
			store:     &fakeStore{startErr: startErr},
			body:      upload,
			wantErr:   startErr,
			wantCalls: []string{"start"},
		},
		{
			name:        "completion cannot be recorded",
			client:      video(),
			store:       &fakeStore{completeErr: completeErr},
			body:        upload,
			wantErr:     completeErr,
			wantGet:     true,
			wantProcess: true,
			wantPut:     true,
			wantCalls:   []string{"start", "complete"},
		},
		{
			name:   "source is in the output bucket",
			client: &fakeS3{},
			body:   eventBody("ObjectCreated:Put", "processed", "uploads/abc/audio.mp3"),
		},
		{
			name:   "key is not an upload",
			client: &fakeS3{},
			body:   eventBody("ObjectCreated:Put", "raw", "a.mp4"),
		},
		{
			name:   "unreadable message",
			client: &fakeS3{},
			body:   "not json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()

			store := tt.store
			if store == nil {
				store = &fakeStore{}
			}
			processed := false
			h := NewHandler(tt.client, store, "processed", tmp, func(ctx context.Context, sourcePath, outDir string) (Output, error) {
				processed = true
				if tt.processErr != nil {
					return Output{}, tt.processErr
				}
				return writeAudio(ctx, sourcePath, outDir)
			})

			err := h.Handle(context.Background(), message(tt.body))
			if err == nil {
				t.Fatal("Handle returned nil, want an error so the message is retried")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want it to wrap %v", err, tt.wantErr)
			}
			if got := len(tt.client.gets) > 0; got != tt.wantGet {
				t.Errorf("downloaded = %v, want %v", got, tt.wantGet)
			}
			if processed != tt.wantProcess {
				t.Errorf("process called = %v, want %v", processed, tt.wantProcess)
			}
			if got := len(tt.client.puts) > 0; got != tt.wantPut {
				t.Errorf("uploaded = %v, want %v", got, tt.wantPut)
			}
			if !slices.Equal(store.calls, tt.wantCalls) {
				t.Errorf("store calls = %v, want %v", store.calls, tt.wantCalls)
			}
			if slices.Contains(tt.wantCalls, "fail") && !strings.HasPrefix(store.failReason, "abc: ") {
				t.Errorf("failure recorded as %q, want it on job abc", store.failReason)
			}
			assertEmpty(t, tmp)
		})
	}
}

// TestHandleShutdownIsNotAFailure stops the job mid-way, as a SIGTERM would.
func TestHandleShutdownIsNotAFailure(t *testing.T) {
	store := &fakeStore{}
	ctx, cancel := context.WithCancel(context.Background())
	h := NewHandler(video(), store, "processed", t.TempDir(), func(ctx context.Context, _, _ string) (Output, error) {
		cancel()
		return Output{}, ctx.Err()
	})

	err := h.Handle(ctx, message(eventBody("ObjectCreated:Put", "raw", "uploads/abc/source.mp4")))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if !slices.Equal(store.calls, []string{"start"}) {
		t.Errorf("store calls = %v, want no failure recorded", store.calls)
	}
}

func TestHandleIgnoresMessageWithNoUpload(t *testing.T) {
	client := &fakeS3{}
	store := &fakeStore{}
	h := NewHandler(client, store, "processed", t.TempDir(), func(context.Context, string, string) (Output, error) {
		t.Error("process called for a message with no upload")
		return Output{}, nil
	})

	err := h.Handle(context.Background(), message(`{"Service":"Amazon S3","Event":"s3:TestEvent"}`))
	if err != nil {
		t.Errorf("err = %v, want nil so the message is deleted", err)
	}
	if len(client.gets) != 0 || len(client.puts) != 0 || len(store.calls) != 0 {
		t.Errorf("calls: %d gets, %d puts, store %v; want none", len(client.gets), len(client.puts), store.calls)
	}
}

func TestParseJobID(t *testing.T) {
	tests := []struct {
		key     string
		want    string
		wantErr bool
	}{
		{key: "uploads/abc/source.mp4", want: "abc"},
		{key: "a.mp4", wantErr: true},
		{key: "uploads/source.mp4", wantErr: true},
		{key: "uploads//source.mp4", wantErr: true},
		{key: "other/abc/source.mp4", wantErr: true},
		{key: "uploads/abc/nested/source.mp4", wantErr: true},
	}
	for _, tt := range tests {
		got, err := parseJobID(tt.key)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("parseJobID(%q) = %q, %v; want %q, error %v", tt.key, got, err, tt.want, tt.wantErr)
		}
	}
}

func assertEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("job directory was not cleaned up: %v", entries)
	}
}
