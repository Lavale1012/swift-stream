package media

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

// makeClip generates a two-second test video with FFmpeg's built-in sources.
func makeClip(t *testing.T, name string, withAudio bool) string {
	t.Helper()
	if err := CheckTools(); err != nil {
		t.Skipf("skipping: %v", err)
	}
	path := filepath.Join(t.TempDir(), name)
	args := []string{"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=25"}
	if withAudio {
		args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-c:a", "aac")
	}
	args = append(args, "-c:v", "mpeg4", "-y", path)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("generate test clip: %v: %s", err, out)
	}
	return path
}

func TestExtractAudio(t *testing.T) {
	for _, name := range []string{"source.mp4", "source.mov", "source.mkv"} {
		t.Run(name, func(t *testing.T) {
			source := makeClip(t, name, true)
			outDir := t.TempDir()

			got, err := ExtractAudio(context.Background(), source, outDir)
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(outDir, "audio.mp3"); got != want {
				t.Errorf("path = %q, want %q", got, want)
			}

			out, err := ffmpeg.Probe(got)
			if err != nil {
				t.Fatal(err)
			}
			var probe struct {
				Streams []struct {
					CodecType string `json:"codec_type"`
					CodecName string `json:"codec_name"`
				} `json:"streams"`
				Format struct {
					Duration string `json:"duration"`
				} `json:"format"`
			}
			if err := json.Unmarshal([]byte(out), &probe); err != nil {
				t.Fatal(err)
			}
			if len(probe.Streams) != 1 || probe.Streams[0].CodecType != "audio" || probe.Streams[0].CodecName != "mp3" {
				t.Errorf("streams = %+v, want one mp3 audio stream", probe.Streams)
			}
			if d, _ := strconv.ParseFloat(probe.Format.Duration, 64); d < 1.9 || d > 2.2 {
				t.Errorf("duration = %s, want about 2 seconds", probe.Format.Duration)
			}
		})
	}
}

func TestExtractAudioFailures(t *testing.T) {
	t.Run("video has no audio", func(t *testing.T) {
		source := makeClip(t, "silent.mp4", false)
		outDir := t.TempDir()

		_, err := ExtractAudio(context.Background(), source, outDir)
		if err == nil {
			t.Fatal("got nil, want an error for a source with no audio")
		}
		if !strings.Contains(err.Error(), "does not contain any stream") {
			t.Errorf("err = %v, want it to carry FFmpeg's explanation", err)
		}
	})

	t.Run("source is not a video", func(t *testing.T) {
		makeClip(t, "unused.mp4", false) // skips when FFmpeg is missing
		source := filepath.Join(t.TempDir(), "source.mp4")
		if err := os.WriteFile(source, []byte("not a video"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ExtractAudio(context.Background(), source, t.TempDir()); err == nil {
			t.Fatal("got nil, want an error")
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		source := makeClip(t, "source.mp4", true)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := ExtractAudio(ctx, source, t.TempDir())
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
}
