package server

import (
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/abjelosevic88/pocket-homelab-cameras/internal/capture"
	"github.com/abjelosevic88/pocket-homelab-cameras/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// What Pocket Search, Pocket Day and Pocket Hub ask Cameras (the capabilities
// value in links.go): one route each, in the shape every Pocket app answers
// in. They read what /search, /day and the sidebar read, as the request's
// person, from Cameras' own database and from what the running cameras last
// reported. No camera is asked for a picture, nothing is checked for movement,
// no AI is called and nothing is changed.

// upperFirst starts a line with a capital letter.
func upperFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 || unicode.IsUpper(r) {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// hasWord reports whether a search has a letter or a digit in it.
func hasWord(q string) bool {
	return strings.IndexFunc(q, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0
}

// eventLine is an event in one line: the AI's sentence, else what it was.
func eventLine(e *store.Event) string {
	return firstNonEmpty(strings.TrimSpace(e.Summary), eventWhat(e))
}

// ---------- search ----------

// pocketSearch answers Pocket Search with what /search finds: the cameras
// first (by name, room, make and notes), then the events, newest first. A
// camera someone shared with the person is found like their own, as in the
// app. An event is titled by what was seen; the camera and the AI's sentence
// are the line under it.
func (s *Server) pocketSearch(r *http.Request, q string, limit int) ([]core.SearchItem, error) {
	if !hasWord(q) {
		return nil, nil
	}
	ctx := r.Context()
	cams, evs, err := s.find(ctx, actorID(ctx), q, limit)
	if err != nil {
		return nil, err
	}
	lq := strings.ToLower(q)
	has := func(v ...string) bool { return strings.Contains(strings.ToLower(strings.Join(v, " ")), lq) }
	out := []core.SearchItem{}
	for _, c := range cams {
		brand := c.Brand
		if b, ok := capture.BrandByID(c.Brand); ok {
			brand = b.Name
		}
		out = append(out, core.SearchItem{ID: "camera:" + itoa(c.ID), Title: c.Name, Kind: "camera", Snippet: joinNonEmpty(" · ", c.Room, brand), URL: fmt.Sprintf("/cameras/%d", c.ID)})
	}
	for i := range evs {
		e := &evs[i]
		it := core.SearchItem{ID: "event:" + itoa(e.ID), Title: eventWhat(e), Kind: "event", Snippet: joinNonEmpty(" · ", e.Camera, e.Summary), Date: e.StartedAt,
			URL: fmt.Sprintf("/events/%d", e.ID)}
		// say where the words were found when the lines above do not show it
		if !has(it.Title, it.Snippet) {
			for _, d := range [][2]string{{"Note", e.Note}, {"Parcel", e.ParcelText}} {
				if has(d[1]) {
					it.Snippet = joinNonEmpty(" · ", it.Snippet, d[0]+": "+d[1])
					break
				}
			}
		}
		out = append(out, it)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---------- day ----------

// dayMaxItems is the most a day carries (the contract's own limit).
const dayMaxItems = 200

// dayKind is the group an event goes under in a journal.
func dayKind(e *store.Event) string {
	switch {
	case e.Kind == "doorbell":
		return "doorbell"
	case e.Kind == "trigger":
		return "alarm"
	}
	for _, l := range []string{"parcel", "person", "vehicle", "animal"} {
		if slices.Contains(e.Labels, l) {
			return l
		}
	}
	return "movement"
}

// pocketDay answers Pocket Day with what /day reads: what the cameras saw
// that day (false alarms left out) and the changes of mode, with the same
// line of counts. The day is the person's when the caller says their time
// zone, else the box's. A day too busy for a journal loses its plain movement
// that alerted nobody first, then its earliest hours, as /day keeps the
// newest. Events carry no picture: theirs opens only for someone signed in to
// Cameras, and goes when the event is tidied away.
func (s *Server) pocketDay(r *http.Request, date string, tz *time.Location) (*core.Day, error) {
	ctx := r.Context()
	uid := actorID(ctx)
	if tz == nil {
		tz = time.Local
	}
	from, to, ok := dayRangeIn(date, tz)
	if !ok {
		return nil, nil
	}
	evs, _, err := s.store.ListEvents(ctx, uid, store.EventFilter{From: from, To: to, Limit: 300})
	if err != nil {
		return nil, err
	}
	modes, err := s.store.ModeChanges(ctx, uid, from, to, 100)
	if err != nil {
		return nil, err
	}
	if len(evs) == 0 && len(modes) == 0 {
		return nil, nil
	}
	type row struct {
		at    time.Time
		quiet bool // plain movement nobody was told about
		item  core.DayItem
	}
	clock := func(iso string) (time.Time, string) {
		t, err := time.Parse(time.RFC3339Nano, iso)
		if err != nil {
			return time.Time{}, ""
		}
		return t, t.In(tz).Format("15:04")
	}
	rows := make([]row, 0, len(evs)+len(modes))
	for i := range evs {
		e := &evs[i]
		at, hm := clock(e.StartedAt)
		it := core.DayItem{ID: "event:" + itoa(e.ID), Time: hm, Title: eventLine(e), Sub: e.Camera, Kind: dayKind(e), URL: fmt.Sprintf("/events/%d", e.ID)}
		if e.Kind != "trigger" { // an alarm's note is its title already
			it.Note = strings.TrimSpace(e.Note)
		}
		rows = append(rows, row{at: at, quiet: it.Kind == "movement" && !e.Alerted, item: it})
	}
	for _, m := range modes {
		at, hm := clock(m.At)
		rows = append(rows, row{at: at, item: core.DayItem{ID: "mode:" + itoa(m.ID), Time: hm, Title: modeWords[m.Mode], Sub: m.Reason, Kind: "mode", URL: "/"}})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at.Before(rows[j].at) })
	if over := len(rows) - dayMaxItems; over > 0 {
		kept := rows[:0]
		for _, x := range rows {
			if x.quiet && over > 0 {
				over--
				continue
			}
			kept = append(kept, x)
		}
		rows = kept[over:]
	}
	c := labelCounts(evs)
	day := &core.Day{Date: date, Summary: upperFirst(todayWords(c))}
	for _, x := range rows {
		day.Items = append(day.Items, x.item)
	}
	if n := c["alerts"]; n > 0 {
		day.Metrics = append(day.Metrics, core.Metric{Label: "Alerts", Value: strconv.Itoa(n)})
	}
	return day, nil
}

// ---------- widget ----------

// widgetEvents is how many events the widget lists.
const widgetEvents = 5

// widgetWhen is when something happened, for a line of details: "14:32",
// "yesterday 22:10", "Mon 2 Jan 08:05".
func widgetWhen(iso string, now time.Time) string {
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return ""
	}
	t = t.In(now.Location())
	day := func(x time.Time) string { return x.Format("2006-01-02") }
	switch day(t) {
	case day(now):
		return t.Format("15:04")
	case day(now.AddDate(0, 0, -1)):
		return "yesterday " + t.Format("15:04")
	}
	return t.Format("Mon 2 Jan 15:04")
}

// pocketWidget answers Pocket Hub: the mode the house is in and the newest
// events the person has not looked at, with the cameras that are offline in
// front. "New" is what the sidebar counts: events of the person's own
// cameras, the ones they can mark as seen. Offline is what the running
// cameras last reported (a camera switched off is not offline); nothing is
// asked of a camera here. Someone without cameras gets nothing to show.
func (s *Server) pocketWidget(r *http.Request) (*core.WidgetData, error) {
	ctx := r.Context()
	uid := actorID(ctx)
	cams, err := s.store.ListCameras(ctx, uid)
	if err != nil {
		return nil, err
	}
	if len(cams) == 0 {
		return nil, nil
	}
	now := time.Now()
	w := &core.WidgetData{URL: "/"}
	var parts []string
	offline := 0
	for _, c := range cams {
		if !c.Enabled || !s.engine.offline(c.ID) {
			continue
		}
		offline++
		detail := "No pictures yet"
		if c.LastSeenAt != nil {
			if when := widgetWhen(*c.LastSeenAt, now); when != "" {
				detail = "No pictures since " + when
			}
		}
		w.Items = append(w.Items, core.WidgetItem{ID: "camera:" + itoa(c.ID), Title: c.Name + " is offline", Detail: detail, URL: fmt.Sprintf("/cameras/%d", c.ID)})
	}
	switch {
	case offline == 1:
		parts = append(parts, w.Items[0].Title)
	case offline > 1:
		parts = append(parts, fmt.Sprintf("%d cameras offline", offline))
	}
	evs, unseen, err := s.store.ListEvents(ctx, uid, store.EventFilter{Unreviewed: true, Own: true, Limit: widgetEvents})
	if err != nil {
		return nil, err
	}
	for i := range evs {
		e := &evs[i]
		w.Items = append(w.Items, core.WidgetItem{ID: "event:" + itoa(e.ID), Title: eventLine(e), Detail: joinNonEmpty(" · ", e.Camera, widgetWhen(e.StartedAt, now)),
			URL: fmt.Sprintf("/events/%d", e.ID)})
	}
	mode := modeWords[s.settingsOf(ctx, uid).Mode]
	parts = append(parts, mode)
	if unseen > 0 {
		parts = append(parts, fmt.Sprintf("%d new %s", unseen, plural(unseen, "event", "events")))
		w.URL = "/events?unreviewed=1"
	} else {
		parts = append(parts, "nothing new")
	}
	w.Line = joinNonEmpty(" · ", parts...)
	w.Metrics = []core.Metric{{Label: "Mode", Value: mode}, {Label: "New events", Value: strconv.Itoa(unseen)}}
	if offline > 0 {
		w.Metrics = append(w.Metrics, core.Metric{Label: "Offline", Value: strconv.Itoa(offline)})
	}
	return w, nil
}
