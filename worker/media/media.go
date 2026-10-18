// Package media runs FFmpeg on downloaded source videos.
package media

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

const (
	// AudioContentType is the MIME type of the file ExtractAudio writes.
	AudioContentType = "audio/mpeg"

	audioFile      = "audio.mp3"
	audioCodec     = "libmp3lame"
	audioBitrate   = "192k"
	extractTimeout = 10 * time.Minute
	probeTimeout   = 30 * time.Second
	// stderrTail caps how much FFmpeg output is kept for an error message.
	stderrTail = 2000
)

// CheckTools reports whether the ffmpeg and ffprobe binaries can be found.
func CheckTools() error {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%s not found on PATH: %w", tool, err)
		}
	}
	return nil
}

// ExtractAudio writes the audio of the video at sourcePath to an MP3 file in
// outDir and returns the path of that file. It fails if the source has no
// audio.
func ExtractAudio(ctx context.Context, sourcePath, outDir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()

	outPath := filepath.Join(outDir, audioFile)
	// ffmpeg -i <source> -b:a 192k -c:a libmp3lame -vn <out> -hide_banner -loglevel error -y
	//   -vn             leave the video out
	//   -c:a libmp3lame re-encode the audio as MP3, whatever codec the source used
	//   -b:a            audio bitrate
	args := ffmpeg.Input(sourcePath).
		Output(outPath, ffmpeg.KwArgs{
			"vn":  "",
			"c:a": audioCodec,
			"b:a": audioBitrate,
		}).
		GlobalArgs("-hide_banner", "-loglevel", "error", "-y").
		GetArgs()

	// ffmpeg-go only builds the arguments. Starting the process here ties it
	// to ctx; the library's own Run loses the context whenever a call such as
	// GlobalArgs adds a node to the chain.
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stderr := &lastBytes{n: stderrTail}
	cmd.Stderr = stderr

	slog.InfoContext(ctx, "running ffmpeg", "stage", "extract_audio", "args", strings.Join(args, " "))
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("ffmpeg: %w", ctx.Err())
		}
		return "", fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(string(stderr.buf)))
	}

	// Exit status 0 is not proof of a usable file, so check what was written.
	if err := verifyAudio(outPath); err != nil {
		return "", fmt.Errorf("verify %s: %w", audioFile, err)
	}
	return outPath, nil
}

// verifyAudio uses ffprobe to confirm the file holds exactly one MP3 audio
// stream and nothing else.
func verifyAudio(path string) error {
	out, err := ffmpeg.ProbeWithTimeout(path, probeTimeout, nil)
	if err != nil {
		return fmt.Errorf("ffprobe: %w", err)
	}
	var probe struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
	}
	if err := json.Unmarshal([]byte(out), &probe); err != nil {
		return fmt.Errorf("decode ffprobe output: %w", err)
	}
	if len(probe.Streams) != 1 {
		return fmt.Errorf("has %d streams, want 1", len(probe.Streams))
	}
	if s := probe.Streams[0]; s.CodecType != "audio" || s.CodecName != "mp3" {
		return fmt.Errorf("stream is %s/%s, want audio/mp3", s.CodecType, s.CodecName)
	}
	return nil
}

// lastBytes is a writer that keeps only the last n bytes written to it, so a
// damaged source that makes FFmpeg print an error per packet cannot fill memory.
type lastBytes struct {
	n   int
	buf []byte
}

func (w *lastBytes) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.n {
		w.buf = append(w.buf[:0], w.buf[len(w.buf)-w.n:]...)
	}
	return len(p), nil
}
