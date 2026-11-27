package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-cameras/internal/capture"
	"github.com/abjelosevic88/pocket-homelab-cameras/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-cameras/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// ---- what is it? ----
//
// A new event's clearest picture goes to the AI the person chose (Pocket AI
// through Core, or their own OpenAI key) with what the other apps know: the
// household's pets (Pocket Pets), parcels due today (Pocket Parcels) and the
// weather (Pocket Weather). The answer — person, vehicle, animal, parcel or
// nothing, and one sentence — decides whether the person is told.

// Labels are what an event can be about.
var Labels = []string{"person", "vehicle", "animal", "parcel"}

const aiSystem = `You look at one picture from a home security camera and say what is in it. Answer with JSON only:
{"labels": [], "summary": "", "worry": 0, "pet": "", "parcel": false}
- labels: any of "person", "vehicle", "animal", "parcel" that you can actually see. [] when nothing real is there (shadows, a light change, rain, snow, insects, leaves or branches moving, a reflection).
- summary: one short plain sentence for a phone notification, saying who or what is where and what they are doing ("A courier leaves a parcel at the front door.", "A grey cat walks across the lawn."). Never guess who a person is.
- worry: 0 for ordinary things (people coming home, deliveries, animals, cars passing), 1 when it is worth a look (someone lingering, looking around, trying a door or window, a person at night), 2 when it is urgent (someone climbing in, breaking something, fire or smoke, a person lying on the ground).
- pet: when an animal looks like one of the household's pets listed, that pet's name; else "".
- parcel: true when a parcel is being delivered or is lying there.`

type aiAnswer struct {
	Labels  []string `json:"labels"`
	Summary string   `json:"summary"`
	Worry   int      `json:"worry"`
	Pet     string   `json:"pet"`
	Parcel  bool     `json:"parcel"`
}

// checkEvent asks what an event is, then alerts when it should.
func (s *Server) checkEvent(ctx context.Context, id int64, pic []byte) {
	ev, err := s.store.EventByID(ctx, id)
	if err != nil {
		return
	}
	cam, err := s.store.CameraByID(ctx, ev.CameraID)
	if err != nil {
		return
	}
	st := s.settingsOf(ctx, cam.UserID)
	info := s.eventContext(ctx, cam.UserID, st)
	ev.Weather = info.weather
	when, _ := time.Parse(time.RFC3339Nano, ev.StartedAt)
	ready := false
	if st.AIHelp && cam.AICheck && st.AIPhotos {
		cl, _, _ := s.aiFor(ctx, cam.UserID, st)
		ready = cl != nil
	}
	switch {
	case ev.Kind != "motion" && !ready:
		ev.AIState = "off"
	case ready:
		var a aiAnswer
		prompt := s.aiPrompt(cam, when, info)
		actx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		err := s.aiAsk(actx, cam.UserID, st, aiSystem, prompt, []aiImage{{Mime: "image/jpeg", Data: pic}}, &a)
		cancel()
		if err != nil {
			s.log.Debug("camera: AI check failed", "event", id, "err", err)
			ev.AIState = "failed"
			if cam.Kind == "demo" {
				s.demoTruth(ev, cam, when, info)
			}
			break
		}
		ev.AIState = "done"
		ev.Labels = cleanLabels(a.Labels, a.Parcel)
		ev.Summary = clip(strings.TrimSpace(a.Summary), 300)
		ev.Worry = clampInt(a.Worry, 0, 2)
		ev.Pet = petName(a.Pet, info.pets)
	case cam.Kind == "demo":
		s.demoTruth(ev, cam, when, info)
	default:
		ev.AIState = "off"
	}
	if ev.Kind == "motion" && ev.AIState == "done" && len(ev.Labels) == 0 {
		ev.Nothing = true
	}
	if slices.Contains(ev.Labels, "parcel") || strings.Contains(strings.ToLower(ev.Summary), "courier") || strings.Contains(strings.ToLower(ev.Summary), "deliver") {
		if p := info.parcelNow(); p != nil {
			ev.ParcelRef, ev.ParcelText = p.ID, p.Text
		}
	}
	ev.Alerted = s.maybeAlert(ctx, ev, cam, st, info)
	if err := s.store.SaveEventCheck(ctx, ev); err != nil {
		s.log.Warn("camera: event check not saved", "event", id, "err", err)
	}
	if ev.Nothing {
		return
	}
	payload := map[string]any{"camera_id": cam.ID, "camera": cam.Name, "event_id": ev.ID, "labels": ev.Labels, "summary": ev.Summary, "worry": ev.Worry, "mode": ev.Mode, "url": s.link(fmt.Sprintf("/events/%d", ev.ID))}
	subject := firstNonEmpty(ev.Summary, eventTitle(ev, cam.Name))
	s.publish(ctx, cam.UserID, "cameras.event", subject, payload)
	for _, l := range ev.Labels {
		s.publish(ctx, cam.UserID, "cameras."+l, subject, payload)
	}
}

func cleanLabels(in []string, parcel bool) []string {
	out := []string{}
	for _, l := range in {
		l = strings.ToLower(strings.TrimSpace(l))
		switch l {
		case "car", "truck", "van", "bike", "bicycle", "motorcycle":
			l = "vehicle"
		case "cat", "dog", "bird", "fox", "deer":
			l = "animal"
		case "package", "box":
			l = "parcel"
		}
		if slices.Contains(Labels, l) && !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	if parcel && !slices.Contains(out, "parcel") {
		out = append(out, "parcel")
	}
	return out
}

func petName(name string, pets []petInfo) string {
	name = strings.TrimSpace(name)
	for _, p := range pets {
		if name != "" && strings.EqualFold(p.Name, name) {
			return p.Name
		}
	}
	return ""
}

func (s *Server) aiPrompt(cam *store.Camera, when time.Time, info eventInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Camera: %s", cam.Name)
	if cam.Room != "" {
		fmt.Fprintf(&b, " (%s)", cam.Room)
	}
	lt := when.Local()
	light := "daylight"
	if h := lt.Hour(); h < 7 || h >= 17 {
		light = "dark outside"
	}
	fmt.Fprintf(&b, ".\nLocal time: %s (%s).", lt.Format("Mon 2 Jan 15:04"), light)
	if len(info.pets) > 0 {
		names := []string{}
		for _, p := range info.pets {
			names = append(names, strings.TrimSpace(p.Name+" ("+joinNonEmpty(", ", p.Species, p.Color)+")"))
		}
		fmt.Fprintf(&b, "\nThe household's pets: %s.", strings.Join(names, "; "))
	}
	if p := info.parcelNow(); p != nil {
		fmt.Fprintf(&b, "\nA parcel is due today: %s.", p.Text)
	}
	if info.weather != "" {
		fmt.Fprintf(&b, "\nWeather: %s.", info.weather)
	}
	return b.String()
}

// demoTruth says what a demo camera drew (no AI needed for the demo).
func (s *Server) demoTruth(ev *store.Event, cam *store.Camera, when time.Time, info eventInfo) {
	scene := strings.TrimPrefix(cam.MainURL, "demo:")
	acts := capture.DemoActors(scene, when.Add(2*time.Second))
	if len(acts) == 0 {
		acts = capture.DemoActors(scene, when.Add(5*time.Second))
	}
	ev.AIState = "done"
	if len(acts) == 0 {
		ev.Labels, ev.Summary = []string{}, "Nothing there: a change in the light."
		return
	}
	ev.Labels, ev.Summary, ev.Worry = DemoWords(scene, acts[0].Kind, when)
	if acts[0].Kind == "cat" && len(info.pets) > 0 {
		ev.Pet = info.pets[0].Name
		ev.Summary = strings.Replace(ev.Summary, "A cat", info.pets[0].Name, 1)
	}
}

// DemoWords are the labels and sentence for a demo visitor.
func DemoWords(scene, kind string, when time.Time) ([]string, string, int) {
	night := when.Local().Hour() >= 20 || when.Local().Hour() < 6
	switch kind {
	case "courier":
		return []string{"person", "parcel"}, "A courier in an orange vest leaves a parcel at the front door.", 0
	case "car":
		return []string{"vehicle"}, "A red car drives past on the street.", 0
	case "fox":
		return []string{"animal"}, "A fox crosses the " + map[bool]string{true: "garden", false: "driveway"}[scene == "garden"] + ".", 0
	case "cat":
		where := map[string]string{"door": "the path to the door", "garden": "the lawn", "room": "the living room"}[scene]
		return []string{"animal"}, "A cat walks across " + firstNonEmpty(where, "the picture") + ".", 0
	}
	switch scene {
	case "door":
		if night {
			return []string{"person"}, "Someone walks up to the front door in the dark.", 1
		}
		return []string{"person"}, "Someone walks up to the front door.", 0
	case "drive":
		if night {
			return []string{"person"}, "A person walks up the driveway at night.", 1
		}
		return []string{"person"}, "A person walks up the driveway towards the house.", 0
	case "garden":
		return []string{"person"}, "Someone walks across the back garden.", map[bool]int{true: 1, false: 0}[night]
	}
	return []string{"person"}, "Someone walks through the living room.", 0
}

// ---- what the other apps know (cached a few minutes per person) ----

type petInfo struct {
	Name, Species, Color string
}

type parcelInfo struct {
	ID, Text, Status, Expected, URL string
}

type eventInfo struct {
	pets    []petInfo
	parcels []parcelInfo
	weather string
	windy   bool
}

// parcelNow is the parcel most likely being delivered right now.
func (i eventInfo) parcelNow() *parcelInfo {
	today := time.Now().Format("2006-01-02")
	for _, want := range []func(p parcelInfo) bool{
		func(p parcelInfo) bool { return p.Status == "out_for_delivery" },
		func(p parcelInfo) bool { return p.Expected == today && p.Status != "delivered" },
	} {
		for k := range i.parcels {
			if want(i.parcels[k]) {
				return &i.parcels[k]
			}
		}
	}
	return nil
}

func (s *Server) cached(ctx context.Context, key string, maxAge time.Duration, v any, fetch func() (any, error)) {
	if raw, at, ok := s.store.CacheGet(ctx, key); ok {
		if t, err := time.Parse(time.RFC3339Nano, at); err == nil && time.Since(t) < maxAge && json.Unmarshal([]byte(raw), v) == nil {
			return
		}
	}
	got, err := fetch()
	if err != nil {
		return
	}
	b, _ := json.Marshal(got)
	_ = s.store.CachePut(ctx, key, string(b))
	_ = json.Unmarshal(b, v)
}

func (s *Server) eventContext(ctx context.Context, uid int64, st store.Settings) eventInfo {
	var out eventInfo
	type wx struct {
		Text  string
		Windy bool
	}
	var pets []petInfo
	s.cached(ctx, fmt.Sprintf("ctx:%d:pets", uid), time.Hour, &pets, func() (any, error) { return s.fetchPets(ctx, uid) })
	var parcels []parcelInfo
	s.cached(ctx, fmt.Sprintf("ctx:%d:parcels", uid), 10*time.Minute, &parcels, func() (any, error) { return s.fetchParcels(ctx, uid) })
	var w wx
	s.cached(ctx, fmt.Sprintf("ctx:%d:weather", uid), 15*time.Minute, &w, func() (any, error) {
		t, windy, err := s.fetchWeather(ctx, uid)
		return wx{t, windy}, err
	})
	out.pets, out.parcels, out.weather, out.windy = pets, parcels, w.Text, w.Windy
	return out
}

// ---- telling people ----

func eventTitle(ev *store.Event, camName string) string {
	return eventWhat(ev) + " — " + camName
}

// eventWhat is what an event was, in a word or two: "Person", "Parcel",
// "Doorbell", the pet's name, an alarm's own words.
func eventWhat(ev *store.Event) string {
	if ev.Kind == "doorbell" {
		return "Doorbell"
	}
	if ev.Kind == "trigger" {
		return firstNonEmpty(ev.Note, "Alarm")
	}
	what := "Movement"
	order := []string{"parcel", "person", "vehicle", "animal"}
	for _, l := range order {
		if slices.Contains(ev.Labels, l) {
			what = map[string]string{"parcel": "Parcel", "person": "Person", "vehicle": "Vehicle", "animal": "Animal"}[l]
			if l == "animal" && ev.Pet != "" {
				what = ev.Pet
			}
			break
		}
	}
	return what
}

// alertReason says why an event does or does not alert ("" = it does).
func alertReason(ev *store.Event, cam *store.Camera, st store.Settings, info eventInfo, lastAlert string) string {
	if ev.Nothing {
		return "nothing there"
	}
	if ev.Kind == "doorbell" {
		if !st.NotifyDoorbell {
			return "doorbell alerts are off"
		}
		return ""
	}
	if !st.NotifyEvents {
		return "event alerts are off"
	}
	if ev.Mode == "off" || ev.Mode == "" {
		return "the cameras are paused"
	}
	if !slices.Contains(cam.Modes, ev.Mode) {
		return "this camera does not alert in " + ev.Mode + " mode"
	}
	if ev.Kind == "motion" {
		if ev.AIState == "done" {
			hit := false
			for _, l := range ev.Labels {
				if slices.Contains(cam.Labels, l) {
					hit = true
				}
			}
			if !hit && ev.Worry < 2 {
				return "not something this camera alerts on"
			}
			if ev.Pet != "" && st.PetsQuiet && len(ev.Labels) == 1 && ev.Labels[0] == "animal" {
				return "it was " + ev.Pet
			}
		} else if st.WeatherCalm && info.windy {
			return "stormy weather: movement alone does not alert"
		}
	}
	if lastAlert != "" && st.Cooldown > 0 && ev.Worry < 2 {
		if t, err := time.Parse(time.RFC3339Nano, lastAlert); err == nil && time.Since(t) < time.Duration(st.Cooldown)*time.Minute {
			return "an alert from this camera went out a moment ago"
		}
	}
	return ""
}

func (s *Server) maybeAlert(ctx context.Context, ev *store.Event, cam *store.Camera, st store.Settings, info eventInfo) bool {
	if why := alertReason(ev, cam, st, info, s.store.LastAlert(ctx, cam.ID)); why != "" {
		s.log.Debug("camera: no alert", "event", ev.ID, "why", why)
		return false
	}
	title := eventTitle(ev, cam.Name)
	body := firstNonEmpty(ev.Summary, "Something moved.")
	if ev.ParcelText != "" {
		body += " Probably your parcel: " + ev.ParcelText + "."
	}
	prio := "default"
	if ev.Worry >= 2 {
		prio = "urgent"
	} else if ev.Worry == 1 || ev.Kind == "doorbell" {
		prio = "high"
	}
	path := fmt.Sprintf("/events/%d", ev.ID)
	sent := s.tell(ctx, cam.UserID, st, title, body, path, prio, ev.Kind == "doorbell" || ev.Worry > 0)
	shares, _ := s.store.ListShares(ctx, cam.ID)
	for _, sh := range shares {
		if !sh.Notify {
			continue
		}
		vst := s.settingsOf(ctx, sh.UserID)
		if (ev.Kind == "doorbell" && !vst.NotifyDoorbell) || (ev.Kind != "doorbell" && !vst.NotifyEvents) {
			continue
		}
		if s.tell(ctx, sh.UserID, vst, title, body, path, prio, ev.Kind == "doorbell") {
			sent = true
		}
	}
	return sent
}

// tell reaches a person: Pocket Alerts when it is connected (it goes on to
// the phone), else Pocket Hub on their phone through Core; and their own
// notification URL when they set one.
func (s *Server) tell(ctx context.Context, uid int64, st store.Settings, title, body, path, priority string, sound bool) bool {
	sent := s.alert(ctx, uid, title, body, path, priority)
	if !sent && s.core.Configured() {
		if u, err := s.store.GetUser(ctx, uid); err == nil {
			if res, err := s.core.Notify(ctx, u.Email, core.PushMessage{Title: clip(title, 120), Body: clip(body, 900), URL: s.link(path), Source: "cameras", Sound: sound, Thread: "cameras"}); err == nil && res.Sent > 0 {
				sent = true
			}
		}
	}
	if st.NotifyURL != "" {
		n := notify.New(st.NotifyURL, st.NotifyFormat)
		np := "info"
		if priority == "high" || priority == "urgent" {
			np = "high"
		}
		if err := n.Send(ctx, notify.Message{Title: title, Body: body, Priority: np, Tags: []string{"camera"}, Link: s.link(path)}); err == nil {
			sent = true
		} else {
			s.log.Debug("notify failed", "user", uid, "err", err)
		}
	}
	return sent
}

// cameraGone tells the owner a camera stopped sending pictures.
func (s *Server) cameraGone(ctx context.Context, cam store.Camera, why string) {
	_ = s.store.SetCameraSeen(ctx, cam.ID, time.Time{}, firstNonEmpty(why, "no pictures for a while"))
	s.publish(ctx, cam.UserID, "cameras.offline", cam.Name+" stopped sending pictures", map[string]any{"camera_id": cam.ID, "camera": cam.Name, "error": why})
	st := s.settingsOf(ctx, cam.UserID)
	if !st.NotifyOffline || cam.Kind == "demo" {
		return
	}
	// a camera that never worked since it was added is not news
	if cam.LastSeenAt == nil {
		return
	}
	s.tell(ctx, cam.UserID, st, cam.Name+" is offline", firstNonEmpty(why, "No pictures for a minute.")+" Check its power and the network.", fmt.Sprintf("/cameras/%d", cam.ID), "high", false)
}

// cameraBack tells the owner a camera is sending again.
func (s *Server) cameraBack(ctx context.Context, cam store.Camera) {
	s.publish(ctx, cam.UserID, "cameras.online", cam.Name+" is back", map[string]any{"camera_id": cam.ID, "camera": cam.Name})
	st := s.settingsOf(ctx, cam.UserID)
	if st.NotifyOffline && cam.Kind != "demo" {
		s.tell(ctx, cam.UserID, st, cam.Name+" is back", "Pictures are coming in again.", fmt.Sprintf("/cameras/%d", cam.ID), "low", false)
	}
}

// ---- event pictures ----

func (s *Server) eventPicture(ev *store.Event, n int) ([]byte, error) {
	if n <= 0 {
		n = ev.Best
	}
	return os.ReadFile(filepath.Join(s.eventDir(ev.UserID, ev.ID), fmt.Sprintf("f%02d.jpg", n)))
}
