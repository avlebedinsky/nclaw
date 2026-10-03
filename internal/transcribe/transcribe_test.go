//go:build unix

package transcribe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fakeFFmpeg = `#!/bin/sh
in=""; prev=""
for a in "$@"; do
  if [ "$prev" = "-i" ]; then in="$a"; fi
  prev="$a"; last="$a"
done
[ -n "$FAIL_FFMPEG" ] && { echo "bad input" >&2; exit 1; }
cp "$in" "$last"
`

const fakeWhisper = `#!/bin/sh
echo "$@" > "$ARGS_FILE"
of=""; prev=""
for a in "$@"; do
  if [ "$prev" = "-of" ]; then of="$a"; fi
  prev="$a"
done
[ -n "$FAIL_WHISPER" ] && { echo "model broken" >&2; exit 1; }
[ -n "$SLEEP_WHISPER" ] && exec sleep 10
printf '%s' "$TRANSCRIPT" > "$of.txt"
`

type fixture struct {
	cfg   Config
	args  string
	audio string
}

func setup(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(content), mode))
		return p
	}
	f := fixture{
		cfg: Config{
			FFmpegBin:  write("ffmpeg", fakeFFmpeg, 0o755),
			WhisperBin: write("whisper-cli", fakeWhisper, 0o755),
			Model:      write("model.bin", "weights", 0o644),
			Language:   "ru",
			Threads:    2,
		},
		args:  filepath.Join(dir, "args"),
		audio: write("voice.ogg", "opus data", 0o644),
	}
	t.Setenv("ARGS_FILE", f.args)
	t.Setenv("TMPDIR", t.TempDir())
	return f
}

func TestTranscribe_Success(t *testing.T) {
	f := setup(t)
	t.Setenv("TRANSCRIPT", "  Напомни  мне\n выпить воды ")
	tr, err := New(&f.cfg)
	require.NoError(t, err)

	text, err := tr.Transcribe(context.Background(), f.audio)

	require.NoError(t, err)
	assert.Equal(t, "Напомни мне выпить воды", text)
	args, err := os.ReadFile(f.args)
	require.NoError(t, err)
	assert.Contains(t, string(args), "-l ru")
	assert.Contains(t, string(args), "-m "+f.cfg.Model)
	assert.Contains(t, string(args), "-t 2")
	leftovers, err := os.ReadDir(os.Getenv("TMPDIR"))
	require.NoError(t, err)
	assert.Empty(t, leftovers)
}

func TestTranscribe_BlankAudio(t *testing.T) {
	f := setup(t)
	t.Setenv("TRANSCRIPT", " [BLANK_AUDIO] ")
	tr, err := New(&f.cfg)
	require.NoError(t, err)

	_, err = tr.Transcribe(context.Background(), f.audio)

	assert.ErrorIs(t, err, ErrNoSpeech)
}

func TestTranscribe_ToolFailures(t *testing.T) {
	for env, want := range map[string]string{"FAIL_FFMPEG": "ffmpeg: exit status 1: bad input", "FAIL_WHISPER": "whisper: exit status 1: model broken"} {
		t.Run(env, func(t *testing.T) {
			f := setup(t)
			t.Setenv(env, "1")
			tr, err := New(&f.cfg)
			require.NoError(t, err)

			_, err = tr.Transcribe(context.Background(), f.audio)

			assert.EqualError(t, err, want)
		})
	}
}

func TestTranscribe_Timeout(t *testing.T) {
	f := setup(t)
	t.Setenv("SLEEP_WHISPER", "1")
	f.cfg.Timeout = 200 * time.Millisecond
	tr, err := New(&f.cfg)
	require.NoError(t, err)
	start := time.Now()

	_, err = tr.Transcribe(context.Background(), f.audio)

	require.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestTranscribe_WaitsForBusySlotUntilContextDone(t *testing.T) {
	f := setup(t)
	tr, err := New(&f.cfg)
	require.NoError(t, err)
	tr.sem <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = tr.Transcribe(ctx, f.audio)

	assert.ErrorIs(t, err, context.Canceled)
}

func TestNew_Unavailable(t *testing.T) {
	f := setup(t)
	cases := map[string]func(c *Config){
		"no model":       func(c *Config) { c.Model = "" },
		"missing model":  func(c *Config) { c.Model = filepath.Join(t.TempDir(), "nope.bin") },
		"missing ffmpeg": func(c *Config) { c.FFmpegBin = "definitely-not-ffmpeg" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := f.cfg
			mutate(&cfg)
			_, err := New(&cfg)
			assert.Error(t, err)
		})
	}
}

func TestNew_Defaults(t *testing.T) {
	f := setup(t)
	f.cfg.Language, f.cfg.Threads = "", 0
	tr, err := New(&f.cfg)
	require.NoError(t, err)
	assert.Equal(t, "auto", tr.cfg.Language)
	assert.Positive(t, tr.cfg.Threads)
	assert.Equal(t, 5*time.Minute, tr.cfg.Timeout)
	assert.True(t, strings.HasSuffix(tr.cfg.WhisperBin, "whisper-cli"))
}
