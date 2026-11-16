package server

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"time"

	"github.com/abjelosevic88/pocket-homelab-cameras/internal/capture"
	"github.com/abjelosevic88/pocket-homelab-cameras/internal/store"
)

// seedDemo fills the owner's account with four demo cameras (front door,
// driveway, back garden, living room) that draw their own pictures, three
// weeks of events with what was seen, and the modes the house was in. Only
// when the account has no cameras yet. The event pictures are drawn in the
// background, so the app starts at once.
func (s *Server) seedDemo(ctx context.Context, uid int64) error {
	ctx = context.WithoutCancel(ctx)
	if have, _ := s.store.ListCameras(ctx, uid); len(have) > 0 {
		return nil
	}
	rng := rand.New(rand.NewSource(42))
	type camSpec struct {
		name, scene, room string
		labels, modes     []string
		doorbell          bool
		perDay            int
	}
	specs := []camSpec{
		{"Front door", "door", "Hallway", []string{"person", "parcel", "vehicle"}, []string{"away", "night"}, true, 5},
		{"Driveway", "drive", "Outside", []string{"person", "vehicle"}, []string{"away", "night"}, false, 6},
		{"Back garden", "garden", "Garden", []string{"person", "animal"}, []string{"away", "night"}, false, 3},
		{"Living room", "room", "Living room", []string{"person"}, []string{"away"}, false, 1},
	}
	cams := make([]store.Camera, len(specs))
	for i, sp := range specs {
		c := newCamera(uid)
		c.Name, c.Kind, c.MainURL, c.Room, c.Labels, c.Modes, c.Doorbell = sp.name, "demo", "demo:"+sp.scene, sp.room, sp.labels, sp.modes, sp.doorbell
		c.Record, c.Brand = "events", ""
		if sp.scene == "garden" {
			c.Zones = []store.Zone{{Name: "Lawn", Kind: "watch", Points: [][2]float64{{0, 0.42}, {1, 0.42}, {1, 1}, {0, 1}}}}
			c.Sensitivity = 6
		}
		if sp.scene == "drive" {
			c.Zones = []store.Zone{{Name: "Street", Kind: "ignore", Points: [][2]float64{{0, 0.28}, {1, 0.28}, {1, 0.43}, {0, 0.43}}}}
		}
		if err := s.store.CreateCamera(ctx, &c); err != nil {
			return err
		}
		cams[i] = c
	}

	// the modes: away on weekdays 8:10–17:30, night 23:00–6:30
	days := 21
	start := time.Now().AddDate(0, 0, -days+1)
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.Local)
	modeAt := func(t time.Time) string {
		h := float64(t.Hour()) + float64(t.Minute())/60
		if h >= 23 || h < 6.5 {
			return "night"
		}
		if wd := t.Weekday(); wd != time.Saturday && wd != time.Sunday && h >= 8.17 && h < 17.5 {
			return "away"
		}
		return "home"
	}
	now := time.Now()
	for d := 0; d < days; d++ {
		day := start.AddDate(0, 0, d)
		marks := []struct {
			h, m       int
			mode, why  string
			by         string
			weekdaysOk bool
		}{
			{6, 30, "home", "the night is over", "auto", true}, {8, 10, "away", "nobody's phone is on the home Wi-Fi", "auto", false},
			{17, 30, "home", "someone is home", "auto", false}, {23, 0, "night", "night hours (23:00–06:00)", "auto", true},
		}
		for _, mk := range marks {
			at := time.Date(day.Year(), day.Month(), day.Day(), mk.h, mk.m+rng.Intn(9), 0, 0, time.Local)
			if at.After(now) {
				continue
			}
			if !mk.weekdaysOk && (day.Weekday() == time.Saturday || day.Weekday() == time.Sunday) {
				continue
			}
			_ = s.store.AddModeChangeAt(ctx, uid, mk.mode, mk.why, mk.by, at.UTC().Format(time.RFC3339Nano))
		}
	}
	st := s.settingsOf(ctx, uid)
	st.Mode, st.ModeBy, st.ModeReason, st.ModeAt = modeAt(now), "auto", "", now.UTC().Format(time.RFC3339)
	st.AutoMode, st.AwayPhones = true, true
	_ = s.store.SaveUserSettings(ctx, uid, st)

	// the events
	weathers := []string{"9 °C, cloudy", "12 °C, partly cloudy", "6 °C, light rain, 1.2 mm rain", "4 °C, clear", "11 °C, overcast, wind 32 km/h", "7 °C, fog"}
	parcels := []string{"DHL — Noise-cancelling headphones", "GLS — Bike lights", "DPD — Coffee beans", "Post — Book: The Overstory"}
	type pic struct {
		uid, id   int64
		scene     string
		at        time.Time
		kind      string
		frames    int
		nothingEv bool
	}
	var pics []pic
	for d := 0; d < days; d++ {
		day := start.AddDate(0, 0, d)
		for ci, sp := range specs {
			n := sp.perDay + rng.Intn(3) - 1
			for k := 0; k < n; k++ {
				hour := 7 + rng.Intn(15)
				if rng.Intn(6) == 0 {
					hour = rng.Intn(5) // something at night
				}
				at := time.Date(day.Year(), day.Month(), day.Day(), hour, rng.Intn(60), rng.Intn(60), 0, time.Local)
				if at.After(now.Add(-2 * time.Minute)) {
					continue
				}
				mode := modeAt(at)
				if sp.scene == "room" && mode != "away" && rng.Intn(3) > 0 {
					continue // indoors only matters when away
				}
				kind := capture.DemoActorKind(sp.scene, int64(rng.Intn(20)))
				c := cams[ci]
				ev := store.Event{UserID: uid, CameraID: c.ID, Camera: c.Name, Kind: "motion", Mode: mode, AIState: "done", Frames: 4, Best: 2 + rng.Intn(2),
					Score:     0.02 + rng.Float64()*0.15,
					StartedAt: at.UTC().Format(time.RFC3339Nano), Weather: weathers[(d+ci)%len(weathers)]}
				end := at.Add(time.Duration(6+rng.Intn(25)) * time.Second).UTC().Format(time.RFC3339Nano)
				ev.EndedAt = &end
				nothing := rng.Intn(10) == 0
				if nothing {
					ev.Labels, ev.Summary, ev.Nothing, ev.Frames = []string{}, "Nothing there: a change in the light.", true, 2
				} else {
					ev.Labels, ev.Summary, ev.Worry = DemoWords(sp.scene, kind, at)
					if kind == "cat" {
						ev.Pet = "Luna"
						ev.Summary = "Luna walks across " + map[string]string{"door": "the path to the door", "garden": "the lawn", "room": "the living room"}[sp.scene] + "."
					}
					if kind == "courier" {
						ev.ParcelText = parcels[rng.Intn(len(parcels))]
					}
				}
				if sp.doorbell && kind != "cat" && !nothing && rng.Intn(4) == 0 {
					ev.Kind = "doorbell"
				}
				hit := false
				for _, l := range ev.Labels {
					if slices.Contains(sp.labels, l) {
						hit = true
					}
				}
				if ev.Pet != "" {
					hit = false
				}
				ev.Alerted = !ev.Nothing && ((ev.Kind == "doorbell") || (hit && slices.Contains(sp.modes, mode)))
				ev.Reviewed = at.Before(now.Add(-36 * time.Hour))
				ev.Starred = !ev.Nothing && ev.Worry > 0 && rng.Intn(3) == 0
				if err := s.store.CreateEvent(ctx, &ev); err != nil {
					return err
				}
				pics = append(pics, pic{uid, ev.ID, sp.scene, at, kind, ev.Frames, ev.Nothing})
			}
		}
	}
	// draw the pictures, newest first, in the background
	go func() {
		for i := len(pics) - 1; i >= 0; i-- {
			p := pics[i]
			for f := 1; f <= p.frames; f++ {
				var acts []capture.Actor
				if !p.nothingEv {
					acts = []capture.Actor{{Kind: p.kind, P: 0.15 + 0.7*float64(f-1)/float64(max(1, p.frames-1))}}
				}
				t := p.at.Add(time.Duration(f) * time.Second)
				_ = s.writeEventFile(p.uid, p.id, fmt.Sprintf("f%02d.jpg", f), capture.EncodeJPEG(capture.DemoFrame(p.scene, t, acts, 640, 360), 70))
			}
		}
		s.log.Info("demo: event pictures drawn", "events", len(pics))
	}()
	s.engine.reload(ctx)
	return nil
}
