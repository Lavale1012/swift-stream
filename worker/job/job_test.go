package job

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
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
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("source %d = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// fakeS3 serves one object body and records what is uploaded.
type fakeS3 struct {
	body   io.Reader
	length *int64
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
	return &s3.GetObjectOutput{Body: io.NopCloser(f.body), ContentLength: f.length}, nil
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
	return types.Message{MessageId: aws.String("m1"), Body: aws.String(body)}
}

// writeAudio is a ProcessFunc that stands in for FFmpeg.
func writeAudio(_ context.Context, _, outDir string) (Output, error) {
	out := filepath.Join(outDir, "audio.mp3")
	if err := os.WriteFile(out, []byte("audio bytes"), 0o600); err != nil {
		return Output{}, err
	}
	return Output{Path: out, ContentType: "audio/mpeg"}, nil
}

func TestHandleDownloadsProcessesAndUploads(t *testing.T) {
	// Point os.MkdirTemp at a directory the test can inspect afterwards.
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	client := &fakeS3{body: strings.NewReader("video bytes"), length: aws.Int64(11)}
	var gotPath, gotContent string
	h := NewHandler(client, "processed", func(ctx context.Context, sourcePath, outDir string) (Output, error) {
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
	assertEmpty(t, tmp)
}

func TestHandleFailures(t *testing.T) {
	processErr := errors.New("ffmpeg exited 1")
	getErr := errors.New("NoSuchKey")
	putErr := errors.New("AccessDenied")
	tests := []struct {
		name        string
		client      *fakeS3
		body        string
		processErr  error
		wantErr     error
		wantGet     bool
		wantProcess bool
		wantPut     bool
	}{
		{
			name:    "s3 download error",
			client:  &fakeS3{getErr: getErr},
			body:    eventBody("ObjectCreated:Put", "raw", "a.mp4"),
			wantErr: getErr,
			wantGet: true,
		},
		{
			name:    "download cut off",
			client:  &fakeS3{body: &failingReader{}},
			body:    eventBody("ObjectCreated:Put", "raw", "a.mp4"),
			wantGet: true,
		},
		{
			name:    "fewer bytes than the object size",
			client:  &fakeS3{body: strings.NewReader("short"), length: aws.Int64(11)},
			body:    eventBody("ObjectCreated:Put", "raw", "a.mp4"),
			wantGet: true,
		},
		{
			name:        "process error",
			client:      &fakeS3{body: strings.NewReader("video bytes"), length: aws.Int64(11)},
			body:        eventBody("ObjectCreated:Put", "raw", "a.mp4"),
			processErr:  processErr,
			wantErr:     processErr,
			wantGet:     true,
			wantProcess: true,
		},
		{
			name:        "s3 upload error",
			client:      &fakeS3{body: strings.NewReader("video bytes"), length: aws.Int64(11), putErr: putErr},
			body:        eventBody("ObjectCreated:Put", "raw", "a.mp4"),
			wantErr:     putErr,
			wantGet:     true,
			wantProcess: true,
			wantPut:     true,
		},
		{
			name:   "source is in the output bucket",
			client: &fakeS3{body: strings.NewReader("video bytes"), length: aws.Int64(11)},
			body:   eventBody("ObjectCreated:Put", "processed", "uploads/abc/audio.mp3"),
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
			t.Setenv("TMPDIR", tmp)

			processed := false
			h := NewHandler(tt.client, "processed", func(ctx context.Context, sourcePath, outDir string) (Output, error) {
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
			assertEmpty(t, tmp)
		})
	}
}

func TestHandleIgnoresMessageWithNoUpload(t *testing.T) {
	client := &fakeS3{}
	h := NewHandler(client, "processed", func(context.Context, string, string) (Output, error) {
		t.Error("process called for a message with no upload")
		return Output{}, nil
	})

	err := h.Handle(context.Background(), message(`{"Service":"Amazon S3","Event":"s3:TestEvent"}`))
	if err != nil {
		t.Errorf("err = %v, want nil so the message is deleted", err)
	}
	if len(client.gets) != 0 || len(client.puts) != 0 {
		t.Errorf("S3 calls: %d gets, %d puts; want none", len(client.gets), len(client.puts))
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
