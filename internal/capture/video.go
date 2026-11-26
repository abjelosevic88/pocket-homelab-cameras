package capture

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"
)

// LiveVideo copies a camera's video, unchanged, into fragmented MP4 that a
// browser plays as it arrives (Media Source Extensions): no decoding or
// re-encoding on the server, the camera's own picture rate, and a fragment
// for every frame so nothing waits for the next key frame.
type LiveVideo struct {
	URL   string
	Codec string // from Probe: "h264" or "hevc"
}

// Playable is whether a browser can play this codec as live video.
func Playable(codec string) bool { return codec == "h264" || codec == "hevc" }

// outArgs are ffmpeg's output options.
func (v LiveVideo) outArgs() []string {
	args := []string{"-an", "-c:v", "copy"}
	if v.Codec == "hevc" {
		args = append(args, "-tag:v", "hvc1") // Safari plays only hvc1
	}
	return append(args,
		"-f", "mp4", "-movflags", "empty_moov+default_base_moof+frag_every_frame",
		"-flush_packets", "1", "pipe:1")
}

// Args are ffmpeg's arguments when it reads the camera itself (not RTSP).
func (v LiveVideo) Args() []string {
	return append(liveInputArgs(v.URL), v.outArgs()...)
}

// Run writes the video to w until ctx ends, the camera stops, or w fails;
// flush is called after every write.
func (v LiveVideo) Run(ctx context.Context, w io.Writer, flush func()) (int64, error) {
	ff := FFmpeg()
	if ff == "" {
		return 0, ErrNoFFmpeg
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if UseRelay(v.URL) {
		// the camera's shared stream, from its newest key frame: the MP4 header
		// gets the parameter sets even when the camera's RTSP description has none
		cmd, err := newRelayed(ctx, v.URL, []string{"-fflags", "nobuffer", "-probesize", "200000", "-analyzeduration", "500000"}, v.outArgs())
		if err != nil {
			return 0, err
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			return 0, err
		}
		if err := cmd.Start(); err != nil {
			return 0, err
		}
		n, werr := copyFlush(out, w, flush, cancel)
		cerr := cmd.Wait()
		if ctx.Err() != nil || werr != nil {
			return n, nil
		}
		return n, cerr
	}
	cmd := exec.CommandContext(ctx, ff, v.Args()...)
	cmd.WaitDelay = 3 * time.Second
	errb := &tail{n: 2048}
	cmd.Stderr = errb
	out, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	var n int64
	buf := make([]byte, 64<<10)
	var werr error
	for {
		k, rerr := out.Read(buf)
		if k > 0 {
			if _, werr = w.Write(buf[:k]); werr != nil {
				cancel()
				break
			}
			n += int64(k)
			flush()
		}
		if rerr != nil {
			break
		}
	}
	cerr := cmd.Wait()
	if ctx.Err() != nil || werr != nil {
		return n, nil
	}
	return n, errors.New(PlainError(errb.String(), cerr))
}

// copyFlush copies ffmpeg's output to w, flushing after every write; on a
// failed write it stops ffmpeg.
func copyFlush(r io.Reader, w io.Writer, flush func(), cancel func()) (int64, error) {
	var n int64
	buf := make([]byte, 64<<10)
	for {
		k, rerr := r.Read(buf)
		if k > 0 {
			if _, err := w.Write(buf[:k]); err != nil {
				cancel()
				return n, err
			}
			n += int64(k)
			flush()
		}
		if rerr != nil {
			return n, nil
		}
	}
}

// codecs remembers for a while what each camera address sends, so a viewer
// seldom waits for ffprobe (and a camera switched to another codec is
// noticed within the hour).
var codecs sync.Map // url → known

type known struct {
	codec string
	at    time.Time
}

// CodecOf is the video codec at a camera address.
func CodecOf(ctx context.Context, url string) (string, error) {
	if k, ok := codecs.Load(url); ok && time.Since(k.(known).at) < time.Hour {
		return k.(known).codec, nil
	}
	if UseRelay(url) {
		// the relay knows from the stream itself, and keeps the camera session
		// for the viewer that asked (no extra session for ffprobe)
		sub := Subscribe(url)
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		codec, err := sub.Codec(cctx)
		cancel()
		sub.Close()
		if err == nil && codec != "" {
			codecs.Store(url, known{codec, time.Now()})
			return codec, nil
		}
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			return "", err
		}
	}
	info, err := Probe(ctx, url)
	if err != nil {
		return "", err
	}
	codecs.Store(url, known{info.Codec, time.Now()})
	return info.Codec, nil
}
