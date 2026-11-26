// Package capture reads pictures from cameras (through ffmpeg, an MJPEG
// stream or a snapshot address), records their video in pieces, and tells
// movement from a still picture.
package capture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// FFmpeg is the ffmpeg program to use ("" = none on this machine). FFMPEG
// in the environment wins over the PATH.
func FFmpeg() string {
	if p := os.Getenv("FFMPEG"); p != "" {
		return p
	}
	p, _ := exec.LookPath("ffmpeg")
	return p
}

// FFprobe is the ffprobe next to ffmpeg.
func FFprobe() string {
	if p := os.Getenv("FFPROBE"); p != "" {
		return p
	}
	p, _ := exec.LookPath("ffprobe")
	return p
}

// ErrNoFFmpeg means a camera needs ffmpeg and there is none.
var ErrNoFFmpeg = errors.New("ffmpeg is not installed on this server (the container image has it; for the bare binary install ffmpeg)")

// inputArgs are the options in front of -i for a camera address.
func inputArgs(url string) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin"}
	low := strings.ToLower(url)
	if strings.HasPrefix(low, "rtsp://") || strings.HasPrefix(low, "rtsps://") {
		args = append(args, "-rtsp_transport", "tcp", "-timeout", "10000000")
	} else if strings.HasPrefix(low, "http") {
		args = append(args, "-rw_timeout", "10000000")
	}
	return append(args, "-i", url)
}

// liveInputArgs are inputArgs for pictures shown live: ffmpeg keeps no
// buffer of its own and starts after a short look at the stream, so a
// picture leaves as soon as it is decoded instead of seconds later.
func liveInputArgs(url string) []string {
	args := inputArgs(url)
	in := args[:len(args)-2]
	low := []string{"-fflags", "nobuffer", "-flags", "low_delay", "-probesize", "500000", "-analyzeduration", "1000000"}
	return append(append(append([]string{}, in...), low...), "-i", url)
}

// relayedLowDelay are the options for reading a relay to show it live: no
// buffer of ffmpeg's own and a short look at the stream (it starts at a key
// frame with everything needed to decode it).
var relayedLowDelay = []string{"-fflags", "nobuffer", "-flags", "low_delay", "-probesize", "200000", "-analyzeduration", "500000"}

// StreamInfo is what ffprobe says about a stream.
type StreamInfo struct {
	Codec  string  `json:"codec"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	FPS    float64 `json:"fps"`
	Audio  string  `json:"audio"`
}

// Probe asks ffprobe what a camera address sends.
func Probe(ctx context.Context, url string) (*StreamInfo, error) {
	fp := FFprobe()
	if fp == "" {
		return nil, ErrNoFFmpeg
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := []string{}
	for _, a := range inputArgs(url) {
		if a != "-nostdin" { // ffprobe has no -nostdin
			args = append(args, a)
		}
	}
	args = append(args[:len(args)-2], "-show_streams", "-of", "json", "-i", url)
	cmd := exec.CommandContext(ctx, fp, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s", PlainError(errb.String(), err))
	}
	var res struct {
		Streams []struct {
			CodecType  string `json:"codec_type"`
			CodecName  string `json:"codec_name"`
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			AvgFrame   string `json:"avg_frame_rate"`
			RFrameRate string `json:"r_frame_rate"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		return nil, err
	}
	info := &StreamInfo{}
	for _, s := range res.Streams {
		switch s.CodecType {
		case "video":
			if info.Codec == "" {
				info.Codec, info.Width, info.Height = s.CodecName, s.Width, s.Height
				info.FPS = rate(s.AvgFrame)
				if info.FPS == 0 {
					info.FPS = rate(s.RFrameRate)
				}
			}
		case "audio":
			if info.Audio == "" {
				info.Audio = s.CodecName
			}
		}
	}
	if info.Codec == "" {
		return nil, errors.New("the camera answered but sent no video")
	}
	return info, nil
}

func rate(v string) float64 {
	a, b, ok := strings.Cut(v, "/")
	n, _ := strconv.ParseFloat(a, 64)
	if !ok {
		return n
	}
	d, _ := strconv.ParseFloat(b, 64)
	if d == 0 {
		return 0
	}
	return float64(int(n/d*10+0.5)) / 10
}

// PlainError turns ffmpeg's last words into something a person can act on.
func PlainError(stderr string, err error) string {
	low := strings.ToLower(stderr)
	switch {
	case strings.Contains(low, "401") || strings.Contains(low, "unauthorized"):
		return "the camera refused the user name or password"
	case strings.Contains(low, "option not found") || strings.Contains(low, "unrecognized option"):
		return "ffmpeg did not understand how it was asked (" + strings.TrimSpace(stderr) + ")"
	case strings.Contains(low, "404"):
		return "the camera has no stream at that address (check the path after the port)"
	case strings.Contains(low, "connection refused"):
		return "the camera refused the connection (wrong port, or RTSP is switched off in the camera's settings)"
	case strings.Contains(low, "timed out") || strings.Contains(low, "timeout") || strings.Contains(low, "no route to host"):
		return "the camera did not answer (is it on, and is the address right?)"
	case strings.Contains(low, "invalid data found"):
		return "the camera answered with something that is not video"
	case strings.Contains(low, "name or service not known") || strings.Contains(low, "failed to resolve"):
		return "that camera name is not known on the network"
	}
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		if len(last) > 200 {
			last = last[:200]
		}
		return last
	}
	if err != nil {
		return err.Error()
	}
	return "the camera stopped sending"
}

// tail keeps the last n bytes written (ffmpeg's stderr).
type tail struct {
	b []byte
	n int
}

func (t *tail) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > t.n {
		t.b = t.b[len(t.b)-t.n:]
	}
	return len(p), nil
}

func (t *tail) String() string { return string(t.b) }
