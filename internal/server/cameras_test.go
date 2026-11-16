package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-cameras/internal/capture"
	"github.com/abjelosevic88/pocket-homelab-cameras/internal/config"
	"github.com/abjelosevic88/pocket-homelab-cameras/internal/store"
)

// addEvent puts an event on a camera straight into the store.
func (a *app) addEvent(cameraID int64, owner string, labels []string, summary string, at time.Time) int64 {
	a.t.Helper()
	e := &store.Event{UserID: a.ids[owner], CameraID: cameraID, Kind: "motion", StartedAt: at.UTC().Format(time.RFC3339Nano), Labels: labels, Summary: summary, AIState: "done", Mode: "away", Frames: 1, Best: 1}
	if err := a.st.CreateEvent(context.Background(), e); err != nil {
		a.t.Fatal(err)
	}
	return e.ID
}

func TestPersonalIsolation(t *testing.T) {
	a := newApp(t, nil)
	c := a.ok(a.alice, "POST", "/api/v1/cameras", map[string]any{"name": "Front door", "kind": "rtsp", "brand": "reolink", "host": "192.168.1.50", "username": "admin", "password": "s3cret"})
	cid := id(c)
	if c["has_password"] != true || strings.Contains(fmt.Sprint(c), "s3cret") {
		t.Fatalf("the password must never come back: %v", c)
	}
	if !strings.Contains(fmt.Sprint(c["main_url"]), "h264Preview_01_main") {
		t.Fatalf("the brand should fill the address: %v", c["main_url"])
	}
	n, _ := a.st.GetCamera(context.Background(), a.ids["Alice"], int64(num(c, "id")))
	ev := a.addEvent(n.ID, "Alice", []string{"person"}, "Someone at the door.", time.Now())

	// Bob sees nothing of Alice's
	for _, p := range []string{"/api/v1/cameras/" + cid, fmt.Sprintf("/api/v1/events/%d", ev), "/api/v1/cameras/" + cid + "/snapshot.jpg", fmt.Sprintf("/api/v1/events/%d/picture.jpg", ev)} {
		a.expect(404, a.bob, "GET", p, nil)
	}
	a.expect(404, a.bob, "PUT", "/api/v1/cameras/"+cid, map[string]any{"name": "Mine now"})
	a.expect(404, a.bob, "DELETE", "/api/v1/cameras/"+cid, nil)
	a.expect(404, a.bob, "PUT", fmt.Sprintf("/api/v1/events/%d", ev), map[string]any{"starred": true})
	a.expect(404, a.bob, "POST", "/api/v1/cameras/"+cid+"/ring", nil)
	if l := a.list(a.bob, "/api/v1/cameras"); len(l) != 0 {
		t.Fatalf("bob sees %d cameras", len(l))
	}
	_, m, _ := a.call(a.bob, "GET", "/api/v1/events", nil)
	if len(m["events"].([]any)) != 0 {
		t.Fatalf("bob sees alice's events")
	}
	_, m, _ = a.call(a.bob, "GET", "/api/v1/search?q=door", nil)
	if len(m["hits"].([]any)) != 0 {
		t.Fatalf("bob finds alice's things: %v", m["hits"])
	}
	// even an admin does not see others' cameras
	a.ok(a.bob, "POST", "/api/v1/cameras", map[string]any{"name": "Bob's shed", "kind": "demo"})
	for _, x := range a.list(a.alice, "/api/v1/cameras") {
		if str(x.(map[string]any), "name") == "Bob's shed" {
			t.Fatal("alice (admin) sees bob's camera")
		}
	}
}

func TestSharingIsWatchOnly(t *testing.T) {
	a := newApp(t, nil)
	c := a.ok(a.alice, "POST", "/api/v1/cameras", map[string]any{"name": "Garden", "kind": "demo", "password": "pw"})
	cid := id(c)
	ev := a.addEvent(int64(num(c, "id")), "Alice", []string{"animal"}, "A fox.", time.Now())
	a.ok(a.alice, "PUT", "/api/v1/cameras/"+cid+"/shares", map[string]any{"shares": []map[string]any{{"user_id": a.ids["Bob"], "notify": true}}})

	got := a.ok(a.bob, "GET", "/api/v1/cameras/"+cid, nil)
	if got["mine"] != false || got["owner"] != "Alice" {
		t.Fatalf("shared camera should say whose it is: %v", got)
	}
	a.ok(a.bob, "GET", fmt.Sprintf("/api/v1/events/%d", ev), nil)
	// watching yes, changing no
	a.expect(404, a.bob, "PUT", "/api/v1/cameras/"+cid, map[string]any{"name": "x"})
	a.expect(404, a.bob, "PUT", "/api/v1/cameras/"+cid+"/shares", map[string]any{"shares": []any{}})
	a.expect(404, a.bob, "DELETE", fmt.Sprintf("/api/v1/events/%d", ev), nil)
	a.expect(404, a.bob, "PUT", fmt.Sprintf("/api/v1/events/%d", ev), map[string]any{"starred": true})
	// bob may switch the alerts off for himself
	a.ok(a.bob, "PUT", "/api/v1/cameras/"+cid+"/notify", map[string]any{"notify": false})
	if a.st.ShareNotify(context.Background(), a.ids["Bob"], int64(num(c, "id"))) {
		t.Fatal("notify should be off")
	}
	// unshared: gone again
	a.ok(a.alice, "PUT", "/api/v1/cameras/"+cid+"/shares", map[string]any{"shares": []any{}})
	a.expect(404, a.bob, "GET", "/api/v1/cameras/"+cid, nil)
}

func TestShareKey(t *testing.T) {
	a := newApp(t, nil)
	c := a.ok(a.alice, "POST", "/api/v1/cameras", map[string]any{"name": "Door", "kind": "demo"})
	res := a.ok(a.alice, "POST", "/api/v1/cameras/"+id(c)+"/share-key", map[string]any{"on": true})
	urls := res["share_urls"].(map[string]any)
	snap := strings.TrimPrefix(str(urls, "snapshot"), "http://cameras.test")
	r, err := http.Get(a.url + snap + "?fresh=1")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("secret picture: %d %s", r.StatusCode, r.Header.Get("Content-Type"))
	}
	a.ok(a.alice, "POST", "/api/v1/cameras/"+id(c)+"/share-key", map[string]any{"on": false})
	r, _ = http.Get(a.url + snap)
	r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatalf("switched-off address still answers: %d", r.StatusCode)
	}
}

func TestCameraValidation(t *testing.T) {
	a := newApp(t, nil)
	a.expect(400, a.alice, "POST", "/api/v1/cameras", map[string]any{"name": ""})
	a.expect(400, a.alice, "POST", "/api/v1/cameras", map[string]any{"name": "x", "kind": "rtsp"})
	a.expect(400, a.alice, "POST", "/api/v1/cameras", map[string]any{"name": "x", "kind": "rtsp", "main_url": "ftp://cam"})
	c := a.ok(a.alice, "POST", "/api/v1/cameras", map[string]any{"name": "x", "kind": "rtsp", "main_url": "rtsp://u:p@10.0.0.5/stream1",
		"labels": []string{"person", "dragon"}, "modes": []string{"away", "party"}, "sensitivity": 99,
		"zones": []map[string]any{{"name": "Path", "kind": "watch", "points": [][2]float64{{0, 0}, {1, 0}, {0.5, 2}}}, {"name": "bad", "points": [][2]float64{{0, 0}}}}})
	if fmt.Sprint(c["labels"]) != "[person]" || fmt.Sprint(c["modes"]) != "[away]" || num(c, "sensitivity") != 10 {
		t.Fatalf("not cleaned: %v %v %v", c["labels"], c["modes"], c["sensitivity"])
	}
	if z := c["zones"].([]any); len(z) != 1 {
		t.Fatalf("zones: %v", z)
	}
	if !strings.Contains(str(c, "main_url"), "•••") || c["host"] != "10.0.0.5" {
		t.Fatalf("the address password should be hidden and the host read: %v %v", c["main_url"], c["host"])
	}
	// sending the hidden address back keeps the real one
	a.ok(a.alice, "PUT", "/api/v1/cameras/"+id(c), map[string]any{"main_url": c["main_url"], "name": "y"})
	got, _ := a.st.GetOwnCamera(context.Background(), a.ids["Alice"], int64(num(c, "id")))
	if got.MainURL != "rtsp://u:p@10.0.0.5/stream1" {
		t.Fatalf("address lost: %q", got.MainURL)
	}
}

// Pocket Core link tokens reach only what the cameras offer.
func TestLinkScopes(t *testing.T) {
	cases := []struct {
		method, path string
		scopes       []string
		want         bool
	}{
		{"GET", "/api/v1/summary", []string{"cameras:read"}, true},
		{"GET", "/api/v1/day/2026-11-16", []string{"cameras:read"}, true},
		{"GET", "/api/v1/events/12", []string{"cameras:read"}, true},
		{"GET", "/api/v1/events/12/picture.jpg", []string{"cameras:read"}, true},
		{"GET", "/api/v1/cameras/3/live.mjpg", []string{"cameras:read"}, false},
		{"PUT", "/api/v1/mode", []string{"cameras:read"}, false},
		{"PUT", "/api/v1/mode", []string{"cameras:write"}, true},
		{"POST", "/api/v1/cameras/3/ring", []string{"cameras:write"}, true},
		{"PUT", "/api/v1/cameras/3", []string{"cameras:write"}, false},
		{"DELETE", "/api/v1/events/3", []string{"cameras:write"}, false},
		{"POST", "/api/v1/cameras", []string{"cameras:write"}, false},
		{"GET", "/api/v1/export.json", []string{"cameras:read"}, false},
		{"GET", "/api/v1/settings", []string{"cameras:read", "cameras:write"}, false},
		{"GET", "/api/v1/auth/me", nil, true},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if got := linkAllows(c.scopes, r); got != c.want {
			t.Errorf("%s %s with %v = %v, want %v", c.method, c.path, c.scopes, got, c.want)
		}
	}
	re := regexp.MustCompile(`^[a-z0-9][a-z0-9_.:-]{0,60}$`)
	for _, o := range linkOffers {
		if !re.MatchString(o.Scope) {
			t.Errorf("bad scope %q", o.Scope)
		}
	}
}

func TestExportImportTwice(t *testing.T) {
	a := newApp(t, nil)
	c := a.ok(a.alice, "POST", "/api/v1/cameras", map[string]any{"name": "Porch", "kind": "rtsp", "main_url": "rtsp://10.0.0.9/live", "password": "pw"})
	a.addEvent(int64(num(c, "id")), "Alice", []string{"parcel"}, "A parcel.", time.Now().Add(-time.Hour))
	_ = a.st.AddModeChange(context.Background(), a.ids["Alice"], "away", "test", "you")
	doc, err := a.s.export(context.Background(), a.ids["Alice"])
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(doc)
	if strings.Contains(string(b), `"password":"pw"`) {
		t.Fatal("the export holds a password")
	}
	var back exportDoc
	_ = json.Unmarshal(b, &back)
	for i := 0; i < 2; i++ {
		res, err := a.s.importDoc(context.Background(), a.ids["Bob"], &back)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && (res["cameras"] != 1 || res["events"] != 1 || res["modes"] != 1) {
			t.Fatalf("first import: %v", res)
		}
		if i == 1 && (res["cameras"] != 0 || res["events"] != 0 || res["modes"] != 0) {
			t.Fatalf("second import added again: %v", res)
		}
	}
	if l := a.list(a.bob, "/api/v1/cameras"); len(l) != 1 {
		t.Fatalf("bob has %d cameras", len(l))
	}
}

func TestFrigateImport(t *testing.T) {
	a := newApp(t, nil)
	cfg := `
mqtt:
  enabled: false
go2rtc:
  streams:
    front_door:
      - rtsp://admin:pw@192.168.1.20:554/h264Preview_01_main
    front_door_sub:
      - "ffmpeg:rtsp://admin:pw@192.168.1.20:554/h264Preview_01_sub#video=copy"
record:
  enabled: true
  retain:
    days: 5
    mode: motion
objects:
  track: [person, car, dog]
cameras:
  front_door:
    ffmpeg:
      inputs:
        - path: rtsp://127.0.0.1:8554/front_door
          roles: [record]
        - path: rtsp://127.0.0.1:8554/front_door_sub
          roles: [detect]
    detect:
      width: 640
      height: 360
    zones:
      porch:
        coordinates: 0,360,640,360,640,200,0,200
    motion:
      mask:
        - 0.0,0.0,0.4,0.0,0.4,0.1,0.0,0.1
  garage:
    enabled: false
    ffmpeg:
      inputs:
        - path: rtsp://{FRIGATE_RTSP_USER}:{FRIGATE_RTSP_PASSWORD}@192.168.1.21/stream1
          roles: [detect, record]
    record:
      enabled: true
      retain:
        days: 14
        mode: all
`
	res, err := a.s.importFrigate(context.Background(), a.ids["Alice"], []byte(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if res["cameras"] != 2 || len(res["warnings"].([]string)) != 1 {
		t.Fatalf("import: %v", res)
	}
	cams, _ := a.st.ListCameras(context.Background(), a.ids["Alice"])
	var fd, gar *store.Camera
	for i := range cams {
		switch cams[i].Name {
		case "Front door":
			fd = &cams[i]
		case "Garage":
			gar = &cams[i]
		}
	}
	if fd == nil || gar == nil {
		t.Fatalf("names: %+v", cams)
	}
	if fd.MainURL != "rtsp://admin:pw@192.168.1.20:554/h264Preview_01_main" || fd.SubURL != "rtsp://admin:pw@192.168.1.20:554/h264Preview_01_sub" {
		t.Fatalf("go2rtc not followed: %q %q", fd.MainURL, fd.SubURL)
	}
	if fd.RetainDays != 5 || fd.Record != "events" || fmt.Sprint(fd.Labels) != "[person vehicle animal]" || fd.Brand != "" && fd.Brand != "reolink" {
		t.Fatalf("front door: %+v", fd)
	}
	if len(fd.Zones) != 2 || fd.Zones[0].Kind != "watch" || fd.Zones[0].Points[0] != [2]float64{0, 1} || fd.Zones[1].Kind != "ignore" {
		t.Fatalf("zones: %+v", fd.Zones)
	}
	if gar.Enabled || gar.Record != "always" || gar.RetainDays != 14 {
		t.Fatalf("garage: %+v", gar)
	}
	// again: nothing new
	res, _ = a.s.importFrigate(context.Background(), a.ids["Alice"], []byte(cfg))
	if res["cameras"] != 0 || res["skipped"] != 2 {
		t.Fatalf("second import: %v", res)
	}
}

func TestAlertRules(t *testing.T) {
	cam := &store.Camera{Name: "Door", Labels: []string{"person", "parcel"}, Modes: []string{"away", "night"}}
	st := store.Settings{NotifyEvents: true, NotifyDoorbell: true, PetsQuiet: true, WeatherCalm: true, Cooldown: 2}
	ev := func(mode string, labels ...string) *store.Event {
		return &store.Event{Kind: "motion", Mode: mode, Labels: labels, AIState: "done"}
	}
	cases := []struct {
		name string
		ev   *store.Event
		info eventInfo
		last string
		want bool
	}{
		{"person away", ev("away", "person"), eventInfo{}, "", true},
		{"person home", ev("home", "person"), eventInfo{}, "", false},
		{"paused", ev("off", "person"), eventInfo{}, "", false},
		{"car not watched", ev("away", "vehicle"), eventInfo{}, "", false},
		{"nothing", &store.Event{Kind: "motion", Mode: "away", Nothing: true}, eventInfo{}, "", false},
		{"doorbell at home", &store.Event{Kind: "doorbell", Mode: "home"}, eventInfo{}, "", true},
		{"cooldown", ev("away", "person"), eventInfo{}, time.Now().Add(-time.Minute).Format(time.RFC3339Nano), false},
		{"urgent ignores cooldown", &store.Event{Kind: "motion", Mode: "away", Labels: []string{"person"}, AIState: "done", Worry: 2}, eventInfo{}, time.Now().Format(time.RFC3339Nano), true},
		{"no AI, windy", &store.Event{Kind: "motion", Mode: "away", AIState: "off"}, eventInfo{windy: true}, "", false},
		{"no AI, calm", &store.Event{Kind: "motion", Mode: "away", AIState: "off"}, eventInfo{}, "", true},
	}
	for _, c := range cases {
		if got := alertReason(c.ev, cam, st, c.info, c.last) == ""; got != c.want {
			t.Errorf("%s: alert = %v, want %v (%s)", c.name, got, c.want, alertReason(c.ev, cam, st, c.info, c.last))
		}
	}
	pet := &store.Event{Kind: "motion", Mode: "away", Labels: []string{"animal"}, AIState: "done", Pet: "Luna"}
	cam2 := &store.Camera{Labels: []string{"animal"}, Modes: []string{"away"}}
	if alertReason(pet, cam2, st, eventInfo{}, "") == "" {
		t.Error("the household's own cat should stay quiet")
	}
}

func TestNightHours(t *testing.T) {
	for _, c := range []struct {
		h, from, to int
		want        bool
	}{{23, 23, 6, true}, {2, 23, 6, true}, {6, 23, 6, false}, {12, 23, 6, false}, {1, 0, 7, true}, {7, 0, 7, false}, {5, 5, 5, false}} {
		if got := inNight(c.h, c.from, c.to); got != c.want {
			t.Errorf("inNight(%d, %d, %d) = %v", c.h, c.from, c.to, got)
		}
	}
}

func TestModeByHandAndByLink(t *testing.T) {
	a := newApp(t, nil)
	m := a.ok(a.alice, "PUT", "/api/v1/mode", map[string]any{"mode": "away", "reason": "holiday"})
	if m["mode"] != "away" || m["by"] != "you" {
		t.Fatalf("mode: %v", m)
	}
	a.expect(400, a.alice, "PUT", "/api/v1/mode", map[string]any{"mode": "party"})
	// Bob's mode is his own
	if b := a.ok(a.bob, "GET", "/api/v1/mode", nil); b["mode"] != "home" {
		t.Fatalf("bob's mode changed with alice's: %v", b["mode"])
	}
	ch, _ := a.st.RecentModeChanges(context.Background(), a.ids["Alice"], 5)
	if len(ch) != 1 || ch[0].Reason != "holiday" {
		t.Fatalf("mode log: %+v", ch)
	}
}

func TestDoorbellWebhook(t *testing.T) {
	a := newApp(t, nil)
	c := a.ok(a.alice, "POST", "/api/v1/cameras", map[string]any{"name": "Door", "kind": "demo", "doorbell": true})
	ev := a.ok(a.alice, "POST", "/api/v1/cameras/"+id(c)+"/ring", map[string]any{})
	if ev["kind"] != "doorbell" || num(ev, "frames") != 1 {
		t.Fatalf("ring: %v", ev)
	}
	r, err := http.NewRequest("GET", a.url+"/api/v1/events/"+id(ev)+"/picture.jpg", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+a.alice)
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("doorbell picture: %d", res.StatusCode)
	}
}

func TestDemoSeedAndDay(t *testing.T) {
	a := newApp(t, func(c *config.Config) { c.DemoData = true })
	// the demo lands in the owner's (first admin's) account when Bootstrap runs; seed explicitly for the test users
	if err := a.s.seedDemo(context.Background(), a.ids["Alice"]); err != nil {
		t.Fatal(err)
	}
	if l := a.list(a.alice, "/api/v1/cameras"); len(l) != 4 {
		t.Fatalf("demo cameras: %d", len(l))
	}
	sum := a.ok(a.alice, "GET", "/api/v1/summary", nil)
	if num(sum, "total") != 4 || str(sum, "text") == "" {
		t.Fatalf("summary: %v", sum)
	}
	y := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	day := a.ok(a.alice, "GET", "/api/v1/day/"+y, nil)
	if len(day["items"].([]any)) == 0 {
		t.Fatalf("an empty day yesterday: %v", day)
	}
	st := a.ok(a.alice, "GET", "/api/v1/stats", nil)
	if len(st["days"].([]any)) != 30 || len(st["hours"].([]any)) != 7 {
		t.Fatalf("stats: %v", st)
	}
	hits := a.ok(a.alice, "GET", "/api/v1/search?q=parcel", nil)
	if len(hits["hits"].([]any)) == 0 {
		t.Fatal("no parcels found")
	}
	tl := a.ok(a.alice, "GET", "/api/v1/timeline?day="+y, nil)
	if len(tl["rows"].([]any)) != 4 {
		t.Fatalf("timeline rows: %v", tl["rows"])
	}
	// bob still sees nothing
	if l := a.list(a.bob, "/api/v1/cameras"); len(l) != 0 {
		t.Fatal("bob sees the demo")
	}
}

func TestMotionDetector(t *testing.T) {
	d := capture.NewDetector(nil, 5)
	at := time.Date(2026, 11, 16, 12, 0, 0, 0, time.Local)
	empty := capture.DemoFrame("garden", at, nil, 320, 180)
	for i := 0; i < 5; i++ {
		if m := d.Feed(empty); m.Moving {
			t.Fatalf("an empty garden moved (frame %d, score %.4f)", i, m.Score)
		}
	}
	moving := false
	for i := 0; i < 6; i++ {
		f := capture.DemoFrame("garden", at, []capture.Actor{{Kind: "person", P: 0.2 + float64(i)*0.1}}, 320, 180)
		if m := d.Feed(f); m.Moving {
			moving = true
			if m.Box[2] <= 0 || m.Box[3] <= 0 {
				t.Fatalf("no box: %+v", m)
			}
		}
	}
	if !moving {
		t.Fatal("a person walking was not seen")
	}
	// an ignore zone over the whole picture sees nothing
	d2 := capture.NewDetector([]capture.Zone{{Kind: "ignore", Points: [][2]float64{{0, 0}, {1, 0}, {1, 1}, {0, 1}}}}, 10)
	d2.Feed(empty)
	for i := 0; i < 6; i++ {
		if m := d2.Feed(capture.DemoFrame("garden", at, []capture.Actor{{Kind: "person", P: float64(i) * 0.15}}, 320, 180)); m.Moving {
			t.Fatal("movement in an ignored zone counted")
		}
	}
}

func TestDemoCameraMakesEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the engine")
	}
	// the drawn demo scene, fed straight to a worker, makes an event with pictures
	a := newApp(t, nil)
	c := a.ok(a.alice, "POST", "/api/v1/cameras", map[string]any{"name": "Garden", "kind": "demo", "main_url": "demo:garden"})
	cam, _ := a.st.GetOwnCamera(context.Background(), a.ids["Alice"], int64(num(c, "id")))
	w := &worker{e: a.s.engine, cam: *cam, subs: map[chan []byte]struct{}{}, det: capture.NewDetector(nil, 6), started: time.Now()}
	ctx := context.Background()
	at := time.Date(2026, 11, 16, 12, 0, 0, 0, time.Local)
	now := time.Now()
	feed := func(i int, acts []capture.Actor) {
		b := capture.EncodeJPEG(capture.DemoFrame("garden", at, acts, 320, 180), 80)
		m, _ := w.det.FeedJPEG(b)
		w.motion(ctx, now.Add(time.Duration(i)*time.Second), b, m)
	}
	for i := 0; i < 3; i++ {
		feed(i, nil)
	}
	for i := 3; i < 9; i++ {
		feed(i, []capture.Actor{{Kind: "person", P: float64(i-3) * 0.15}})
	}
	for i := 9; i < 22; i++ {
		feed(i, nil)
	}
	evs, _, _ := a.st.ListEvents(ctx, a.ids["Alice"], store.EventFilter{Nothing: true})
	if len(evs) != 1 || evs[0].Frames < 2 || evs[0].EndedAt == nil {
		t.Fatalf("events: %+v", evs)
	}
}
