package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-cameras/internal/capture"
	"github.com/abjelosevic88/pocket-homelab-cameras/internal/store"
)

// ---- events ----

// dayRange is [local midnight, next midnight) of a YYYY-MM-DD, as UTC ISO.
func dayRange(day string) (string, string, bool) {
	return dayRangeIn(day, time.Local)
}

// dayRangeIn is dayRange for a day as it is in another time zone.
func dayRangeIn(day string, loc *time.Location) (string, string, bool) {
	t, err := time.ParseInLocation("2006-01-02", day, loc)
	if err != nil {
		return "", "", false
	}
	return t.UTC().Format(time.RFC3339Nano), t.AddDate(0, 0, 1).UTC().Format(time.RFC3339Nano), true
}

func (s *Server) eventFilter(r *http.Request) store.EventFilter {
	q := r.URL.Query()
	f := store.EventFilter{CameraID: int64(queryInt(r, "camera")), Label: q.Get("label"), Unreviewed: queryBool(r, "unreviewed"), Starred: queryBool(r, "starred"),
		Nothing: queryBool(r, "nothing"), Q: q.Get("q"), Limit: queryInt(r, "limit"), Offset: queryInt(r, "offset")}
	if d := q.Get("day"); d != "" {
		f.From, f.To, _ = dayRange(d)
	}
	if v := q.Get("from"); v != "" {
		if a, _, ok := dayRange(v); ok {
			f.From = a
		}
	}
	if v := q.Get("to"); v != "" {
		if _, b, ok := dayRange(v); ok {
			f.To = b
		}
	}
	if h := queryInt(r, "hours"); h > 0 {
		f.From = time.Now().Add(-time.Duration(h) * time.Hour).UTC().Format(time.RFC3339Nano)
	}
	return f
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, total, err := s.store.ListEvents(ctx, actorID(ctx), s.eventFilter(r))
	if err != nil {
		s.fail(w, err, "events")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": s.eventsOut(list), "total": total, "unreviewed": s.store.UnreviewedCount(ctx, actorID(ctx))})
}

func (s *Server) eventsOut(list []store.Event) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for i := range list {
		out = append(out, s.eventOut(&list[i]))
	}
	return out
}

func (s *Server) eventOut(e *store.Event) map[string]any {
	return map[string]any{"id": e.ID, "camera_id": e.CameraID, "camera": e.Camera, "kind": e.Kind, "started_at": e.StartedAt, "ended_at": e.EndedAt,
		"score": e.Score, "box": e.Box, "labels": e.Labels, "summary": e.Summary, "ai_state": e.AIState, "worry": e.Worry, "frames": e.Frames, "best": e.Best,
		"clip": e.Clip, "mode": e.Mode, "alerted": e.Alerted, "reviewed": e.Reviewed, "starred": e.Starred, "nothing": e.Nothing, "pet": e.Pet,
		"parcel_ref": e.ParcelRef, "parcel_text": e.ParcelText, "weather": e.Weather, "note": e.Note, "source": e.Source,
		"title": eventTitle(e, e.Camera), "url": s.link(fmt.Sprintf("/events/%d", e.ID))}
}

func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ev, err := s.store.GetEvent(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "event")
		return
	}
	cam, _ := s.store.GetCamera(ctx, actorID(ctx), ev.CameraID)
	out := s.eventOut(ev)
	out["mine"] = ev.UserID == actorID(ctx)
	// recordings around it (RTSP cameras)
	if cam != nil && cam.Kind == "rtsp" {
		from, to := clipWindow(ev)
		segs, _ := s.store.Segments(ctx, ev.CameraID, from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano))
		out["recorded"] = len(segs) > 0
	}
	if ev.ParcelRef != "" {
		if sib, err := s.sibFor(ctx, actorID(ctx), appParcels, scopeParcelsRead); err == nil {
			out["parcel_url"] = linkTo(sib, "/parcels/"+ev.ParcelRef)
		}
	}
	// the events just before and after on the same camera, for stepping through
	if prev, _, err := s.store.ListEvents(ctx, actorID(ctx), store.EventFilter{CameraID: ev.CameraID, To: ev.StartedAt, Limit: 1}); err == nil && len(prev) > 0 {
		out["prev_id"] = prev[0].ID
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleUpdateEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		Reviewed *bool     `json:"reviewed"`
		Starred  *bool     `json:"starred"`
		Nothing  *bool     `json:"nothing"`
		Note     *string   `json:"note"`
		Labels   *[]string `json:"labels"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Note != nil {
		n := cleanText(*in.Note, 2000)
		in.Note = &n
	}
	if in.Labels != nil {
		l := cleanLabels(*in.Labels, false)
		in.Labels = &l
	}
	id := idParam(r, "id")
	if err := s.store.SetEventFlags(ctx, uid, id, store.EventFlags{Reviewed: in.Reviewed, Starred: in.Starred, Nothing: in.Nothing, Note: in.Note, Labels: in.Labels}); err != nil {
		s.fail(w, err, "event")
		return
	}
	ev, err := s.store.GetEvent(ctx, uid, id)
	if err != nil {
		s.fail(w, err, "event")
		return
	}
	if in.Starred != nil && *in.Starred {
		// a starred event keeps its clip even when the recordings go
		go func() {
			bg := context.WithoutCancel(ctx)
			if cam, err := s.store.CameraByID(bg, ev.CameraID); err == nil && cam.Kind == "rtsp" {
				_, _ = s.eventClip(bg, ev, cam)
			}
		}()
	}
	writeJSON(w, http.StatusOK, s.eventOut(ev))
}

func (s *Server) handleReviewAll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		CameraID int64  `json:"camera_id"`
		Before   string `json:"before"`
	}
	_ = decode(r, &in)
	n, err := s.store.ReviewAll(ctx, actorID(ctx), in.CameraID, in.Before)
	if err != nil {
		s.fail(w, err, "review")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reviewed": n})
}

func (s *Server) handleDeleteEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ev, err := s.store.GetEvent(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil || ev.UserID != actorID(ctx) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.store.DeleteEvent(ctx, ev.UserID, ev.ID); err != nil {
		s.fail(w, err, "event")
		return
	}
	s.dropEventFiles(ev.UserID, ev.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) dropEventFiles(uid, id int64) {
	_ = os.RemoveAll(s.eventDir(uid, id))
}

// handleEventPicture serves one kept picture (n, default the clearest).
func (s *Server) handleEventPicture(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ev, err := s.store.GetEvent(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "event")
		return
	}
	n := queryInt(r, "n")
	if n <= 0 || n > ev.Frames {
		n = ev.Best
	}
	p := filepath.Join(s.eventDir(ev.UserID, ev.ID), fmt.Sprintf("f%02d.jpg", n))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if queryBool(r, "download") {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="event-%d-%d.jpg"`, ev.ID, n))
	}
	http.ServeFile(w, r, p)
}

// clipWindow is the stretch of video kept for an event.
func clipWindow(ev *store.Event) (time.Time, time.Time) {
	from, _ := time.Parse(time.RFC3339Nano, ev.StartedAt)
	to := from.Add(20 * time.Second)
	if ev.EndedAt != nil {
		if t, err := time.Parse(time.RFC3339Nano, *ev.EndedAt); err == nil {
			to = t
		}
	}
	if to.Sub(from) > 5*time.Minute {
		to = from.Add(5 * time.Minute)
	}
	return from.Add(-4 * time.Second), to.Add(4 * time.Second)
}

// eventClip is an event's video: kept with the event, or cut now from the recordings.
func (s *Server) eventClip(ctx context.Context, ev *store.Event, cam *store.Camera) (string, error) {
	dst := filepath.Join(s.eventDir(ev.UserID, ev.ID), "clip.mp4")
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	if cam.Kind != "rtsp" {
		if ev.Frames >= 2 && capture.FFmpeg() != "" {
			if err := s.framesClip(ctx, ev.UserID, ev.ID, ev.Frames); err != nil {
				return "", err
			}
			_ = s.store.SetEventClip(ctx, ev.ID, true)
			return dst, nil
		}
		return "", errors.New("this camera keeps pictures, not video")
	}
	from, to := clipWindow(ev)
	segs, err := s.store.Segments(ctx, ev.CameraID, from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return "", err
	}
	if len(segs) == 0 {
		return "", errors.New("nothing was recorded then")
	}
	paths := []string{}
	for _, g := range segs {
		paths = append(paths, filepath.Join(s.cfg.RecordingsDir, g.Path))
	}
	first, _ := time.Parse(time.RFC3339Nano, segs[0].StartedAt)
	if err := capture.Clip(ctx, paths, first, from, to.Sub(from), dst); err != nil {
		return "", err
	}
	_ = s.store.SetEventClip(ctx, ev.ID, true)
	return dst, nil
}

func (s *Server) handleEventClip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ev, err := s.store.GetEvent(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "event")
		return
	}
	cam, err := s.store.CameraByID(ctx, ev.CameraID)
	if err != nil {
		s.fail(w, err, "camera")
		return
	}
	p, err := s.eventClip(ctx, ev, cam)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if queryBool(r, "download") {
		t, _ := time.Parse(time.RFC3339Nano, ev.StartedAt)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s %s.mp4"`, fileSafe(cam.Name), t.Local().Format("2006-01-02 15.04.05")))
	}
	http.ServeFile(w, r, p)
}

// ---- the doorbell and other buttons (webhooks) ----

// handleRing is a doorbell press (from Home Assistant, a Shelly button, a
// Shortcut): an event with the picture now, and an alert in every mode.
func (s *Server) handleRing(w http.ResponseWriter, r *http.Request) {
	s.buttonEvent(w, r, "doorbell")
}

// handleTrigger is any outside alarm (a door sensor, a siren): an event
// with the picture now, alerting like movement would.
func (s *Server) handleTrigger(w http.ResponseWriter, r *http.Request) {
	s.buttonEvent(w, r, "trigger")
}

func (s *Server) buttonEvent(w http.ResponseWriter, r *http.Request, kind string) {
	ctx := r.Context()
	var in struct {
		Note  string `json:"note"`
		Label string `json:"label"`
	}
	_ = decode(r, &in)
	c, err := s.store.GetOwnCamera(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "camera")
		return
	}
	st := s.settingsOf(ctx, c.UserID)
	now := time.Now()
	ev := store.Event{UserID: c.UserID, CameraID: c.ID, Camera: c.Name, Kind: kind, StartedAt: now.UTC().Format(time.RFC3339Nano), Mode: st.Mode,
		AIState: "pending", Labels: []string{}, Note: cleanText(firstNonEmpty(in.Note, in.Label), 200), Source: firstNonEmpty(linkApp(ctx), "webhook")}
	if err := s.store.CreateEvent(ctx, &ev); err != nil {
		s.fail(w, err, "event")
		return
	}
	bg := context.WithoutCancel(ctx)
	pic, at := s.engine.latest(c.ID)
	if pic == nil || time.Since(at) > 10*time.Second {
		pic, _ = s.grabFrame(bg, *c)
	}
	if pic != nil {
		ev.Frames, ev.Best = 1, 1
		_ = s.writeEventFile(c.UserID, ev.ID, "f01.jpg", pic)
	}
	end := now.Add(8 * time.Second).UTC().Format(time.RFC3339Nano)
	ev.EndedAt = &end
	_ = s.store.FinishEvent(bg, &ev)
	// a few more pictures while the person is at the door
	go func() {
		for i := 2; i <= 6; i++ {
			time.Sleep(1500 * time.Millisecond)
			if b, _ := s.engine.latest(c.ID); b != nil {
				_ = s.writeEventFile(c.UserID, ev.ID, fmt.Sprintf("f%02d.jpg", i), b)
				ev.Frames = i
				_ = s.store.FinishEvent(bg, &ev)
			}
		}
	}()
	s.publish(ctx, c.UserID, "cameras."+map[string]string{"doorbell": "doorbell", "trigger": "trigger"}[kind], eventTitle(&ev, c.Name), map[string]any{"camera_id": c.ID, "camera": c.Name, "event_id": ev.ID})
	if pic != nil {
		go s.checkEvent(bg, ev.ID, pic)
	} else {
		go func() {
			info := s.eventContext(bg, c.UserID, st)
			ev.AIState = "off"
			ev.Alerted = s.maybeAlert(bg, &ev, c, st, info)
			_ = s.store.SaveEventCheck(bg, &ev)
		}()
	}
	writeJSON(w, http.StatusCreated, s.eventOut(&ev))
}

// ---- recordings ----

// handleTimeline is one day of one camera (or all of them): what was
// recorded, the events, and the changes of mode.
func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	day := firstNonEmpty(r.URL.Query().Get("day"), today())
	from, to, ok := dayRange(day)
	if !ok {
		writeErr(w, http.StatusBadRequest, "day is YYYY-MM-DD")
		return
	}
	cams, err := s.store.ListCameras(ctx, uid)
	if err != nil {
		s.fail(w, err, "cameras")
		return
	}
	only := int64(queryInt(r, "camera"))
	rowsOut := []map[string]any{}
	for _, c := range cams {
		if only > 0 && c.ID != only {
			continue
		}
		segs, _ := s.store.Segments(ctx, c.ID, from, to)
		spans := [][2]string{}
		pieces := []map[string]any{}
		for _, g := range segs {
			if n := len(spans); n > 0 {
				prev, _ := time.Parse(time.RFC3339Nano, spans[n-1][1])
				st, _ := time.Parse(time.RFC3339Nano, g.StartedAt)
				if st.Sub(prev) < 5*time.Second {
					spans[n-1][1] = g.EndedAt
					pieces = append(pieces, map[string]any{"id": g.ID, "start": g.StartedAt, "end": g.EndedAt})
					continue
				}
			}
			spans = append(spans, [2]string{g.StartedAt, g.EndedAt})
			pieces = append(pieces, map[string]any{"id": g.ID, "start": g.StartedAt, "end": g.EndedAt})
		}
		evs, _, _ := s.store.ListEvents(ctx, uid, store.EventFilter{CameraID: c.ID, From: from, To: to, Limit: 500})
		rowsOut = append(rowsOut, map[string]any{"camera": map[string]any{"id": c.ID, "name": c.Name, "kind": c.Kind, "record": c.Record}, "spans": spans, "pieces": pieces, "events": s.eventsOut(evs)})
	}
	modes, _ := s.store.ModeChanges(ctx, uid, from, to, 200)
	// the mode the day started in
	if prev, err := s.store.ModeChanges(ctx, uid, "0", from, 1000000); err == nil && len(prev) > 0 {
		first := prev[len(prev)-1]
		first.At = from
		modes = append([]store.ModeChange{first}, modes...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"day": day, "from": from, "to": to, "rows": rowsOut, "modes": modes})
}

func (s *Server) handleSegmentVideo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	g, err := s.store.GetSegment(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "recording")
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, filepath.Join(s.cfg.RecordingsDir, g.Path))
}

// handleExportVideo cuts any stretch (at most 30 minutes) out of a camera's recordings.
func (s *Server) handleExportVideo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.store.GetCamera(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "camera")
		return
	}
	from, err1 := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	to, err2 := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	if err1 != nil || err2 != nil || !to.After(from) || to.Sub(from) > 30*time.Minute {
		writeErr(w, http.StatusBadRequest, "from and to are times (RFC 3339), at most 30 minutes apart")
		return
	}
	segs, err := s.store.Segments(ctx, c.ID, from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano))
	if err != nil || len(segs) == 0 {
		writeErr(w, http.StatusNotFound, "nothing was recorded then")
		return
	}
	paths := []string{}
	for _, g := range segs {
		paths = append(paths, filepath.Join(s.cfg.RecordingsDir, g.Path))
	}
	first, _ := time.Parse(time.RFC3339Nano, segs[0].StartedAt)
	tmp := filepath.Join(s.cfg.DataDir, "tmp", fmt.Sprintf("export-%d-%s.mp4", c.ID, randomHex(6)))
	if err := capture.Clip(ctx, paths, first, from, to.Sub(from), tmp); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	defer os.Remove(tmp)
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s %s.mp4"`, fileSafe(c.Name), from.Local().Format("2006-01-02 15.04.05")))
	http.ServeFile(w, r, tmp)
}

// ---- keeping the disk in check ----

// indexRecordings notes new recorded pieces and forgets what is gone.
func (s *Server) indexRecordings(ctx context.Context) {
	cams, err := s.store.AllCameras(ctx)
	if err != nil {
		return
	}
	for _, c := range cams {
		dir := s.recDir(c.ID)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		known, _ := s.store.SegmentPaths(ctx, c.ID)
		type piece struct {
			name  string
			start time.Time
			info  os.FileInfo
		}
		var list []piece
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".mp4") {
				continue
			}
			st, ok := capture.SegmentStart(e.Name())
			if !ok {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			list = append(list, piece{e.Name(), st, info})
		}
		slices.SortFunc(list, func(a, b piece) int { return a.start.Compare(b.start) })
		for i, p := range list {
			rel := filepath.Join(fmt.Sprint(c.ID), p.name)
			// the newest piece is still being written: wait for the next one
			last := i == len(list)-1
			if last && time.Since(p.info.ModTime()) < 15*time.Second {
				continue
			}
			end := p.info.ModTime().UTC()
			if !last && list[i+1].start.Before(end) {
				end = list[i+1].start
			}
			if end.Sub(p.start) > 10*time.Minute || !end.After(p.start) {
				end = p.start.Add(time.Minute)
			}
			if known[rel] && !last && i < len(list)-2 {
				continue
			}
			_ = s.store.AddSegment(ctx, &store.Segment{UserID: c.UserID, CameraID: c.ID, StartedAt: p.start.Format(time.RFC3339Nano), EndedAt: end.Format(time.RFC3339Nano), Path: rel, Bytes: p.info.Size()})
		}
	}
}

// keepTidy removes recordings and events past their keep time, video
// with no event around it for cameras recording only events, and the oldest
// video when the recordings are over their cap.
func (s *Server) keepTidy(ctx context.Context) {
	cams, err := s.store.AllCameras(ctx)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	drop := func(g store.Segment) {
		_ = os.Remove(filepath.Join(s.cfg.RecordingsDir, g.Path))
		_ = s.store.DeleteSegment(ctx, g.ID)
	}
	for _, c := range cams {
		old, _ := s.store.OldSegments(ctx, c.ID, now.AddDate(0, 0, -c.RetainDays).Format(time.RFC3339Nano))
		for _, g := range old {
			drop(g)
		}
		if c.Record == "events" || c.Record == "off" {
			// pieces older than 10 minutes with no event around them go
			recent, _ := s.store.OldSegments(ctx, c.ID, now.Add(-10*time.Minute).Format(time.RFC3339Nano))
			for _, g := range recent {
				a, _ := time.Parse(time.RFC3339Nano, g.StartedAt)
				b, _ := time.Parse(time.RFC3339Nano, g.EndedAt)
				evs, _ := s.store.EventsBetween(ctx, c.ID, a.Add(-30*time.Second).Format(time.RFC3339Nano), b.Add(30*time.Second).Format(time.RFC3339Nano))
				if len(evs) == 0 {
					drop(g)
				}
			}
		}
	}
	if evs, err := s.store.ExpiredEvents(ctx, now); err == nil {
		for _, e := range evs {
			_ = s.store.DropEvent(ctx, e.ID)
			s.dropEventFiles(e.UserID, e.ID)
		}
	}
	if s.cfg.MaxStorageGB > 0 {
		capBytes := int64(s.cfg.MaxStorageGB * (1 << 30))
		for i := 0; i < 20; i++ {
			_, total, err := s.store.StorageUse(ctx)
			if err != nil || total <= capBytes {
				break
			}
			old, _ := s.store.OldestSegments(ctx, 60)
			if len(old) == 0 {
				break
			}
			for _, g := range old {
				drop(g)
			}
		}
	}
}

func (s *Server) dropRecordings(cameraID int64) {
	_ = os.RemoveAll(s.recDir(cameraID))
}
