package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-cameras/internal/capture"
	"github.com/abjelosevic88/pocket-homelab-cameras/internal/store"
)

// ---- the cameras, running ----
//
// Every switched-on camera has a worker: it reads a few pictures a second
// from the camera's small stream (or its MJPEG stream, its snapshot address,
// or the demo drawing), keeps the newest for the live view and the grid,
// hands them to everyone watching live, and looks for movement. Movement
// becomes an event: a few pictures are kept, the AI is asked what it is,
// and an alert goes out when that camera alerts on it in the mode the
// person is in. A second goroutine records the full-quality RTSP stream in
// one-minute pieces (no re-encoding).

const (
	eventQuiet    = 8 * time.Second // an event ends after this long without movement
	eventMax      = 3 * time.Minute // and is cut after this long
	eventFrames   = 12              // pictures kept per event
	frameEvery    = time.Second     // one kept picture a second
	checkAfter    = 1500 * time.Millisecond
	offlineAfter  = 45 * time.Second // no picture for this long = offline
	seenEvery     = time.Minute      // last_seen_at written at most this often
	liveSubBuffer = 2
)

type engine struct {
	s       *Server
	mu      sync.Mutex
	workers map[int64]*worker
	root    context.Context
}

type worker struct {
	e    *engine
	cam  store.Camera
	hash string
	stop context.CancelFunc

	mu        sync.Mutex
	last      []byte
	lastAt    time.Time
	started   time.Time
	err       string
	online    bool
	toldOff   bool // the offline alert went out
	seenSaved time.Time
	subs      map[chan []byte]struct{}
	recording bool
	recErr    string

	det *capture.Detector
	ev  *liveEvent
}

// liveEvent is an event still going on.
type liveEvent struct {
	ev        store.Event
	start     time.Time
	lastMove  time.Time
	lastSaved time.Time
	bestScore float64
	bestJPEG  []byte
	checked   bool
}

func newEngine(s *Server) *engine { return &engine{s: s, workers: map[int64]*worker{}} }

// camHash changes when anything the worker uses changes.
func camHash(c store.Camera) string {
	b, _ := json.Marshal([]any{c.Kind, c.MainURL, c.SubURL, c.SnapshotURL, c.Username, c.Password, c.Enabled, c.Record, c.Detect, c.Sensitivity, c.Zones})
	return string(b)
}

// start runs the cameras until ctx ends.
func (e *engine) start(ctx context.Context) {
	e.mu.Lock()
	e.root = ctx
	e.mu.Unlock()
	e.sync(ctx)
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		n := 0
		for {
			select {
			case <-ctx.Done():
				e.stopAll()
				return
			case <-t.C:
				n++
				e.watch(ctx)
				if n%4 == 0 {
					e.sync(ctx)
				}
			}
		}
	}()
}

// sync starts, restarts and stops workers to match the cameras.
func (e *engine) sync(ctx context.Context) {
	if e == nil || e.root == nil {
		return
	}
	cams, err := e.s.store.AllCameras(ctx)
	if err != nil {
		e.s.log.Warn("cameras: list failed", "err", err)
		return
	}
	want := map[int64]store.Camera{}
	for _, c := range cams {
		if c.Enabled {
			want[c.ID] = c
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, w := range e.workers {
		c, ok := want[id]
		if !ok || camHash(c) != w.hash {
			w.stop()
			delete(e.workers, id)
		} else {
			w.mu.Lock()
			w.cam = c // names, labels, modes: no restart needed
			w.mu.Unlock()
		}
	}
	for id, c := range want {
		if _, ok := e.workers[id]; ok {
			continue
		}
		wctx, cancel := context.WithCancel(e.root)
		w := &worker{e: e, cam: c, hash: camHash(c), stop: cancel, subs: map[chan []byte]struct{}{}, started: time.Now()}
		e.workers[id] = w
		go w.run(wctx)
		if c.Kind == "rtsp" && c.Record != "off" {
			go w.record(wctx)
		}
	}
}

func (e *engine) stopAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, w := range e.workers {
		w.stop()
		delete(e.workers, id)
	}
}

func (e *engine) get(id int64) *worker {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.workers[id]
}

// reload applies a camera change at once.
func (e *engine) reload(ctx context.Context) {
	if e == nil || e.root == nil {
		return
	}
	go e.sync(context.WithoutCancel(ctx))
}

// camState is what the page shows about a running camera.
type camState struct {
	Online    bool    `json:"online"`
	Running   bool    `json:"running"`
	Error     string  `json:"error"`
	LastAt    *string `json:"last_at"`
	Recording bool    `json:"recording"`
	RecError  string  `json:"rec_error"`
	Event     bool    `json:"event"` // something is moving now
	Watchers  int     `json:"watchers"`
}

func (e *engine) state(id int64) camState {
	w := e.get(id)
	if w == nil {
		return camState{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	st := camState{Online: w.online, Running: true, Error: w.err, Recording: w.recording, RecError: w.recErr, Event: w.ev != nil, Watchers: len(w.subs)}
	if !w.lastAt.IsZero() {
		v := w.lastAt.UTC().Format(time.RFC3339)
		st.LastAt = &v
	}
	return st
}

// offline reports a running camera that has sent no picture for a while
// (offlineAfter, as watch counts it). A camera that only just started is not
// offline yet, and one that is not running at all is not known to be.
func (e *engine) offline(id int64) bool {
	w := e.get(id)
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	since := w.lastAt
	if since.IsZero() {
		since = w.started
	}
	return !w.online && time.Since(since) > offlineAfter
}

// latest is the newest picture of a camera (nil = none yet).
func (e *engine) latest(id int64) ([]byte, time.Time) {
	w := e.get(id)
	if w == nil {
		return nil, time.Time{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last, w.lastAt
}

// subscribe gets every new picture of a camera until cancel is called.
func (e *engine) subscribe(id int64) (<-chan []byte, func(), bool) {
	w := e.get(id)
	if w == nil {
		return nil, func() {}, false
	}
	ch := make(chan []byte, liveSubBuffer)
	w.mu.Lock()
	w.subs[ch] = struct{}{}
	w.mu.Unlock()
	return ch, func() {
		w.mu.Lock()
		delete(w.subs, ch)
		w.mu.Unlock()
	}, true
}

// source is how a camera's pictures are read.
func (s *Server) source(c store.Camera) capture.Source {
	fps := s.cfg.DetectFPS
	switch c.Kind {
	case "demo":
		return capture.DemoSource{Scene: strings.TrimPrefix(c.MainURL, "demo:"), FPS: fps}
	case "mjpeg":
		return capture.MJPEGSource{URL: c.MainURL, User: c.Username, Pass: c.Password, FPS: fps}
	case "snapshot":
		every := time.Duration(float64(time.Second) / fps)
		if every < 500*time.Millisecond {
			every = 500 * time.Millisecond
		}
		return capture.SnapshotSource{URL: firstNonEmpty(c.SnapshotURL, c.MainURL), User: c.Username, Pass: c.Password, Every: every}
	}
	return capture.FFmpegSource{URL: capture.WithLogin(firstNonEmpty(c.SubURL, c.MainURL), c.Username, c.Password), FPS: fps, Width: 640}
}

func (w *worker) run(ctx context.Context) {
	s := w.e.s
	wait := 3 * time.Second
	for ctx.Err() == nil {
		w.mu.Lock()
		cam := w.cam
		w.det = nil
		if cam.Detect {
			zones := make([]capture.Zone, 0, len(cam.Zones))
			for _, z := range cam.Zones {
				zones = append(zones, capture.Zone{Kind: z.Kind, Points: z.Points})
			}
			w.det = capture.NewDetector(zones, cam.Sensitivity)
		}
		w.mu.Unlock()
		began := time.Now()
		err := s.source(cam).Run(ctx, func(b []byte) { w.frame(ctx, b) })
		if ctx.Err() != nil {
			break
		}
		msg := "the camera stopped sending"
		if err != nil {
			msg = err.Error()
		}
		w.mu.Lock()
		w.err = msg
		w.mu.Unlock()
		_ = s.store.SetCameraSeen(context.WithoutCancel(ctx), cam.ID, time.Time{}, msg)
		s.log.Debug("camera: stopped", "camera", cam.ID, "err", msg)
		if time.Since(began) > time.Minute {
			wait = 3 * time.Second
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		if wait < time.Minute {
			wait *= 2
		}
	}
	w.endEvent(context.WithoutCancel(ctx), time.Now())
}

// frame takes one new picture.
func (w *worker) frame(ctx context.Context, b []byte) {
	s := w.e.s
	now := time.Now()
	w.mu.Lock()
	w.last, w.lastAt = b, now
	back := !w.online
	told := w.toldOff
	w.online, w.err, w.toldOff = true, "", false
	for ch := range w.subs {
		select {
		case ch <- b:
		default:
		}
	}
	saveSeen := now.Sub(w.seenSaved) > seenEvery
	if saveSeen {
		w.seenSaved = now
	}
	cam := w.cam
	det := w.det
	w.mu.Unlock()
	bg := context.WithoutCancel(ctx)
	if saveSeen {
		_ = s.store.SetCameraSeen(bg, cam.ID, now, "")
	}
	if back && told {
		s.cameraBack(bg, cam)
	}
	if det == nil {
		return
	}
	m, err := det.FeedJPEG(b)
	if err != nil {
		return
	}
	w.motion(ctx, now, b, m)
}

func (w *worker) motion(ctx context.Context, now time.Time, b []byte, m capture.Motion) {
	s := w.e.s
	bg := context.WithoutCancel(ctx)
	w.mu.Lock()
	ev := w.ev
	cam := w.cam
	w.mu.Unlock()
	if ev == nil {
		if !m.Moving {
			return
		}
		st := s.settingsOf(bg, cam.UserID)
		ev = &liveEvent{start: now, lastMove: now, ev: store.Event{UserID: cam.UserID, CameraID: cam.ID, Camera: cam.Name, Kind: "motion",
			StartedAt: now.Add(-time.Second).UTC().Format(time.RFC3339Nano), Mode: st.Mode, AIState: "pending", Labels: []string{}}}
		if err := s.store.CreateEvent(bg, &ev.ev); err != nil {
			s.log.Warn("camera: event not saved", "camera", cam.ID, "err", err)
			return
		}
		w.mu.Lock()
		w.ev = ev
		w.mu.Unlock()
		s.publish(bg, cam.UserID, "cameras.motion", fmt.Sprintf("Movement at %s", cam.Name), map[string]any{"camera_id": cam.ID, "camera": cam.Name, "event_id": ev.ev.ID})
	}
	if m.Moving || m.Score > 0 {
		if m.Moving {
			ev.lastMove = now
		}
		if m.Score > ev.ev.Score {
			ev.ev.Score = m.Score
			ev.ev.Box = fmt.Sprintf("%.3f,%.3f,%.3f,%.3f", m.Box[0], m.Box[1], m.Box[2], m.Box[3])
		}
	}
	if ev.ev.Frames < eventFrames && now.Sub(ev.lastSaved) >= frameEvery {
		ev.ev.Frames++
		ev.lastSaved = now
		_ = s.writeEventFile(cam.UserID, ev.ev.ID, fmt.Sprintf("f%02d.jpg", ev.ev.Frames), b)
		if m.Score >= ev.bestScore || ev.bestJPEG == nil {
			ev.bestScore, ev.ev.Best, ev.bestJPEG = m.Score, ev.ev.Frames, b
		}
		// the pictures show while the event is still going on
		_ = s.store.FinishEvent(bg, &ev.ev)
	}
	if !ev.checked && now.Sub(ev.start) >= checkAfter && ev.bestJPEG != nil {
		ev.checked = true
		go s.checkEvent(bg, ev.ev.ID, ev.bestJPEG)
	}
	if now.Sub(ev.lastMove) > eventQuiet || now.Sub(ev.start) > eventMax {
		w.endEvent(bg, now)
	}
}

// endEvent finishes the event going on, if any.
func (w *worker) endEvent(ctx context.Context, now time.Time) {
	s := w.e.s
	w.mu.Lock()
	ev := w.ev
	w.ev = nil
	cam := w.cam
	w.mu.Unlock()
	if ev == nil {
		return
	}
	end := ev.lastMove.Add(2 * time.Second)
	if end.After(now) {
		end = now
	}
	v := end.UTC().Format(time.RFC3339Nano)
	ev.ev.EndedAt = &v
	if ev.ev.Best == 0 {
		ev.ev.Best = 1
	}
	_ = s.store.FinishEvent(ctx, &ev.ev)
	if cam.Kind != "rtsp" && ev.ev.Frames >= 3 && capture.FFmpeg() != "" {
		id, n := ev.ev.ID, ev.ev.Frames
		go func() {
			if s.framesClip(ctx, cam.UserID, id, n) == nil {
				_ = s.store.SetEventClip(ctx, id, true)
			}
		}()
	}
	if !ev.checked && ev.bestJPEG != nil {
		go s.checkEvent(ctx, ev.ev.ID, ev.bestJPEG)
	}
}

// record keeps the camera's full-quality video, restarting when it stops.
func (w *worker) record(ctx context.Context) {
	s := w.e.s
	if capture.FFmpeg() == "" {
		w.mu.Lock()
		w.recErr = capture.ErrNoFFmpeg.Error()
		w.mu.Unlock()
		return
	}
	wait := 5 * time.Second
	for ctx.Err() == nil {
		w.mu.Lock()
		cam := w.cam
		w.recording, w.recErr = true, ""
		w.mu.Unlock()
		began := time.Now()
		err := capture.Recorder{URL: capture.WithLogin(cam.MainURL, cam.Username, cam.Password), Dir: s.recDir(cam.ID)}.Run(ctx)
		w.mu.Lock()
		w.recording = false
		if err != nil && ctx.Err() == nil {
			w.recErr = err.Error()
		}
		w.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if time.Since(began) > 2*time.Minute {
			wait = 5 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait < time.Minute {
			wait *= 2
		}
	}
}

// watch notices cameras that stopped sending.
func (e *engine) watch(ctx context.Context) {
	e.mu.Lock()
	list := make([]*worker, 0, len(e.workers))
	for _, w := range e.workers {
		list = append(list, w)
	}
	e.mu.Unlock()
	now := time.Now()
	for _, w := range list {
		w.mu.Lock()
		since := w.lastAt
		if since.IsZero() {
			since = w.started
		}
		gone := now.Sub(since) > offlineAfter && !w.toldOff
		if gone {
			w.online, w.toldOff = false, true
		}
		cam := w.cam
		errText := w.err
		w.mu.Unlock()
		if gone {
			e.s.cameraGone(ctx, cam, errText)
		}
		// an event left open by a camera that stopped mid-way
		w.mu.Lock()
		stale := w.ev != nil && now.Sub(w.ev.lastMove) > eventQuiet+5*time.Second
		w.mu.Unlock()
		if stale {
			w.endEvent(ctx, now)
		}
	}
}

// ---- files ----

func (s *Server) recDir(cameraID int64) string {
	return filepath.Join(s.cfg.RecordingsDir, fmt.Sprint(cameraID))
}

func (s *Server) eventDir(uid, id int64) string {
	return filepath.Join(s.mediaRoot(), fmt.Sprint(uid), "events", fmt.Sprint(id))
}

func (s *Server) writeEventFile(uid, id int64, name string, b []byte) error {
	dir := s.eventDir(uid, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), b, 0o644)
}

func (s *Server) framesClip(ctx context.Context, uid, id int64, n int) error {
	dir := s.eventDir(uid, id)
	frames := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		frames = append(frames, filepath.Join(dir, fmt.Sprintf("f%02d.jpg", i)))
	}
	return capture.FramesToClip(ctx, frames, 2, filepath.Join(dir, "clip.mp4"))
}
