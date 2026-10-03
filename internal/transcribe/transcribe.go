// Package transcribe turns speech in audio and video files into text with ffmpeg and whisper.cpp.
package transcribe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ErrNoSpeech is returned when Whisper recognizes no speech.
var ErrNoSpeech = errors.New("no speech recognized")

// Config holds transcriber settings.
type Config struct {
	WhisperBin string
	FFmpegBin  string
	Model      string
	Language   string
	Threads    int
	Timeout    time.Duration
}

// Transcriber converts speech to text, one file at a time.
type Transcriber struct {
	cfg Config
	sem chan struct{}
}

// New returns a Transcriber, or an error explaining why transcription is unavailable.
func New(c *Config) (*Transcriber, error) {
	cfg := *c
	if cfg.Model == "" {
		return nil, errors.New("no whisper model configured")
	}
	if _, err := os.Stat(cfg.Model); err != nil {
		return nil, fmt.Errorf("whisper model: %w", err)
	}
	for _, bin := range []string{cfg.WhisperBin, cfg.FFmpegBin} {
		if _, err := exec.LookPath(bin); err != nil {
			return nil, err
		}
	}
	if cfg.Language == "" {
		cfg.Language = "auto"
	}
	if cfg.Threads <= 0 {
		cfg.Threads = min(runtime.GOMAXPROCS(0), 8)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Minute
	}
	return &Transcriber{cfg: cfg, sem: make(chan struct{}, 1)}, nil
}

// Transcribe returns the speech in the audio or video file at path as text.
func (t *Transcriber) Transcribe(ctx context.Context, path string) (string, error) {
	select {
	case t.sem <- struct{}{}:
		defer func() { <-t.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}

	ctx, cancel := context.WithTimeout(ctx, t.cfg.Timeout)
	defer cancel()

	tmp, err := os.MkdirTemp("", "nclaw-whisper-")
	if err != nil {
		return "", err
	}
	defer removeAll(tmp)

	wav := filepath.Join(tmp, "audio.wav")
	if err := run(ctx, t.cfg.FFmpegBin, "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", path, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", wav); err != nil {
		return "", fmt.Errorf("ffmpeg: %w", err)
	}

	out := filepath.Join(tmp, "transcript")
	if err := run(ctx, t.cfg.WhisperBin, "-m", t.cfg.Model, "-f", wav, "-l", t.cfg.Language,
		"-t", fmt.Sprint(t.cfg.Threads), "-nt", "-np", "-otxt", "-of", out); err != nil {
		return "", fmt.Errorf("whisper: %w", err)
	}

	return readTranscript(out + ".txt")
}

func readTranscript(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := strings.Join(strings.Fields(string(data)), " ")
	if text == "" || text == "[BLANK_AUDIO]" {
		return "", ErrNoSpeech
	}
	return text, nil
}

func run(ctx context.Context, bin string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, lastLine(stderr.String()))
	}
	return nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

func removeAll(path string) {
	if err := os.RemoveAll(path); err != nil {
		log.Printf("transcribe: remove %s: %v", path, err)
	}
}
