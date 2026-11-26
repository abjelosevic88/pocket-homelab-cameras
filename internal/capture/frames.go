package capture

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// Source sends pictures (JPEG) until ctx ends or the camera stops; Run
// returns why it stopped.
type Source interface {
	Run(ctx context.Context, emit func(jpeg []byte)) error
}

// FFmpegSource decodes any stream ffmpeg reads (RTSP, HLS, HTTP video) into
// pictures, a few a second and small enough to check for movement.
type FFmpegSource struct {
	URL   string
	FPS   float64 // pictures a second (default 3)
	Width int     // scaled to this width (default 640; 0 keeps the size)
}

// Run implements Source.
func (s FFmpegSource) Run(ctx context.Context, emit func([]byte)) error {
	ff := FFmpeg()
	if ff == "" {
		return ErrNoFFmpeg
	}
	fps := s.FPS
	if fps <= 0 {
		fps = 3
	}
	vf := fmt.Sprintf("fps=%g", fps)
	if s.Width > 0 {
		vf += fmt.Sprintf(",scale='min(%d,iw)':-2", s.Width) // never bigger than the camera sends
	}
	args := append(liveInputArgs(s.URL), "-an", "-vf", vf, "-q:v", "7", "-flush_packets", "1", "-f", "image2pipe", "-c:v", "mjpeg", "pipe:1")
	cmd := exec.CommandContext(ctx, ff, args...)
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
	n := 0
	rerr := SplitJPEG(out, func(b []byte) { n++; emit(b) })
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if rerr != nil && !errors.Is(rerr, io.EOF) {
		return rerr
	}
	return errors.New(PlainError(errb.String(), werr))
}

// SplitJPEG cuts a stream of JPEGs (as ffmpeg's image2pipe writes them) into pictures.
func SplitJPEG(r io.Reader, emit func([]byte)) error {
	br := bufio.NewReaderSize(r, 256<<10)
	var cur []byte
	in := false
	var prev byte
	for {
		b, err := br.ReadByte()
		if err != nil {
			return err
		}
		if !in {
			if prev == 0xFF && b == 0xD8 {
				in = true
				cur = append(cur[:0], 0xFF, 0xD8)
			}
			prev = b
			continue
		}
		cur = append(cur, b)
		if prev == 0xFF && b == 0xD9 {
			pic := make([]byte, len(cur))
			copy(pic, cur)
			emit(pic)
			in = false
			cur = cur[:0]
			prev = 0
			continue
		}
		if len(cur) > 16<<20 {
			in, cur = false, cur[:0]
		}
		prev = b
	}
}

// MJPEGSource reads a camera's own MJPEG stream (multipart/x-mixed-replace).
type MJPEGSource struct {
	URL, User, Pass string
	FPS             float64 // at most this many a second (0 = all)
}

// Run implements Source.
func (s MJPEGSource) Run(ctx context.Context, emit func([]byte)) error {
	res, err := Get(ctx, &http.Client{}, s.URL, s.User, s.Pass)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return httpStatusErr(res.StatusCode)
	}
	every := time.Duration(0)
	if s.FPS > 0 {
		every = time.Duration(float64(time.Second) / s.FPS)
	}
	var last time.Time
	send := func(b []byte) {
		if every > 0 && time.Since(last) < every {
			return
		}
		last = time.Now()
		emit(b)
	}
	_, params, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if bd := strings.TrimPrefix(params["boundary"], "--"); bd != "" {
		mr := multipart.NewReader(res.Body, bd)
		for {
			p, err := mr.NextPart()
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if errors.Is(err, io.EOF) {
					return errors.New("the camera ended the stream")
				}
				// some cameras write the boundary without the leading dashes: fall back to markers
				break
			}
			b, err := io.ReadAll(io.LimitReader(p, 16<<20))
			if err != nil {
				return err
			}
			if i := bytes.Index(b, []byte{0xFF, 0xD8}); i >= 0 {
				send(b[i:])
			}
		}
	}
	err = SplitJPEG(res.Body, send)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// SnapshotSource asks a snapshot address for a picture every Every.
type SnapshotSource struct {
	URL, User, Pass string
	Every           time.Duration
}

// Run implements Source.
func (s SnapshotSource) Run(ctx context.Context, emit func([]byte)) error {
	every := s.Every
	if every <= 0 {
		every = time.Second
	}
	cl := &http.Client{Timeout: 10 * time.Second}
	fails := 0
	for {
		b, err := Snapshot(ctx, cl, s.URL, s.User, s.Pass)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			fails++
			if fails >= 3 {
				return err
			}
		} else {
			fails = 0
			emit(b)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every):
		}
	}
}

// Snapshot fetches one picture.
func Snapshot(ctx context.Context, cl *http.Client, url, user, pass string) ([]byte, error) {
	res, err := Get(ctx, cl, url, user, pass)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, httpStatusErr(res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return nil, errors.New("the camera did not send a JPEG picture at that address")
	}
	return b, nil
}

func httpStatusErr(code int) error {
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden:
		return errors.New("the camera refused the user name or password")
	case http.StatusNotFound:
		return errors.New("the camera has nothing at that address")
	}
	return fmt.Errorf("the camera answered HTTP %d", code)
}

// Get fetches a camera address with basic auth, retrying with digest auth
// when the camera asks for it (Hikvision, Dahua and most others do).
func Get(ctx context.Context, cl *http.Client, url, user, pass string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.New("that is not a web address")
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	res, err := cl.Do(req)
	if err != nil {
		return nil, plainNetErr(err)
	}
	if res.StatusCode != http.StatusUnauthorized || user == "" {
		return res, nil
	}
	chal := res.Header.Get("WWW-Authenticate")
	res.Body.Close()
	if !strings.HasPrefix(strings.ToLower(chal), "digest ") {
		return nil, httpStatusErr(http.StatusUnauthorized)
	}
	req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req2.Header.Set("Authorization", digest(chal, user, pass, http.MethodGet, req2.URL.RequestURI()))
	res, err = cl.Do(req2)
	if err != nil {
		return nil, plainNetErr(err)
	}
	return res, nil
}

func plainNetErr(err error) error {
	low := strings.ToLower(err.Error())
	switch {
	case strings.Contains(low, "refused"):
		return errors.New("the camera refused the connection (wrong port?)")
	case strings.Contains(low, "timeout") || strings.Contains(low, "deadline"):
		return errors.New("the camera did not answer (is it on, and is the address right?)")
	case strings.Contains(low, "no such host"):
		return errors.New("that camera name is not known on the network")
	}
	return err
}

func digest(chal, user, pass, method, uri string) string {
	p := map[string]string{}
	for _, part := range splitParams(chal[len("digest "):]) {
		k, v, ok := strings.Cut(part, "=")
		if ok {
			p[strings.ToLower(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	h := func(s string) string { x := md5.Sum([]byte(s)); return hex.EncodeToString(x[:]) }
	ha1 := h(user + ":" + p["realm"] + ":" + pass)
	ha2 := h(method + ":" + uri)
	cb := make([]byte, 8)
	_, _ = rand.Read(cb)
	cnonce := hex.EncodeToString(cb)
	nc := "00000001"
	qop := ""
	for _, q := range strings.Split(p["qop"], ",") {
		if strings.TrimSpace(q) == "auth" {
			qop = "auth"
		}
	}
	var resp string
	if qop != "" {
		resp = h(ha1 + ":" + p["nonce"] + ":" + nc + ":" + cnonce + ":" + qop + ":" + ha2)
	} else {
		resp = h(ha1 + ":" + p["nonce"] + ":" + ha2)
	}
	out := fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`, user, p["realm"], p["nonce"], uri, resp)
	if p["opaque"] != "" {
		out += fmt.Sprintf(`, opaque="%s"`, p["opaque"])
	}
	if p["algorithm"] != "" {
		out += ", algorithm=" + p["algorithm"]
	}
	if qop != "" {
		out += fmt.Sprintf(`, qop=%s, nc=%s, cnonce="%s"`, qop, nc, cnonce)
	}
	return out
}

// splitParams splits `a="x, y", b=z` on the commas outside quotes.
func splitParams(s string) []string {
	var out []string
	var cur strings.Builder
	q := false
	for _, r := range s {
		switch {
		case r == '"':
			q = !q
			cur.WriteRune(r)
		case r == ',' && !q:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
