package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-cameras/internal/config"
	"github.com/abjelosevic88/pocket-homelab-cameras/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// fakeCapCore is a Pocket Core that vouches for link tokens
// pcx_<alice|bob>_<search|day|widget|capture|read|write> from Pocket Hub
// and remembers the manifest Cameras registers with.
type fakeCapCore struct {
	*httptest.Server
	mu       sync.Mutex
	manifest map[string]any
}

func newCapApp(t *testing.T) (*app, *fakeCapCore) {
	t.Helper()
	fc := &fakeCapCore{}
	scopes := map[string]string{"search": core.ScopeSearch, "day": core.ScopeDay, "widget": core.ScopeWidget, "capture": core.ScopeCapture,
		"read": "cameras:read", "write": "cameras:write"}
	fc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/register":
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			fc.mu.Lock()
			fc.manifest = m
			fc.mu.Unlock()
			io.WriteString(w, `{}`)
		case "/api/v1/auth/introspect":
			var in struct{ Token string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			parts := strings.Split(in.Token, "_")
			if len(parts) != 3 || parts[0] != "pcx" || scopes[parts[2]] == "" {
				io.WriteString(w, `{"active":false}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "kind": "delegated", "audience": "cameras", "scope": scopes[parts[2]],
				"user": map[string]any{"id": 1, "sub": parts[1] + "@example.com", "email": parts[1] + "@example.com", "name": parts[1], "role": "member"},
				"app":  map[string]any{"id": 9, "slug": "hub", "name": "Pocket Hub"}})
		default:
			io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(fc.Close)
	return newApp(t, func(c *config.Config) { c.CoreURL, c.CoreRegistrationKey = fc.URL, "key" }), fc
}

// items are the rows of a capability answer.
func items(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	l, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("no items list in %v", m)
	}
	out := []map[string]any{}
	for _, v := range l {
		out = append(out, v.(map[string]any))
	}
	return out
}

// item finds the row with that title.
func item(t *testing.T, m map[string]any, title string) map[string]any {
	t.Helper()
	for _, it := range items(t, m) {
		if it["title"] == title {
			return it
		}
	}
	t.Fatalf("no %q in %v", title, m["items"])
	return nil
}

// camera adds a camera for a person and returns its id.
func (a *app) camera(tok string, body map[string]any) int64 {
	a.t.Helper()
	if body["kind"] == nil {
		body["kind"] = "demo"
	}
	return int64(num(a.ok(tok, "POST", "/api/v1/cameras", body), "id"))
}

// event puts an event on a camera straight into the store, as tweak makes it.
func (a *app) event(cameraID int64, owner string, at time.Time, tweak func(*store.Event)) int64 {
	a.t.Helper()
	e := &store.Event{UserID: a.ids[owner], CameraID: cameraID, Kind: "motion", StartedAt: at.UTC().Format(time.RFC3339Nano), AIState: "done", Mode: "away", Frames: 1, Best: 1}
	if tweak != nil {
		tweak(e)
	}
	if err := a.st.CreateEvent(context.Background(), e); err != nil {
		a.t.Fatal(err)
	}
	return e.ID
}

// share lets Bob watch one of Alice's cameras.
func (a *app) share(cameraID int64) {
	a.t.Helper()
	a.ok(a.alice, "PUT", fmt.Sprintf("/api/v1/cameras/%d/shares", cameraID), map[string]any{"shares": []map[string]any{{"user_id": a.ids["Bob"], "notify": true}}})
}

func TestPocketSearch(t *testing.T) {
	a := newApp(t, nil)
	door := a.camera(a.alice, map[string]any{"name": "Front door", "kind": "rtsp", "brand": "reolink", "host": "192.168.1.50", "room": "Hall"})
	a.camera(a.alice, map[string]any{"name": "Garden"})
	now := time.Now()
	person := a.event(door, "Alice", now.Add(-time.Minute), func(e *store.Event) { e.Labels, e.Summary = []string{"person"}, "Someone walks up to the door." })
	parcel := a.event(door, "Alice", now.Add(-time.Hour), func(e *store.Event) {
		e.Labels, e.Summary, e.ParcelText = []string{"parcel", "person"}, "A courier leaves a box on the step.", "Shoes from Zalando"
	})
	a.event(door, "Alice", now.Add(-2*time.Hour), func(e *store.Event) {
		e.Labels, e.Summary, e.Note = []string{"vehicle"}, "A car parks outside.", "the blue van again"
	})
	a.event(door, "Alice", now.Add(-3*time.Hour), func(e *store.Event) { e.Summary, e.Nothing = "Only leaves moving.", true })

	res := a.ok(a.alice, "GET", "/api/v1/pocket/search?q=door", nil)
	all := items(t, res)
	if len(all) != 4 || all[0]["id"] != fmt.Sprintf("camera:%d", door) || all[0]["title"] != "Front door" || all[0]["kind"] != "camera" || all[0]["snippet"] != "Hall · Reolink" ||
		all[0]["url"] != fmt.Sprintf("/cameras/%d", door) || all[0]["date"] != nil {
		t.Fatalf("the camera leads, then its events: %v", all)
	}
	ev := all[1]
	if ev["id"] != fmt.Sprintf("event:%d", person) || ev["title"] != "Person" || ev["kind"] != "event" || ev["snippet"] != "Front door · Someone walks up to the door." ||
		ev["url"] != fmt.Sprintf("/events/%d", person) || ev["date"] != now.Add(-time.Minute).UTC().Format(time.RFC3339Nano) {
		t.Fatalf("the newest event: %v", ev)
	}
	// by what was seen, in the words people use for it
	for _, q := range []string{"parcel", "parcels", "delivery"} {
		got := items(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q="+q, nil))
		if len(got) != 1 || got[0]["id"] != fmt.Sprintf("event:%d", parcel) || got[0]["title"] != "Parcel" || got[0]["snippet"] != "Front door · A courier leaves a box on the step." {
			t.Fatalf("%s: %v", q, got)
		}
	}
	// found by something the two lines do not show: the line says where
	if got := items(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q=blue+van", nil)); len(got) != 1 || got[0]["title"] != "Vehicle" ||
		got[0]["snippet"] != "Front door · A car parks outside. · Note: the blue van again" {
		t.Fatalf("by the note: %v", got)
	}
	if got := items(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q=zalando", nil)); len(got) != 1 || got[0]["snippet"] != "Front door · A courier leaves a box on the step. · Parcel: Shoes from Zalando" {
		t.Fatalf("by the parcel: %v", got)
	}
	if got := items(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q=door&limit=1", nil)); len(got) != 1 || got[0]["kind"] != "camera" {
		t.Fatalf("limit 1: %v", got)
	}
	// a false alarm is not found, as in the app
	if got := items(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q=leaves", nil)); len(got) != 1 || got[0]["title"] != "Parcel" {
		t.Fatalf("false alarms: %v", got)
	}
	// no words, or only signs: nothing, even where the app's own search would match
	for _, q := range []string{"", "%3F%3F", "*", "%25", "_", "."} {
		if got := items(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q="+q, nil)); len(got) != 0 {
			t.Fatalf("q=%q: %v", q, got)
		}
	}
	// another person's cameras and events never appear…
	for _, q := range []string{"door", "garden", "parcel", "van", "hall"} {
		if got := items(t, a.ok(a.bob, "GET", "/api/v1/pocket/search?q="+q, nil)); len(got) != 0 {
			t.Fatalf("bob finds alice's %q: %v", q, got)
		}
	}
	// …until the camera is shared with them: then exactly what the app's own search shows
	a.share(door)
	got := items(t, a.ok(a.bob, "GET", "/api/v1/pocket/search?q=door", nil))
	if own := a.list(a.bob, "/api/v1/search?q=door"); len(got) != 4 || len(own) != 4 || got[0]["title"] != "Front door" || got[1]["title"] != "Person" {
		t.Fatalf("a shared camera: %v (the app finds %d)", got, len(own))
	}
	if got := items(t, a.ok(a.bob, "GET", "/api/v1/pocket/search?q=garden", nil)); len(got) != 0 {
		t.Fatalf("bob finds a camera that was not shared: %v", got)
	}
	a.expect(401, "", "GET", "/api/v1/pocket/search?q=door", nil)
	// the old route still answers in its own shape
	if old := a.ok(a.alice, "GET", "/api/v1/search?q=door", nil)["hits"].([]any); len(old) != 4 || old[1].(map[string]any)["title"] != "Someone walks up to the door." ||
		old[0].(map[string]any)["subtitle"] != "Hall · reolink" {
		t.Fatalf("/search changed: %v", old)
	}
}

func TestPocketDay(t *testing.T) {
	a := newApp(t, nil)
	ctx := context.Background()
	door := a.camera(a.alice, map[string]any{"name": "Front door"})
	garden := a.camera(a.alice, map[string]any{"name": "Garden"})
	y := time.Now().AddDate(0, 0, -1)
	at := func(h, m int) time.Time { return time.Date(y.Year(), y.Month(), y.Day(), h, m, 0, 0, time.Local) }
	date := y.Format("2006-01-02")
	person := a.event(door, "Alice", at(8, 5), func(e *store.Event) {
		e.Labels, e.Summary, e.Alerted = []string{"person"}, "Someone walks up to the door.", true
	})
	a.event(door, "Alice", at(9, 30), func(e *store.Event) {
		e.Labels, e.Summary, e.Note = []string{"parcel", "person"}, "A courier leaves a box on the step.", "for the neighbours"
	})
	a.event(door, "Alice", at(12, 0), func(e *store.Event) { e.Kind = "doorbell" })
	a.event(garden, "Alice", at(13, 0), nil)
	a.event(garden, "Alice", at(14, 0), func(e *store.Event) { e.Labels, e.Pet = []string{"animal"}, "Luna" })
	a.event(garden, "Alice", at(15, 0), func(e *store.Event) { e.Kind, e.Note = "trigger", "Back door opened" })
	a.event(garden, "Alice", at(16, 0), func(e *store.Event) { e.Summary, e.Nothing = "Only leaves moving.", true })
	for _, m := range []struct {
		mode, reason string
		at           time.Time
	}{{"away", "holiday", at(7, 0)}, {"night", "", at(23, 0)}, {"home", "", y.AddDate(0, 0, -3)}} {
		if err := a.st.AddModeChangeAt(ctx, a.ids["Alice"], m.mode, m.reason, "you", m.at.UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}

	day := a.ok(a.alice, "GET", "/api/v1/pocket/day/"+date, nil)
	rows := items(t, day)
	if day["date"] != date || day["summary"] != "2 people, 1 parcel, 1 animal, 1 ring at the door" || len(rows) != 8 {
		t.Fatalf("day: %v", day)
	}
	if ms := day["metrics"].([]any); len(ms) != 1 || ms[0].(map[string]any)["label"] != "Alerts" || ms[0].(map[string]any)["value"] != "1" {
		t.Fatalf("metrics: %v", ms)
	}
	for i, want := range []string{"07:00", "08:05", "09:30", "12:00", "13:00", "14:00", "15:00", "23:00"} {
		if rows[i]["time"] != want || rows[i]["image"] != nil {
			t.Fatalf("row %d: %v, want it at %s without a picture", i, rows[i], want)
		}
	}
	if it := rows[0]; it["title"] != "Away" || it["sub"] != "holiday" || it["kind"] != "mode" || it["url"] != "/" || !strings.HasPrefix(it["id"].(string), "mode:") {
		t.Fatalf("mode: %v", it)
	}
	if it := item(t, day, "Someone walks up to the door."); it["id"] != fmt.Sprintf("event:%d", person) || it["sub"] != "Front door" || it["kind"] != "person" ||
		it["url"] != fmt.Sprintf("/events/%d", person) || it["note"] != nil || it["done"] != nil {
		t.Fatalf("person: %v", it)
	}
	if it := item(t, day, "A courier leaves a box on the step."); it["kind"] != "parcel" || it["note"] != "for the neighbours" {
		t.Fatalf("parcel: %v", it)
	}
	for title, want := range map[string][2]string{"Doorbell": {"doorbell", "Front door"}, "Movement": {"movement", "Garden"}, "Luna": {"animal", "Garden"},
		"Back door opened": {"alarm", "Garden"}, "Night": {"mode", ""}} {
		it := item(t, day, title)
		if sub, _ := it["sub"].(string); it["kind"] != want[0] || sub != want[1] || it["note"] != nil {
			t.Fatalf("%s: %v", title, it)
		}
	}
	// the person's own time zone: the same moment, on the day and at the hour it is there
	loc, err := time.LoadLocation("Pacific/Kiritimati")
	if err != nil {
		t.Fatal(err)
	}
	there := at(8, 5).In(loc)
	far := a.ok(a.alice, "GET", "/api/v1/pocket/day/"+there.Format("2006-01-02")+"?tz=Pacific/Kiritimati", nil)
	if it := item(t, far, "Someone walks up to the door."); it["time"] != there.Format("15:04") {
		t.Fatalf("in another time zone: %v", it)
	}
	// a day with only a change of mode, a day with nothing
	if d := a.ok(a.alice, "GET", "/api/v1/pocket/day/"+y.AddDate(0, 0, -3).Format("2006-01-02"), nil); d["summary"] != "Nothing seen" || len(items(t, d)) != 1 || d["metrics"] != nil {
		t.Fatalf("a day with only a mode: %v", d)
	}
	if d := a.ok(a.alice, "GET", "/api/v1/pocket/day/2019-03-04", nil); d["summary"] != nil || len(items(t, d)) != 0 {
		t.Fatalf("an empty day: %v", d)
	}
	a.expect(400, a.alice, "GET", "/api/v1/pocket/day/yesterday", nil)
	// somebody else's day; a shared camera brings its events, as in the app, never the owner's modes
	if d := a.ok(a.bob, "GET", "/api/v1/pocket/day/"+date, nil); d["summary"] != nil || len(items(t, d)) != 0 {
		t.Fatalf("bob sees alice's day: %v", d)
	}
	a.share(garden)
	shared := a.ok(a.bob, "GET", "/api/v1/pocket/day/"+date, nil)
	if got := items(t, shared); len(got) != 3 || shared["summary"] != "1 animal" || got[0]["title"] != "Movement" || got[2]["title"] != "Back door opened" {
		t.Fatalf("bob's day with a shared camera: %v", shared)
	}
	if old := a.ok(a.bob, "GET", "/api/v1/day/"+date, nil); len(old["items"].([]any)) != 3 {
		t.Fatalf("the app's own day for bob: %v", old)
	}
	// the old route still answers as before
	old := a.ok(a.alice, "GET", "/api/v1/day/"+date, nil)
	if l := old["items"].([]any); len(l) != 8 || old["text"] != "2 people, 1 parcel, 1 animal, 1 ring at the door" || l[1].(map[string]any)["text"] != "Someone walks up to the door." ||
		l[0].(map[string]any)["text"] != "Away — holiday" {
		t.Fatalf("/day changed: %v", old)
	}
}

// A day busier than a journal takes keeps what was seen: plain movement that
// alerted nobody goes first, then the earliest hours.
func TestPocketDayBusy(t *testing.T) {
	a := newApp(t, nil)
	street := a.camera(a.alice, map[string]any{"name": "Street"})
	y := time.Now().AddDate(0, 0, -1)
	day := time.Date(y.Year(), y.Month(), y.Day(), 0, 0, 0, 0, time.Local)
	a.event(street, "Alice", day.Add(30*time.Minute), func(e *store.Event) { e.Labels, e.Summary = []string{"person"}, "The paper is delivered." })
	a.event(street, "Alice", day.Add(45*time.Minute), func(e *store.Event) { e.Summary, e.Alerted = "A branch falls on the fence.", true })
	for i := range 250 {
		a.event(street, "Alice", day.Add(time.Hour+time.Duration(i)*time.Minute), func(e *store.Event) { e.Summary = fmt.Sprintf("Movement %d", i) })
	}
	d := a.ok(a.alice, "GET", "/api/v1/pocket/day/"+day.Format("2006-01-02"), nil)
	rows := items(t, d)
	if len(rows) != 200 || d["summary"] != "1 person" || rows[0]["title"] != "The paper is delivered." || rows[1]["title"] != "A branch falls on the fence." ||
		rows[2]["title"] != "Movement 52" || rows[199]["title"] != "Movement 249" {
		t.Fatalf("a busy day: %d rows, %v … %v", len(rows), rows[:3], rows[len(rows)-1])
	}
	// nothing but visitors: the earliest go, as /day keeps the newest
	b := y.AddDate(0, 0, -1)
	before := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, time.Local)
	for i := range 230 {
		a.event(street, "Alice", before.Add(time.Hour+time.Duration(i)*time.Minute), func(e *store.Event) { e.Labels, e.Summary = []string{"vehicle"}, fmt.Sprintf("Car %d", i) })
	}
	d = a.ok(a.alice, "GET", "/api/v1/pocket/day/"+before.Format("2006-01-02"), nil)
	if rows := items(t, d); len(rows) != 200 || d["summary"] != "230 vehicles" || rows[0]["title"] != "Car 30" || rows[199]["title"] != "Car 229" {
		t.Fatalf("a day of cars: %d rows, %v, %v … %v", len(rows), d["summary"], rows[0], rows[len(rows)-1])
	}
}

// running makes the engine count a camera as running, without a camera behind it.
func (a *app) running(cameraID int64, w *worker) {
	a.s.engine.mu.Lock()
	a.s.engine.workers[cameraID] = w
	a.s.engine.mu.Unlock()
}

func TestPocketWidget(t *testing.T) {
	a := newApp(t, nil)
	ctx := context.Background()
	// no cameras: nothing to show
	if w := a.ok(a.bob, "GET", "/api/v1/pocket/widget", nil); w["line"] != nil || w["metrics"] != nil || w["url"] != nil || len(items(t, w)) != 0 {
		t.Fatalf("without cameras: %v", w)
	}
	door := a.camera(a.alice, map[string]any{"name": "Front door"})
	garden := a.camera(a.alice, map[string]any{"name": "Garden"})
	if w := a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil); w["line"] != "Home · nothing new" || w["url"] != "/" || len(items(t, w)) != 0 {
		t.Fatalf("a quiet house: %v", w)
	}
	now := time.Now()
	newest := now.Add(-time.Second)
	person := a.event(door, "Alice", newest, func(e *store.Event) { e.Labels, e.Summary = []string{"person"}, "Someone walks up to the door." })
	a.event(garden, "Alice", now.AddDate(0, 0, -3), nil)
	seen := a.event(garden, "Alice", now.Add(-time.Hour), func(e *store.Event) { e.Labels = []string{"animal"} })
	a.ok(a.alice, "PUT", fmt.Sprintf("/api/v1/events/%d", seen), map[string]any{"reviewed": true})
	a.event(garden, "Alice", now.Add(-time.Minute), func(e *store.Event) { e.Summary, e.Nothing = "Only leaves moving.", true })
	a.ok(a.alice, "PUT", "/api/v1/mode", map[string]any{"mode": "away"})

	w := a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil)
	if w["line"] != "Away · 2 new events" || w["url"] != "/events?unreviewed=1" {
		t.Fatalf("widget: %v", w)
	}
	ms := w["metrics"].([]any)
	for i, want := range [][2]string{{"Mode", "Away"}, {"New events", "2"}} {
		if m := ms[i].(map[string]any); len(ms) != 2 || m["label"] != want[0] || m["value"] != want[1] {
			t.Fatalf("metrics: %v", ms)
		}
	}
	rows := items(t, w)
	if len(rows) != 2 || rows[0]["id"] != fmt.Sprintf("event:%d", person) || rows[0]["title"] != "Someone walks up to the door." ||
		rows[0]["detail"] != "Front door · "+widgetWhen(newest.UTC().Format(time.RFC3339Nano), time.Now()) || rows[0]["url"] != fmt.Sprintf("/events/%d", person) ||
		rows[1]["title"] != "Movement" || !strings.HasPrefix(rows[1]["detail"].(string), "Garden · ") {
		t.Fatalf("the newest unseen events: %v", rows)
	}
	// the sidebar counts the same
	if home := a.ok(a.alice, "GET", "/api/v1/home", nil); num(home, "unreviewed") != 2 {
		t.Fatalf("the sidebar counts %v", home["unreviewed"])
	}
	a.ok(a.alice, "PUT", fmt.Sprintf("/api/v1/events/%d", person), map[string]any{"reviewed": true})
	if w := a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil); w["line"] != "Away · 1 new event" || len(items(t, w)) != 1 {
		t.Fatalf("one left: %v", w)
	}
	a.ok(a.alice, "POST", "/api/v1/events/review", map[string]any{})
	if w := a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil); w["line"] != "Away · nothing new" || w["url"] != "/" || len(items(t, w)) != 0 {
		t.Fatalf("all seen: %v", w)
	}
	// more than fit: the newest few, and the whole count
	for i := range 7 {
		a.event(door, "Alice", now.Add(-time.Duration(i+1)*time.Minute), func(e *store.Event) { e.Summary = fmt.Sprintf("Visit %d", i) })
	}
	if w := a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil); w["line"] != "Away · 7 new events" || len(items(t, w)) != 5 || items(t, w)[0]["title"] != "Visit 0" {
		t.Fatalf("many unseen: %v", w)
	}

	// a camera that stopped sending comes first; one that only just started, or is switched off, is not offline
	shed := a.camera(a.alice, map[string]any{"name": "Shed", "enabled": false})
	a.running(door, &worker{online: true, started: now.Add(-time.Hour), lastAt: now})
	a.running(garden, &worker{started: now})
	a.running(shed, &worker{started: now.Add(-time.Hour)})
	if w := a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil); w["line"] != "Away · 7 new events" || len(w["metrics"].([]any)) != 2 {
		t.Fatalf("nothing offline: %v", w)
	}
	a.running(garden, &worker{started: now.Add(-2 * time.Minute)})
	w = a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil)
	rows = items(t, w)
	if w["line"] != "Garden is offline · Away · 7 new events" || len(rows) != 6 || rows[0]["id"] != fmt.Sprintf("camera:%d", garden) || rows[0]["title"] != "Garden is offline" ||
		rows[0]["detail"] != "No pictures yet" || rows[0]["url"] != fmt.Sprintf("/cameras/%d", garden) || rows[1]["title"] != "Visit 0" {
		t.Fatalf("one camera offline: %v", w)
	}
	if m := w["metrics"].([]any)[2].(map[string]any); m["label"] != "Offline" || m["value"] != "1" {
		t.Fatalf("metrics: %v", w["metrics"])
	}
	last := now.Add(-10 * time.Minute)
	if err := a.st.SetCameraSeen(ctx, garden, last, ""); err != nil {
		t.Fatal(err)
	}
	a.running(door, &worker{started: now.Add(-time.Hour), lastAt: now.Add(-5 * time.Minute)})
	w = a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil)
	rows = items(t, w)
	if w["line"] != "2 cameras offline · Away · 7 new events" || len(rows) != 7 || rows[0]["title"] != "Front door is offline" || rows[1]["title"] != "Garden is offline" ||
		rows[1]["detail"] != "No pictures since "+widgetWhen(last.UTC().Format(time.RFC3339Nano), time.Now()) {
		t.Fatalf("two cameras offline: %v", w)
	}

	// a camera shared with Bob: he hears that it is offline, its events are not his to look at, his mode is his own
	a.share(garden)
	w = a.ok(a.bob, "GET", "/api/v1/pocket/widget", nil)
	if rows := items(t, w); w["line"] != "Garden is offline · Home · nothing new" || len(rows) != 1 || w["url"] != "/" {
		t.Fatalf("bob's widget: %v", w)
	}
	a.expect(401, "", "GET", "/api/v1/pocket/widget", nil)
}

func TestWidgetWhen(t *testing.T) {
	loc := time.FixedZone("here", 3600)
	now := time.Date(2026, 11, 27, 9, 30, 0, 0, loc)
	for iso, want := range map[string]string{
		"2026-11-27T07:15:00Z":           "08:15",
		"2026-11-26T23:30:00.123456789Z": "00:30",
		"2026-11-26T21:10:00Z":           "yesterday 22:10",
		"2026-11-23T07:05:00Z":           "Mon 23 Nov 08:05",
		"sometime":                       "",
	} {
		if got := widgetWhen(iso, now); got != want {
			t.Errorf("widgetWhen(%s) = %q, want %q", iso, got, want)
		}
	}
}

// A link token with one pocket:* scope reaches its one route and nothing else.
func TestPocketLinkTokens(t *testing.T) {
	a, _ := newCapApp(t)
	door := a.camera(a.alice, map[string]any{"name": "Front door"})
	ev := a.event(door, "Alice", time.Now().Add(-time.Minute), func(e *store.Event) { e.Labels, e.Summary = []string{"person"}, "Someone walks up to the door." })
	today := time.Now().Format("2006-01-02")
	cam, event := fmt.Sprintf("/api/v1/cameras/%d", door), fmt.Sprintf("/api/v1/events/%d", ev)
	for _, c := range []struct {
		tok, method, path string
		body              any
		want              int
	}{
		{"pcx_alice_search", "GET", "/api/v1/pocket/search?q=door", nil, 200},
		{"pcx_alice_day", "GET", "/api/v1/pocket/day/" + today, nil, 200},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/widget", nil, 200},
		{"pcx_alice_widget", "GET", "/api/v1/auth/me", nil, 200},
		// another capability's route
		{"pcx_alice_search", "GET", "/api/v1/pocket/day/" + today, nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/pocket/search?q=door", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/search?q=door", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/day/" + today, nil, 403},
		// Cameras keeps no quick lines: there is no such route
		{"pcx_alice_capture", "POST", "/api/v1/pocket/capture", map[string]any{"request_id": "req-0000000000000001", "text": "away"}, 404},
		{"pcx_alice_capture", "GET", "/api/v1/pocket/widget", nil, 403},
		// the rest of the app: nothing read, nothing watched, nothing changed
		{"pcx_alice_widget", "GET", "/api/v1/home", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/summary", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/mode", nil, 403},
		{"pcx_alice_widget", "PUT", "/api/v1/mode", map[string]any{"mode": "off"}, 403},
		{"pcx_alice_widget", "GET", "/api/v1/cameras", nil, 403},
		{"pcx_alice_widget", "GET", cam + "/snapshot.jpg", nil, 403},
		{"pcx_alice_widget", "GET", cam + "/live.mjpg", nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/search?q=door", nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/events", nil, 403},
		{"pcx_alice_search", "GET", event, nil, 403},
		{"pcx_alice_search", "GET", event + "/picture.jpg", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/day/" + today, nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/timeline", nil, 403},
		{"pcx_alice_day", "PUT", event, map[string]any{"reviewed": true}, 403},
		{"pcx_alice_day", "POST", "/api/v1/events/review", map[string]any{}, 403},
		{"pcx_alice_day", "GET", "/api/v1/settings", nil, 403},
		// the app's own scopes do not open the new routes
		{"pcx_alice_read", "GET", "/api/v1/pocket/search?q=door", nil, 403},
		{"pcx_alice_read", "GET", "/api/v1/pocket/day/" + today, nil, 403},
		{"pcx_alice_read", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_write", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_read", "GET", "/api/v1/search?q=door", nil, 200},
		{"pcx_nobody", "GET", "/api/v1/pocket/widget", nil, 401},
	} {
		if code, m, _ := a.call(c.tok, c.method, c.path, c.body); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%v)", c.tok, c.method, c.path, code, c.want, m)
		}
	}
	// the token acts for its person only
	if got := items(t, a.ok("pcx_alice_search", "GET", "/api/v1/pocket/search?q=door", nil)); len(got) != 2 || got[0]["title"] != "Front door" {
		t.Fatalf("alice through the link: %v", got)
	}
	if got := items(t, a.ok("pcx_alice_day", "GET", "/api/v1/pocket/day/"+today, nil)); len(got) == 0 {
		t.Fatalf("alice's day through the link: %v", got)
	}
	if w := a.ok("pcx_alice_widget", "GET", "/api/v1/pocket/widget", nil); w["line"] != "Home · 1 new event" {
		t.Fatalf("alice's widget through the link: %v", w)
	}
	if got := items(t, a.ok("pcx_bob_search", "GET", "/api/v1/pocket/search?q=door", nil)); len(got) != 0 {
		t.Fatalf("bob through the link finds alice's camera: %v", got)
	}
	if d := a.ok("pcx_bob_day", "GET", "/api/v1/pocket/day/"+today, nil); len(items(t, d)) != 0 {
		t.Fatalf("bob's day through the link: %v", d)
	}
	if w := a.ok("pcx_bob_widget", "GET", "/api/v1/pocket/widget", nil); w["line"] != nil || len(items(t, w)) != 0 {
		t.Fatalf("bob's widget through the link: %v", w)
	}
	// nothing was changed on the way, and a person cannot keep a line here either
	if e := a.ok(a.alice, "GET", event, nil); e["reviewed"] != false {
		t.Fatalf("the event was marked seen: %v", e)
	}
	if m := a.ok(a.alice, "GET", "/api/v1/mode", nil); m["mode"] != "home" {
		t.Fatalf("the mode changed: %v", m)
	}
	a.expect(404, a.alice, "POST", "/api/v1/pocket/capture", map[string]any{"request_id": "req-0000000000000001", "text": "away"})
}

func TestPocketLinkScopes(t *testing.T) {
	req := func(m, p string) *http.Request { return httptest.NewRequest(m, p, nil) }
	for _, c := range []struct {
		scope string
		r     *http.Request
		want  bool
	}{
		{core.ScopeSearch, req("GET", "/api/v1/pocket/search?q=x"), true},
		{core.ScopeDay, req("GET", "/api/v1/pocket/day/2026-01-02"), true},
		{core.ScopeWidget, req("GET", "/api/v1/pocket/widget"), true},
		{core.ScopeWidget, req("GET", "/api/v1/auth/me"), true},
		{core.ScopeCapture, req("POST", "/api/v1/pocket/capture"), false},
		{core.ScopeSearch, req("POST", "/api/v1/pocket/search"), false},
		{core.ScopeDay, req("GET", "/api/v1/pocket/day/today"), false},
		{core.ScopeDay, req("GET", "/api/v1/day/2026-01-02"), false},
		{core.ScopeWidget, req("GET", "/api/v1/summary"), false},
		{core.ScopeWidget, req("PUT", "/api/v1/mode"), false},
		{core.ScopeSearch, req("GET", "/api/v1/events/3/picture.jpg"), false},
		{"cameras:read", req("GET", "/api/v1/pocket/widget"), false},
		{"cameras:read", req("GET", "/api/v1/pocket/search"), false},
		{"cameras:write", req("GET", "/api/v1/pocket/day/2026-01-02"), false},
	} {
		if got := linkAllows([]string{c.scope}, c.r); got != c.want {
			t.Errorf("%s %s %s = %v, want %v", c.scope, c.r.Method, c.r.URL.Path, got, c.want)
		}
	}
}

// Cameras tells Core what it can do, so Search, Day and Hub find it on their own.
func TestManifestCapabilities(t *testing.T) {
	a, fc := newCapApp(t)
	if err := a.s.registerWithCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	fc.mu.Lock()
	m := fc.manifest
	fc.mu.Unlock()
	if m["slug"] != "cameras" || m["category"] != "Home" {
		t.Fatalf("manifest: %v %v", m["slug"], m["category"])
	}
	search, _ := m["search"].(map[string]any)
	day, _ := m["day"].(map[string]any)
	widget, _ := m["widget"].(map[string]any)
	if search == nil || fmt.Sprint(search["bangs"]) != "[cameras cam cctv]" || fmt.Sprint(search["kinds"]) != "[camera event]" || day == nil || day["label"] != "Cameras" ||
		day["one"] != "event" || day["many"] != "events" || widget == nil || widget["title"] != "Cameras" || m["capture"] != nil {
		t.Fatalf("capabilities: %v %v %v %v", search, day, widget, m["capture"])
	}
	offered := map[string]bool{}
	for _, o := range m["offers"].([]any) {
		offered[o.(map[string]any)["scope"].(string)] = true
	}
	for _, sc := range []string{"cameras:read", "cameras:write", core.ScopeSearch, core.ScopeDay, core.ScopeWidget} {
		if !offered[sc] {
			t.Errorf("the manifest does not offer %s: %v", sc, offered)
		}
	}
	if offered[core.ScopeCapture] {
		t.Errorf("the manifest offers a quick capture Cameras does not have")
	}
}
