package capture

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SegmentLayout is the file name of a recorded piece (UTC start time).
const SegmentLayout = "20060102-150405"

// Recorder copies a camera's video (no re-encoding) into one-minute MP4
// files named by their UTC start time. The files are fragmented MP4s, so a
// piece cut short by a crash still plays.
type Recorder struct {
	URL     string
	Dir     string
	Seconds int // length of a piece (default 60)
}

// Run records until ctx ends or the camera stops.
func (r Recorder) Run(ctx context.Context) error {
	ff := FFmpeg()
	if ff == "" {
		return ErrNoFFmpeg
	}
	if err := os.MkdirAll(r.Dir, 0o755); err != nil {
		return err
	}
	secs := r.Seconds
	if secs <= 0 {
		secs = 60
	}
	args := append(inputArgs(r.URL),
		"-map", "0:v:0", "-map", "0:a:0?", "-c:v", "copy", "-c:a", "aac", "-b:a", "64k",
		"-f", "segment", "-segment_time", itoa(secs), "-segment_atclocktime", "1", "-reset_timestamps", "1", "-strftime", "1",
		"-segment_format", "mp4", "-segment_format_options", "movflags=+frag_keyframe+empty_moov+default_base_moof",
		filepath.Join(r.Dir, "%Y%m%d-%H%M%S.mp4"))
	cmd := exec.CommandContext(ctx, ff, args...)
	cmd.Env = append(os.Environ(), "TZ=UTC")
	cmd.WaitDelay = 5 * time.Second
	// stop with SIGINT so ffmpeg finishes the piece it is writing
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	errb := &tail{n: 2048}
	cmd.Stderr = errb
	err := cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.New(PlainError(errb.String(), err))
}

// SegmentStart reads the start time from a piece's file name.
func SegmentStart(name string) (time.Time, bool) {
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	t, err := time.Parse(SegmentLayout, base)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// Clip cuts [from, from+length) out of the pieces (oldest first, each with
// its start) into one MP4 at dst, without re-encoding.
func Clip(ctx context.Context, pieces []string, firstStart, from time.Time, length time.Duration, dst string) error {
	ff := FFmpeg()
	if ff == "" {
		return ErrNoFFmpeg
	}
	if len(pieces) == 0 {
		return errors.New("nothing was recorded then")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	list, err := os.CreateTemp(filepath.Dir(dst), "concat-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(list.Name())
	for _, p := range pieces {
		abs, _ := filepath.Abs(p)
		_, _ = list.WriteString("file '" + strings.ReplaceAll(abs, "'", `'\''`) + "'\n")
	}
	list.Close()
	off := from.Sub(firstStart)
	if off < 0 {
		off = 0
	}
	tmp := dst + ".part.mp4"
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, ff, "-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-f", "concat", "-safe", "0", "-i", list.Name(),
		"-ss", secs(off), "-t", secs(length), "-c", "copy", "-movflags", "+faststart", tmp)
	errb := &tail{n: 2048}
	cmd.Stderr = errb
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmp)
		return errors.New(PlainError(errb.String(), err))
	}
	return os.Rename(tmp, dst)
}

// FramesToClip makes a short MP4 from pictures (cameras without video).
func FramesToClip(ctx context.Context, frames []string, fps float64, dst string) error {
	ff := FFmpeg()
	if ff == "" {
		return ErrNoFFmpeg
	}
	if len(frames) == 0 {
		return errors.New("no pictures")
	}
	list, err := os.CreateTemp(filepath.Dir(dst), "frames-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(list.Name())
	d := 1 / fps
	for _, f := range frames {
		abs, _ := filepath.Abs(f)
		_, _ = list.WriteString("file '" + strings.ReplaceAll(abs, "'", `'\''`) + "'\nduration " + strings.TrimRight(strings.TrimRight(ftoa(d), "0"), ".") + "\n")
	}
	list.Close()
	tmp := dst + ".part.mp4"
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, ff, "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-f", "concat", "-safe", "0", "-i", list.Name(),
		"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2,format=yuv420p", "-c:v", "libx264", "-preset", "veryfast", "-r", "10", "-movflags", "+faststart", tmp)
	errb := &tail{n: 2048}
	cmd.Stderr = errb
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmp)
		return errors.New(PlainError(errb.String(), err))
	}
	return os.Rename(tmp, dst)
}
