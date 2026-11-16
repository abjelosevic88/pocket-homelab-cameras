package server

import (
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-cameras/internal/store"
)

func localClock(iso string) string {
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return ""
	}
	return t.In(time.Local).Format("15:04")
}

// labelCounts counts events by what they were about.
func labelCounts(evs []store.Event) map[string]int {
	out := map[string]int{"events": 0, "person": 0, "vehicle": 0, "animal": 0, "parcel": 0, "doorbell": 0, "alerts": 0}
	for _, e := range evs {
		if e.Nothing {
			continue
		}
		out["events"]++
		if e.Kind == "doorbell" {
			out["doorbell"]++
		}
		if e.Alerted {
			out["alerts"]++
		}
		for _, l := range e.Labels {
			out[l]++
		}
	}
	return out
}

// handleHome is what the sidebar needs: the mode, each camera now, what is new.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	st := s.settingsOf(ctx, uid)
	cams, _ := s.store.ListCameras(ctx, uid)
	list := []map[string]any{}
	online := 0
	for _, c := range cams {
		cs := s.engine.state(c.ID)
		if cs.Online {
			online++
		}
		list = append(list, map[string]any{"id": c.ID, "name": c.Name, "enabled": c.Enabled, "online": cs.Online, "event": cs.Event, "mine": c.Mine})
	}
	from, _, _ := dayRange(today())
	evs, _ := s.store.EventRows(ctx, uid, from)
	writeJSON(w, http.StatusOK, map[string]any{"mode": st.Mode, "auto_mode": st.AutoMode, "cameras": list, "online": online, "total": len(cams),
		"unreviewed": s.store.UnreviewedCount(ctx, uid), "today": labelCounts(evs)})
}

// handleSummary is the cameras at a glance — for Homepage, Pocket AI and the Day.
func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	st := s.settingsOf(ctx, uid)
	cams, _ := s.store.ListCameras(ctx, uid)
	camsOut := []map[string]any{}
	online := 0
	for _, c := range cams {
		cs := s.engine.state(c.ID)
		if cs.Online {
			online++
		}
		m := map[string]any{"id": c.ID, "name": c.Name, "room": c.Room, "enabled": c.Enabled, "online": cs.Online, "recording": cs.Recording, "url": s.link(fmt.Sprintf("/cameras/%d", c.ID))}
		if ev, err := s.store.LastEvent(ctx, c.ID); err == nil {
			m["last_event"] = map[string]any{"at": ev.StartedAt, "title": eventTitle(ev, c.Name), "summary": ev.Summary}
		}
		camsOut = append(camsOut, m)
	}
	from, _, _ := dayRange(today())
	todayEvs, _ := s.store.EventRows(ctx, uid, from)
	recent, _, _ := s.store.ListEvents(ctx, uid, store.EventFilter{Limit: 5})
	recentOut := []map[string]any{}
	for i := range recent {
		e := &recent[i]
		recentOut = append(recentOut, map[string]any{"id": e.ID, "at": e.StartedAt, "camera": e.Camera, "title": eventTitle(e, e.Camera), "summary": e.Summary, "labels": e.Labels, "url": s.link(fmt.Sprintf("/events/%d", e.ID))})
	}
	_, total, _ := s.store.StorageUse(ctx)
	counts := labelCounts(todayEvs)
	text := fmt.Sprintf("%s mode. %d of %d cameras online. Today: %s.", modeWords[st.Mode], online, len(cams), todayWords(counts))
	writeJSON(w, http.StatusOK, map[string]any{"mode": st.Mode, "mode_reason": st.ModeReason, "auto_mode": st.AutoMode, "cameras": camsOut, "online": online, "total": len(cams),
		"today": counts, "unreviewed": s.store.UnreviewedCount(ctx, uid), "recent": recentOut, "storage_bytes": total, "text": text, "url": s.link("/")})
}

func todayWords(c map[string]int) string {
	if c["events"] == 0 {
		return "nothing seen"
	}
	parts := []string{}
	for _, k := range []struct{ key, one, many string }{{"person", "person", "people"}, {"vehicle", "vehicle", "vehicles"}, {"parcel", "parcel", "parcels"}, {"animal", "animal", "animals"}, {"doorbell", "ring at the door", "rings at the door"}} {
		if n := c[k.key]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, plural(n, k.one, k.many)))
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%d %s", c["events"], plural(c["events"], "event", "events"))
	}
	return strings.Join(parts, ", ")
}

// handleDay is one day — for the person's journal (Pocket Day).
func (s *Server) handleDay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	day := chiParam(r, "date")
	from, to, ok := dayRange(day)
	if !ok {
		writeErr(w, http.StatusBadRequest, "the date is YYYY-MM-DD")
		return
	}
	evs, _, err := s.store.ListEvents(ctx, uid, store.EventFilter{From: from, To: to, Limit: 300})
	if err != nil {
		s.fail(w, err, "day")
		return
	}
	items := []map[string]any{}
	for i := len(evs) - 1; i >= 0; i-- {
		e := &evs[i]
		status := "seen"
		if e.Alerted {
			status = "alerted"
		}
		kind := "event"
		if e.Kind != "motion" {
			kind = e.Kind
		}
		items = append(items, map[string]any{"kind": kind, "id": e.ID, "text": firstNonEmpty(e.Summary, eventTitle(e, e.Camera)),
			"title": eventTitle(e, e.Camera), "time": localClock(e.StartedAt), "camera": e.Camera, "labels": e.Labels, "status": status, "worry": e.Worry, "url": s.link(fmt.Sprintf("/events/%d", e.ID))})
	}
	modes, _ := s.store.ModeChanges(ctx, uid, from, to, 100)
	for _, m := range modes {
		text := modeWords[m.Mode]
		if m.Reason != "" {
			text += " — " + m.Reason
		}
		items = append(items, map[string]any{"kind": "mode", "id": m.ID, "text": text, "title": modeWords[m.Mode], "time": localClock(m.At), "status": m.Mode, "url": s.link("/")})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i]["time"].(string) < items[j]["time"].(string) })
	c := labelCounts(evs)
	writeJSON(w, http.StatusOK, map[string]any{"date": day, "items": items, "counts": c, "text": todayWords(c)})
}

// handleSearch finds cameras and events.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := queryInt(r, "limit")
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	hits := []map[string]any{}
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"hits": hits})
		return
	}
	low := strings.ToLower(q)
	cams, _ := s.store.ListCameras(ctx, uid)
	for _, c := range cams {
		if strings.Contains(strings.ToLower(c.Name+" "+c.Room+" "+c.Brand+" "+c.Notes), low) {
			hits = append(hits, map[string]any{"kind": "camera", "id": c.ID, "title": c.Name, "subtitle": joinNonEmpty(" · ", c.Room, c.Brand), "path": fmt.Sprintf("/cameras/%d", c.ID)})
		}
	}
	// "person", "parcels", "delivery"… search by what was seen
	f := store.EventFilter{Q: q, Limit: limit}
	for _, l := range Labels {
		if low == l || low == l+"s" || (l == "person" && low == "people") || (l == "parcel" && (low == "package" || low == "delivery" || low == "deliveries")) {
			f = store.EventFilter{Label: l, Limit: limit}
		}
	}
	evs, _, _ := s.store.ListEvents(ctx, uid, f)
	for i := range evs {
		e := &evs[i]
		t, _ := time.Parse(time.RFC3339Nano, e.StartedAt)
		hits = append(hits, map[string]any{"kind": "event", "id": e.ID, "title": firstNonEmpty(e.Summary, eventTitle(e, e.Camera)), "subtitle": e.Camera + " · " + t.Local().Format("Mon 2 Jan 15:04"),
			"date": e.StartedAt, "labels": e.Labels, "path": fmt.Sprintf("/events/%d", e.ID)})
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": hits})
}

// handleStats are the numbers: events a day by what they were, the busy
// hours, each camera, false alarms, storage.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	days := queryInt(r, "days")
	if days <= 0 || days > 365 {
		days = 30
	}
	start := time.Now().AddDate(0, 0, -days+1)
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.Local)
	evs, err := s.store.EventRows(ctx, uid, start.UTC().Format(time.RFC3339Nano))
	if err != nil {
		s.fail(w, err, "stats")
		return
	}
	type dayRow struct {
		Day      string `json:"day"`
		Person   int    `json:"person"`
		Vehicle  int    `json:"vehicle"`
		Animal   int    `json:"animal"`
		Parcel   int    `json:"parcel"`
		Other    int    `json:"other"`
		Nothing  int    `json:"nothing"`
		Alerts   int    `json:"alerts"`
		Doorbell int    `json:"doorbell"`
	}
	byDay := map[string]*dayRow{}
	order := []string{}
	for d := start; !d.After(time.Now()); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		byDay[k] = &dayRow{Day: k}
		order = append(order, k)
	}
	hours := make([][]int, 7) // Monday first
	for i := range hours {
		hours[i] = make([]int, 24)
	}
	perCam := map[int64]map[string]int{}
	totals := map[string]int{"events": 0, "nothing": 0, "alerts": 0, "person": 0, "vehicle": 0, "animal": 0, "parcel": 0}
	for _, e := range evs {
		t, err := time.Parse(time.RFC3339Nano, e.StartedAt)
		if err != nil {
			continue
		}
		t = t.Local()
		row := byDay[t.Format("2006-01-02")]
		if row == nil {
			continue
		}
		if perCam[e.CameraID] == nil {
			perCam[e.CameraID] = map[string]int{}
		}
		if e.Nothing {
			row.Nothing++
			totals["nothing"]++
			perCam[e.CameraID]["nothing"]++
			continue
		}
		totals["events"]++
		perCam[e.CameraID]["events"]++
		hours[(int(t.Weekday())+6)%7][t.Hour()]++
		if e.Alerted {
			row.Alerts++
			totals["alerts"]++
		}
		switch {
		case e.Kind == "doorbell":
			row.Doorbell++
		case slices.Contains(e.Labels, "parcel"):
			row.Parcel++
		case slices.Contains(e.Labels, "person"):
			row.Person++
		case slices.Contains(e.Labels, "vehicle"):
			row.Vehicle++
		case slices.Contains(e.Labels, "animal"):
			row.Animal++
		default:
			row.Other++
		}
		for _, l := range e.Labels {
			totals[l]++
		}
	}
	daysOut := []dayRow{}
	for _, k := range order {
		daysOut = append(daysOut, *byDay[k])
	}
	cams, _ := s.store.ListCameras(ctx, uid)
	use, total, _ := s.store.StorageUse(ctx)
	camsOut := []map[string]any{}
	for _, c := range cams {
		camsOut = append(camsOut, map[string]any{"id": c.ID, "name": c.Name, "events": perCam[c.ID]["events"], "nothing": perCam[c.ID]["nothing"], "bytes": use[c.ID]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": daysOut, "hours": hours, "cameras": camsOut, "totals": totals, "storage_bytes": total, "storage_cap": int64(s.cfg.MaxStorageGB * (1 << 30))})
}
