package capture

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// A relay reads one camera stream once (one RTSP session) and hands it, as
// MPEG-TS, to everything that wants it: the recorder, the movement check,
// every live viewer. Cameras allow only a few sessions (many answer "503"
// to the fourth or fifth), and each session costs the camera; a viewer that
// joins also starts at once, from the last key frame the relay kept, instead
// of waiting for the camera's next one.
//
// Going through MPEG-TS has a second use: ffmpeg reading it finds the
// codec's parameter sets (H.265 VPS/SPS/PPS, H.264 SPS/PPS) in the stream
// itself. Cameras that leave them out of their RTSP description otherwise
// get an MP4 header without them, which browsers cannot play.

const (
	tsSize        = 188
	relayLinger   = 15 * time.Second // keep the camera session this long after the last one leaves
	relayMaxGOP   = 24 << 20         // a key-frame gap longer than this is not kept
	relaySubQueue = 2048             // chunks a slow reader may fall behind before it is let go
)

var (
	relaysMu sync.Mutex
	relays   = map[string]*relay{}
)

// UseRelay is whether an address is read through a relay (RTSP cameras).
func UseRelay(url string) bool {
	low := strings.ToLower(url)
	return strings.HasPrefix(low, "rtsp://") || strings.HasPrefix(low, "rtsps://")
}

type relay struct {
	url    string
	cancel context.CancelFunc

	mu       sync.Mutex
	subs     map[*RelaySub]struct{}
	pat, pmt []byte // the newest program tables
	pmtPID   int
	videoPID int
	codec    string
	gop      []byte // TS packets since the newest video key frame (nil until one came)
	ready    chan struct{}
	closed   bool
	err      error
	idle     *time.Timer
}

// RelaySub is one reader of a relay: an io.Reader of MPEG-TS that starts at
// a key frame.
type RelaySub struct {
	r    *relay
	ch   chan []byte
	cur  []byte
	done chan struct{}
	once sync.Once
	mu   sync.Mutex
	err  error
}

// Subscribe joins (or starts) the relay for url.
func Subscribe(url string) *RelaySub {
	relaysMu.Lock()
	defer relaysMu.Unlock()
	r := relays[url]
	if r == nil {
		r = startRelay(url)
		relays[url] = r
	}
	s := &RelaySub{r: r, ch: make(chan []byte, relaySubQueue), done: make(chan struct{})}
	r.mu.Lock()
	if r.idle != nil {
		r.idle.Stop()
		r.idle = nil
	}
	if r.gop != nil && r.pat != nil && r.pmt != nil {
		head := make([]byte, 0, len(r.pat)+len(r.pmt)+len(r.gop))
		head = append(append(append(head, r.pat...), r.pmt...), r.gop...)
		s.ch <- head
	}
	r.subs[s] = struct{}{}
	r.mu.Unlock()
	return s
}

// Read implements io.Reader.
func (s *RelaySub) Read(p []byte) (int, error) {
	for len(s.cur) == 0 {
		select {
		case b, ok := <-s.ch:
			if !ok {
				return 0, s.Err()
			}
			s.cur = b
		case <-s.done:
			return 0, s.Err()
		}
	}
	n := copy(p, s.cur)
	s.cur = s.cur[n:]
	return n, nil
}

// Err is why the relay stopped giving this reader video (io.EOF while it runs).
func (s *RelaySub) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		return io.EOF
	}
	return s.err
}

// CameraErr is the camera's own trouble (wrong password, not answering),
// nil when the relay is fine or this reader just left.
func (s *RelaySub) CameraErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil || errors.Is(s.err, errLeft) || errors.Is(s.err, errTooSlow) || errors.Is(s.err, context.Canceled) {
		return nil
	}
	return s.err
}

// Codec waits for the relay to know the video codec ("h264", "hevc", or
// another name) or fails with the camera's trouble.
func (s *RelaySub) Codec(ctx context.Context) (string, error) {
	select {
	case <-s.r.ready:
	case <-s.done:
		return "", s.Err()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	return s.r.codec, nil
}

var (
	errLeft    = errors.New("left")
	errTooSlow = errors.New("this reader fell too far behind the camera")
)

// Close leaves the relay; the camera session ends a little after the last one leaves.
func (s *RelaySub) Close() { s.finish(errLeft) }

func (s *RelaySub) finish(err error) {
	s.once.Do(func() {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		close(s.done)
		r := s.r
		r.mu.Lock()
		delete(r.subs, s)
		if len(r.subs) == 0 && !r.closed && r.idle == nil {
			r.idle = time.AfterFunc(relayLinger, r.stopIfIdle)
		}
		r.mu.Unlock()
	})
}

func (r *relay) stopIfIdle() {
	relaysMu.Lock()
	r.mu.Lock()
	idle := len(r.subs) == 0
	if idle && relays[r.url] == r {
		delete(relays, r.url) // a reader arriving now starts a new relay, not this ending one
	}
	r.mu.Unlock()
	relaysMu.Unlock()
	if idle {
		r.cancel()
	}
}

func startRelay(url string) *relay {
	ctx, cancel := context.WithCancel(context.Background())
	r := &relay{url: url, cancel: cancel, subs: map[*RelaySub]struct{}{}, pmtPID: -1, videoPID: -1, ready: make(chan struct{})}
	go r.run(ctx)
	return r
}

func (r *relay) run(ctx context.Context) {
	err := r.read(ctx)
	if err == nil {
		err = errors.New("the camera stopped sending")
	}
	relaysMu.Lock()
	if relays[r.url] == r {
		delete(relays, r.url)
	}
	relaysMu.Unlock()
	r.mu.Lock()
	r.closed, r.err = true, err
	if r.idle != nil {
		r.idle.Stop()
	}
	subs := make([]*RelaySub, 0, len(r.subs))
	for s := range r.subs {
		subs = append(subs, s)
	}
	r.mu.Unlock()
	for _, s := range subs {
		s.finish(err)
	}
	r.cancel()
}

// relayArgs: the camera's video as it is; its sound as AAC (MPEG-TS cannot
// carry the G.711 most cameras send, and recordings want AAC anyway). Video
// leaves as it comes instead of waiting to be interleaved with the encoded
// sound (that wait held every picture back about 0.3 s).
func relayArgs(url string) []string {
	return append(inputArgs(url),
		"-map", "0:v:0", "-map", "0:a:0?", "-c:v", "copy", "-c:a", "aac", "-b:a", "64k",
		"-max_interleave_delta", "1", "-f", "mpegts", "-flush_packets", "1", "pipe:1")
}

func (r *relay) read(ctx context.Context) error {
	ff := FFmpeg()
	if ff == "" {
		return ErrNoFFmpeg
	}
	cmd := exec.CommandContext(ctx, ff, relayArgs(r.url)...)
	cmd.WaitDelay = 3 * time.Second
	errb := &tail{n: 2048}
	cmd.Stderr = errb
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	buf := make([]byte, 0, 512*tsSize)
	tmp := make([]byte, 256*tsSize)
	for {
		n, err := out.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			// find packet alignment (a sync byte every 188 bytes)
			for len(buf) >= tsSize && buf[0] != 0x47 {
				buf = buf[1:]
			}
			whole := len(buf) / tsSize * tsSize
			if whole > 0 {
				r.feed(buf[:whole])
				buf = append(buf[:0], buf[whole:]...)
			}
		}
		if err != nil {
			break
		}
	}
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return context.Canceled
	}
	return errors.New(PlainError(errb.String(), werr))
}

// feed takes whole TS packets: keeps the tables and the newest key frame's
// group of pictures, and sends the packets to every reader.
func (r *relay) feed(pkts []byte) {
	chunk := make([]byte, len(pkts))
	copy(chunk, pkts)
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := 0; i+tsSize <= len(chunk); i += tsSize {
		p := chunk[i : i+tsSize]
		if p[0] != 0x47 {
			continue
		}
		pid := int(p[1]&0x1f)<<8 | int(p[2])
		pusi := p[1]&0x40 != 0
		switch {
		case pid == 0:
			r.pat = append(r.pat[:0], p...)
			if pusi {
				if pmt := patPMT(p); pmt > 0 {
					r.pmtPID = pmt
				}
			}
			continue
		case pid == r.pmtPID:
			r.pmt = append(r.pmt[:0], p...)
			if pusi && r.videoPID < 0 {
				if vpid, codec := pmtVideo(p); vpid > 0 {
					r.videoPID, r.codec = vpid, codec
					close(r.ready)
				}
			}
			continue
		}
		if pid == r.videoPID && pusi && randomAccess(p) {
			r.gop = r.gop[:0]
			if r.gop == nil {
				r.gop = make([]byte, 0, 1<<20)
			}
		}
		if r.gop != nil {
			if len(r.gop)+tsSize > relayMaxGOP {
				r.gop = nil // too long a gap: readers wait for the next key frame instead
			} else {
				r.gop = append(r.gop, p...)
			}
		}
	}
	for s := range r.subs {
		select {
		case s.ch <- chunk:
		default:
			go s.finish(errTooSlow)
		}
	}
}

// payload is where a TS packet's payload starts (-1 = none).
func payload(p []byte) int {
	afc := (p[3] >> 4) & 3
	switch afc {
	case 1:
		return 4
	case 3:
		o := 5 + int(p[4])
		if o >= tsSize {
			return -1
		}
		return o
	}
	return -1
}

func randomAccess(p []byte) bool {
	return (p[3]>>4)&2 != 0 && p[4] > 0 && p[5]&0x40 != 0
}

// section is the PSI section in a packet that starts one.
func section(p []byte) []byte {
	o := payload(p)
	if o < 0 || o >= tsSize {
		return nil
	}
	o += 1 + int(p[o]) // pointer field
	if o+3 > tsSize {
		return nil
	}
	l := int(p[o+1]&0x0f)<<8 | int(p[o+2])
	end := o + 3 + l
	if end > tsSize {
		end = tsSize
	}
	return p[o:end]
}

// patPMT is the first program's PMT PID.
func patPMT(p []byte) int {
	s := section(p)
	if len(s) < 12 || s[0] != 0x00 {
		return -1
	}
	for i := 8; i+4 <= len(s)-4; i += 4 {
		prog := int(s[i])<<8 | int(s[i+1])
		if prog != 0 {
			return int(s[i+2]&0x1f)<<8 | int(s[i+3])
		}
	}
	return -1
}

// pmtVideo is the video stream's PID and codec.
func pmtVideo(p []byte) (int, string) {
	s := section(p)
	if len(s) < 16 || s[0] != 0x02 {
		return -1, ""
	}
	info := int(s[10]&0x0f)<<8 | int(s[11])
	for i := 12 + info; i+5 <= len(s)-4; {
		typ := s[i]
		pid := int(s[i+1]&0x1f)<<8 | int(s[i+2])
		esl := int(s[i+3]&0x0f)<<8 | int(s[i+4])
		switch typ {
		case 0x1b:
			return pid, "h264"
		case 0x24:
			return pid, "hevc"
		case 0x01, 0x02:
			return pid, "mpeg2video"
		case 0x10:
			return pid, "mpeg4"
		}
		i += 5 + esl
	}
	return -1, ""
}

// relayedCmd is ffmpeg reading a relay on its standard input.
type relayedCmd struct {
	*exec.Cmd
	url   string
	sub   *RelaySub
	stdin io.WriteCloser
	errb  *tail
}

// newRelayed is ffmpeg with the relay for url on its input: inArgs go in
// front of "-i pipe:0", outArgs after it.
func newRelayed(ctx context.Context, url string, inArgs, outArgs []string) (*relayedCmd, error) {
	ff := FFmpeg()
	if ff == "" {
		return nil, ErrNoFFmpeg
	}
	args := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, inArgs...)
	args = append(append(args, "-f", "mpegts", "-i", "pipe:0"), outArgs...)
	cmd := exec.CommandContext(ctx, ff, args...)
	cmd.WaitDelay = 3 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	c := &relayedCmd{Cmd: cmd, url: url, stdin: stdin, errb: &tail{n: 2048}}
	cmd.Stderr = c.errb
	return c, nil
}

// Start starts ffmpeg and joins the relay.
func (c *relayedCmd) Start() error {
	c.sub = Subscribe(c.url)
	if err := c.Cmd.Start(); err != nil {
		c.sub.Close()
		return err
	}
	go func() {
		_, _ = io.Copy(c.stdin, c.sub)
		_ = c.stdin.Close()
	}()
	return nil
}

// Wait waits for ffmpeg and leaves the relay; the error says why it stopped
// in words a person can act on (the camera's trouble first).
func (c *relayedCmd) Wait() error {
	err := c.Cmd.Wait()
	c.sub.Close()
	if cerr := c.sub.CameraErr(); cerr != nil {
		return cerr
	}
	return errors.New(PlainError(c.errb.String(), err))
}

// Run is Start and Wait.
func (c *relayedCmd) Run() error {
	if err := c.Start(); err != nil {
		return err
	}
	return c.Wait()
}
