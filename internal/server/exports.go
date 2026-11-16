package server

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-cameras/internal/store"
)

// The export is everything a person set up and saw: the cameras (without
// their passwords or secret addresses), every event with what was seen, and
// the changes of mode. The pictures and recordings themselves stay in the
// data folder (and the box's backup). Importing twice changes nothing. The
// same button takes a Frigate config (config.yml) too.

type exportDoc struct {
	App        string             `json:"app"`
	Version    string             `json:"version"`
	ExportedAt string             `json:"exported_at"`
	Cameras    []store.Camera     `json:"cameras"`
	Events     []exportEvent      `json:"events"`
	Modes      []store.ModeChange `json:"modes"`
}

type exportEvent struct {
	store.Event
	CameraName string `json:"camera_name"`
}

func (s *Server) export(ctx context.Context, uid int64) (*exportDoc, error) {
	doc := &exportDoc{App: "pocket-cameras", Version: Version, ExportedAt: store.Now(), Cameras: []store.Camera{}, Events: []exportEvent{}}
	cams, err := s.store.ListCameras(ctx, uid)
	if err != nil {
		return nil, err
	}
	for _, c := range cams {
		if !c.Mine {
			continue
		}
		c.Password, c.ShareKey = "", ""
		doc.Cameras = append(doc.Cameras, c)
	}
	evs, err := s.store.AllEvents(ctx, uid)
	if err != nil {
		return nil, err
	}
	for _, e := range evs {
		doc.Events = append(doc.Events, exportEvent{Event: e, CameraName: e.Camera})
	}
	if doc.Modes, err = s.store.ModeChanges(ctx, uid, "0", "9999", 1000000); err != nil {
		return nil, err
	}
	return doc, nil
}

func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	doc, err := s.export(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pocket-cameras-%s.json"`, time.Now().Format("2006-01-02")))
	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	_ = enc.Encode(doc)
}

func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	evs, err := s.store.AllEvents(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pocket-cameras-events-%s.csv"`, time.Now().Format("2006-01-02")))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"started", "ended", "camera", "kind", "seen", "summary", "worry", "mode", "alerted", "starred", "false alarm", "pet", "parcel", "weather", "note"})
	for _, e := range evs {
		end := ""
		if e.EndedAt != nil {
			end = *e.EndedAt
		}
		_ = cw.Write([]string{e.StartedAt, end, e.Camera, e.Kind, strings.Join(e.Labels, " "), e.Summary, fmt.Sprint(e.Worry), e.Mode, yes(e.Alerted), yes(e.Starred), yes(e.Nothing), e.Pet, e.ParcelText, e.Weather, e.Note})
	}
	cw.Flush()
}

func yes(b bool) string {
	if b {
		return "yes"
	}
	return ""
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if linkApp(ctx) != "" {
		writeErr(w, http.StatusForbidden, "apps cannot import")
		return
	}
	b, err := s.readBody(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "could not read the file")
		return
	}
	trim := strings.TrimSpace(string(b))
	if !strings.HasPrefix(trim, "{") {
		res, err := s.importFrigate(ctx, actorID(ctx), b)
		if err != nil {
			s.userFail(w, err, "import")
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	}
	var doc exportDoc
	if err := json.Unmarshal(b, &doc); err != nil || doc.App != "pocket-cameras" {
		writeErr(w, http.StatusBadRequest, "this is not a Pocket Cameras export or a Frigate config")
		return
	}
	res, err := s.importDoc(ctx, actorID(ctx), &doc)
	if err != nil {
		s.fail(w, err, "import")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) importDoc(ctx context.Context, uid int64, doc *exportDoc) (map[string]any, error) {
	have, err := s.store.ListCameras(ctx, uid)
	if err != nil {
		return nil, err
	}
	byName := map[string]int64{}
	for _, c := range have {
		if c.Mine {
			byName[strings.ToLower(c.Name)] = c.ID
		}
	}
	idMap := map[int64]int64{}
	newCams, newEvents, newModes := 0, 0, 0
	for _, c := range doc.Cameras {
		if id, ok := byName[strings.ToLower(c.Name)]; ok {
			idMap[c.ID] = id
			continue
		}
		old := c.ID
		c.UserID, c.Password, c.ShareKey = uid, "", ""
		if c.Zones == nil {
			c.Zones = []store.Zone{}
		}
		if c.Labels == nil {
			c.Labels = []string{}
		}
		if c.Modes == nil {
			c.Modes = []string{}
		}
		if err := s.store.CreateCamera(ctx, &c); err != nil {
			return nil, err
		}
		idMap[old] = c.ID
		byName[strings.ToLower(c.Name)] = c.ID
		newCams++
	}
	for _, e := range doc.Events {
		cid, ok := idMap[e.CameraID]
		if !ok {
			cid, ok = byName[strings.ToLower(e.CameraName)]
		}
		if !ok || e.StartedAt == "" || s.store.EventExists(ctx, cid, e.StartedAt) {
			continue
		}
		ev := e.Event
		ev.UserID, ev.CameraID, ev.Frames, ev.Clip = uid, cid, 0, false
		if err := s.store.CreateEvent(ctx, &ev); err != nil {
			return nil, err
		}
		newEvents++
	}
	for _, m := range doc.Modes {
		if m.At == "" || s.store.ModeChangeExists(ctx, uid, m.At) {
			continue
		}
		if err := s.store.AddModeChangeAt(ctx, uid, m.Mode, m.Reason, m.By, m.At); err != nil {
			return nil, err
		}
		newModes++
	}
	if newCams > 0 {
		s.engine.reload(ctx)
	}
	return map[string]any{"cameras": newCams, "events": newEvents, "modes": newModes,
		"text": fmt.Sprintf("Brought in %d %s and %d %s", newCams, plural(newCams, "camera", "cameras"), newEvents, plural(newEvents, "event", "events"))}, nil
}
