package server

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// ---- Home / Away / Night / Off ----
//
// The mode decides which cameras alert: each camera says in which modes it
// does (a garden camera at night and when away; the living room only when
// away). Off pauses every alert but the doorbell; the cameras still record.
// With "switch by itself" on, the mode follows the night hours, a trip in
// Pocket Travel, where you are (Pocket Movement) and whose phones are on the
// home Wi-Fi (Pocket Network). A mode set by hand stays until the next time
// that picture changes (you come home, the night starts).

var allModes = []string{"home", "away", "night", "off"}

var modeWords = map[string]string{"home": "Home", "away": "Away", "night": "Night", "off": "Off"}

// setMode changes a person's mode and writes it down.
func (s *Server) setMode(ctx context.Context, uid int64, mode, reason, by string) error {
	st := s.settingsOf(ctx, uid)
	if st.Mode == mode {
		return nil
	}
	st.Mode, st.ModeAt, st.ModeBy, st.ModeReason = mode, time.Now().UTC().Format(time.RFC3339), by, reason
	if err := s.store.SaveUserSettings(ctx, uid, st); err != nil {
		return err
	}
	_ = s.store.AddModeChange(ctx, uid, mode, reason, by)
	s.publish(ctx, uid, "cameras.mode_changed", modeWords[mode]+firstNonEmpty(map[bool]string{true: ": " + reason}[reason != ""], ""), map[string]any{"mode": mode, "reason": reason, "by": by})
	return nil
}

func (s *Server) modeOut(ctx context.Context, uid int64) map[string]any {
	st := s.settingsOf(ctx, uid)
	recent, _ := s.store.RecentModeChanges(ctx, uid, 8)
	auto := map[string]any{}
	if st.AutoMode {
		want, why := s.wantedMode(ctx, uid)
		auto = map[string]any{"mode": want, "reason": why}
	}
	return map[string]any{"mode": st.Mode, "at": st.ModeAt, "by": st.ModeBy, "reason": st.ModeReason, "auto_mode": st.AutoMode, "auto": auto,
		"night_from": st.NightFrom, "night_to": st.NightTo, "recent": recent}
}

func (s *Server) handleGetMode(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.modeOut(r.Context(), actorID(r.Context())))
}

// handleSetMode sets the mode by hand (or from another app, through a link).
func (s *Server) handleSetMode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		Mode     string `json:"mode"`
		Reason   string `json:"reason"`
		AutoMode *bool  `json:"auto_mode"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.AutoMode != nil {
		st := s.settingsOf(ctx, uid)
		st.AutoMode = *in.AutoMode
		if err := s.store.SaveUserSettings(ctx, uid, st); err != nil {
			s.fail(w, err, "mode")
			return
		}
		_ = s.store.CacheDelete(ctx, fmt.Sprintf("auto:%d", uid))
		if st.AutoMode && in.Mode == "" {
			s.autoMode(ctx, uid)
		}
	}
	if in.Mode != "" {
		if !slices.Contains(allModes, in.Mode) {
			writeErr(w, http.StatusBadRequest, "the mode is home, away, night or off")
			return
		}
		by := firstNonEmpty(linkApp(ctx), "you")
		if err := s.setMode(ctx, uid, in.Mode, cleanText(in.Reason, 120), by); err != nil {
			s.fail(w, err, "mode")
			return
		}
	}
	writeJSON(w, http.StatusOK, s.modeOut(ctx, uid))
}

func inNight(h, from, to int) bool {
	if from == to {
		return false
	}
	if from < to {
		return h >= from && h < to
	}
	return h >= from || h < to
}

// wantedMode is what the mode would be now, and why.
func (s *Server) wantedMode(ctx context.Context, uid int64) (string, string) {
	st := s.settingsOf(ctx, uid)
	if st.AwayTrips {
		if trip, err := s.onTrip(ctx, uid); err == nil && trip != "" {
			return "away", "on a trip: " + trip
		}
	}
	away, votes := 0, 0
	why := []string{}
	if st.AwayOut {
		if home, known, where, err := s.atHome(ctx, uid); err == nil && known {
			votes++
			if !home {
				away++
				why = append(why, "you are out ("+where+")")
			}
		}
	}
	if st.AwayPhones {
		if phones, err := s.phonesHome(ctx, uid); err == nil {
			votes++
			counts := false
			for _, p := range phones {
				if len(st.Phones) == 0 || slices.ContainsFunc(st.Phones, func(x string) bool { return strings.EqualFold(x, p) }) {
					counts = true
				}
			}
			if !counts {
				away++
				why = append(why, "nobody's phone is on the home Wi-Fi")
			}
		}
	}
	if votes > 0 && away == votes {
		return "away", strings.Join(why, " and ")
	}
	if inNight(time.Now().Hour(), st.NightFrom, st.NightTo) {
		return "night", fmt.Sprintf("night hours (%02d:00–%02d:00)", st.NightFrom, st.NightTo)
	}
	if votes > 0 {
		return "home", "someone is home"
	}
	return "home", "day hours"
}

// autoMode moves a person's mode when what it should be changed.
func (s *Server) autoMode(ctx context.Context, uid int64) {
	st := s.settingsOf(ctx, uid)
	if !st.AutoMode {
		return
	}
	want, why := s.wantedMode(ctx, uid)
	key := fmt.Sprintf("auto:%d", uid)
	last, _, _ := s.store.CacheGet(ctx, key)
	if last == want {
		return // a mode set by hand stays until the picture changes
	}
	_ = s.store.CachePut(ctx, key, want)
	if err := s.setMode(ctx, uid, want, why, "auto"); err != nil {
		s.log.Warn("cameras: auto mode failed", "user", uid, "err", err)
	}
}

func (s *Server) autoModes(ctx context.Context) {
	ids, err := s.store.SettingsUsers(ctx)
	if err != nil {
		return
	}
	for _, uid := range ids {
		s.autoMode(ctx, uid)
	}
}
