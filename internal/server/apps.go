package server

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-cameras/internal/capture"
	"github.com/abjelosevic88/pocket-homelab-cameras/internal/store"
)

// The other Pocket apps. None of it is needed: when an app is not there,
// the page greys the button and says what to do.

func linkTo(sib *siblingConn, path string) string {
	if sib == nil || sib.public == "" {
		return ""
	}
	return strings.TrimRight(sib.public, "/") + path
}

func plainErr(a pocketApp, err error) string {
	if isNotConnected(err) {
		return err.Error()
	}
	if ue, ok := err.(*userErr); ok {
		return ue.msg
	}
	return a.name + ": " + err.Error()
}

func (s *Server) appFail(w http.ResponseWriter, a pocketApp, err error) {
	if isNotConnected(err) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if ue, ok := err.(*userErr); ok {
		writeErr(w, http.StatusBadRequest, ue.msg)
		return
	}
	writeErr(w, http.StatusBadGateway, plainErr(a, err))
}

// handleApps lists how the person reaches each app, in words they can act on.
func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c := s.clients(ctx, actorID(ctx))
	out := []appState{}
	for _, a := range pocketApps {
		out = append(out, c.status(ctx, a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": out, "core": s.core.Configured()})
}

func (s *Server) sib(w http.ResponseWriter, r *http.Request, a pocketApp, scopes ...string) (*siblingConn, bool) {
	ctx := r.Context()
	sib, err := s.clients(ctx, actorID(ctx)).conn(ctx, a, scopes...)
	if err != nil {
		s.appFail(w, a, err)
		return nil, false
	}
	return sib, true
}

func (s *Server) sibFor(ctx context.Context, uid int64, a pocketApp, scopes ...string) (*siblingConn, error) {
	return s.clients(ctx, uid).conn(ctx, a, scopes...)
}

func ifs(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}

// ---- Pocket Alerts ----

func (s *Server) alert(ctx context.Context, uid int64, title, body, path, priority string) bool {
	sib, err := s.sibFor(ctx, uid, appAlerts, scopeAlertsWrite)
	if err != nil {
		return false
	}
	_, err = postJSONTo(ctx, sib, "/api/v1/alerts", map[string]any{"title": clip(title, 200), "body": clip(body, 2000), "priority": priority, "tags": []string{"camera"}, "click": s.link(path)})
	if err != nil {
		s.log.Debug("pocket alerts: send failed", "err", err)
		return false
	}
	return true
}

// ---- what the AI is told: pets, parcels, weather ----

func (s *Server) fetchPets(ctx context.Context, uid int64) ([]petInfo, error) {
	sib, err := s.sibFor(ctx, uid, appPets, scopePetsRead)
	if err != nil {
		if isNotConnected(err) {
			return []petInfo{}, nil
		}
		return nil, err
	}
	v, err := getJSONFrom(ctx, sib, "/api/v1/pets", nil)
	if err != nil {
		return nil, err
	}
	out := []petInfo{}
	for _, p := range rows(v, "pets") {
		if p["archived"] == true || str(p, "passed_on") != "" {
			continue
		}
		out = append(out, petInfo{Name: str(p, "name"), Species: str(p, "species"), Color: str(p, "color")})
	}
	return out, nil
}

func (s *Server) fetchParcels(ctx context.Context, uid int64) ([]parcelInfo, error) {
	sib, err := s.sibFor(ctx, uid, appParcels, scopeParcelsRead)
	if err != nil {
		if isNotConnected(err) {
			return []parcelInfo{}, nil
		}
		return nil, err
	}
	v, err := getJSONFrom(ctx, sib, "/api/v1/parcels", url.Values{"view": {"active"}, "limit": {"100"}})
	if err != nil {
		return nil, err
	}
	out := []parcelInfo{}
	for _, p := range rows(v, "parcels") {
		if str(p, "direction") == "outgoing" {
			continue
		}
		text := joinNonEmpty(" — ", firstNonEmpty(str(p, "carrier_name"), str(p, "carrier")), firstNonEmpty(str(p, "title"), str(p, "shop"), str(p, "tracking_number")))
		out = append(out, parcelInfo{ID: idOf(p), Text: text, Status: str(p, "status"), Expected: day10(p["expected_on"]), URL: linkTo(sib, "/parcels/"+idOf(p))})
	}
	return out, nil
}

// fetchWeather is the weather at home now ("wind 48 km/h, rain") and whether it is stormy.
func (s *Server) fetchWeather(ctx context.Context, uid int64) (string, bool, error) {
	sib, err := s.sibFor(ctx, uid, appWeather, scopeWeatherRead)
	if err != nil {
		if isNotConnected(err) {
			return "", false, nil
		}
		return "", false, err
	}
	v, err := getJSONFrom(ctx, sib, "/api/v1/summary", nil)
	if err != nil {
		return "", false, err
	}
	m, _ := v.(map[string]any)
	now := obj(m, "now")
	if now == nil {
		return "", false, nil
	}
	wind, gust, precip, temp := num(now, "wind"), num(now, "gust"), num(now, "precip"), num(now, "temp")
	cond := str(now, "condition")
	parts := []string{fmt.Sprintf("%.0f °C", temp)}
	if t := str(now, "text"); t != "" {
		parts = append(parts, strings.ToLower(t))
	} else if cond != "" && cond != "unknown" {
		parts = append(parts, strings.ReplaceAll(cond, "-", " "))
	}
	if wind >= 15 || gust >= 30 {
		parts = append(parts, fmt.Sprintf("wind %.0f km/h", math.Max(wind, gust)))
	}
	if precip > 0 {
		parts = append(parts, fmt.Sprintf("%.1f mm rain", precip))
	}
	windy := gust >= 50 || wind >= 35 || precip >= 2 || cond == "storm" || cond == "snow"
	return strings.Join(parts, ", "), windy, nil
}

func (s *Server) handlePets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pets, err := s.fetchPets(ctx, actorID(ctx))
	if err != nil {
		s.appFail(w, appPets, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pets": pets})
}

func (s *Server) handleParcels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := s.fetchParcels(ctx, actorID(ctx))
	if err != nil {
		s.appFail(w, appParcels, err)
		return
	}
	out := []map[string]any{}
	for _, p := range list {
		out = append(out, map[string]any{"id": p.ID, "text": p.Text, "status": p.Status, "expected_on": p.Expected, "url": p.URL})
	}
	writeJSON(w, http.StatusOK, map[string]any{"parcels": out})
}

// ---- who is home: trips, where you are, phones on the Wi-Fi ----

// onTrip is the trip going on today, "" when none.
func (s *Server) onTrip(ctx context.Context, uid int64) (string, error) {
	sib, err := s.sibFor(ctx, uid, appTravel, scopeTripsRead)
	if err != nil {
		return "", err
	}
	v, err := getJSONFrom(ctx, sib, "/api/v1/trips", nil)
	if err != nil {
		return "", err
	}
	today := time.Now().Format("2006-01-02")
	for _, t := range rows(v, "trips") {
		if t["archived"] == true || str(t, "status") == "cancelled" {
			continue
		}
		from, to := day10(t["start_date"]), day10(t["end_date"])
		if from != "" && from <= today && (to == "" || today <= to) {
			return firstNonEmpty(str(t, "name"), str(t, "destination"), "a trip"), nil
		}
	}
	return "", nil
}

// atHome says whether Pocket Movement places you at home (known = it knows).
func (s *Server) atHome(ctx context.Context, uid int64) (home, known bool, where string, err error) {
	sib, err := s.sibFor(ctx, uid, appMovement, scopeMovementRead)
	if err != nil {
		return false, false, "", err
	}
	v, err := getJSONFrom(ctx, sib, "/api/v1/now", nil)
	if err != nil {
		return false, false, "", err
	}
	m, _ := v.(map[string]any)
	pt := obj(m, "point")
	if pt == nil {
		return false, false, "", nil
	}
	ts := num(pt, "ts")
	if ts == 0 || time.Since(time.Unix(int64(ts), 0)) > 3*time.Hour {
		return false, false, "", nil // too old to say
	}
	visit := obj(m, "visit")
	place := obj(visit, "place")
	if place != nil && str(place, "category") == "home" {
		return true, true, str(place, "name"), nil
	}
	if place != nil {
		return false, true, firstNonEmpty(str(place, "name"), str(visit, "name")), nil
	}
	return false, true, "on the move", nil
}

// phonesHome lists whose phones are on the home Wi-Fi now (Pocket Network).
func (s *Server) phonesHome(ctx context.Context, uid int64) ([]string, error) {
	sib, err := s.sibFor(ctx, uid, appNetwork, scopeNetworkRead)
	if err != nil {
		return nil, err
	}
	v, err := getJSONFrom(ctx, sib, "/api/v1/dashboard", nil)
	if err != nil {
		return nil, err
	}
	m, _ := v.(map[string]any)
	out := []string{}
	for _, h := range rows(m["home"]) {
		if o := str(h, "owner"); o != "" {
			out = append(out, o)
		}
	}
	return out, nil
}

// handleOwners lists the people Pocket Network knows phones of (for Settings).
func (s *Server) handleOwners(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sib, ok := s.sib(w, r, appNetwork, scopeNetworkRead)
	if !ok {
		return
	}
	v, err := getJSONFrom(ctx, sib, "/api/v1/devices", nil)
	if err != nil {
		s.appFail(w, appNetwork, err)
		return
	}
	seen := map[string]bool{}
	out := []string{}
	for _, d := range rows(v, "devices") {
		o := strings.TrimSpace(str(d, "owner"))
		kind := firstNonEmpty(str(d, "kind"), str(d, "kind_guess"))
		if o == "" || seen[o] || (kind != "phone" && kind != "watch" && kind != "tablet") {
			continue
		}
		seen[o] = true
		out = append(out, o)
	}
	sort.Strings(out)
	home, _ := s.phonesHome(ctx, actorID(ctx))
	writeJSON(w, http.StatusOK, map[string]any{"owners": out, "home": home})
}

// networkCameras are the devices on the home network that look like cameras.
func (s *Server) networkCameras(ctx context.Context, uid int64) ([]found, error) {
	sib, err := s.sibFor(ctx, uid, appNetwork, scopeNetworkRead)
	if err != nil {
		return nil, err
	}
	v, err := getJSONFrom(ctx, sib, "/api/v1/devices", nil)
	if err != nil {
		return nil, err
	}
	out := []found{}
	for _, d := range rows(v, "devices") {
		kind := strings.ToLower(firstNonEmpty(str(d, "kind"), str(d, "kind_guess")))
		vendor := str(d, "vendor")
		name := firstNonEmpty(str(d, "name"), str(d, "hostname"))
		brand := capture.GuessBrand(vendor + " " + name + " " + str(d, "model"))
		ports := str(d, "ports")
		rtsp := strings.Contains(ports, `"port":554`) || strings.Contains(ports, `"port": 554`)
		if !(strings.Contains(kind, "camera") || rtsp || (brand != "" && brand != "tapo" && brand != "unifi")) {
			continue
		}
		ip := str(d, "last_ip")
		if ip == "" {
			continue
		}
		out = append(out, found{IP: ip, Name: name, Vendor: vendor, Brand: brand, RTSP: rtsp, From: "network", Online: d["online"] == true})
	}
	return out, nil
}

// ---- Pocket Rooms and Pocket Home: where a camera is ----

func (s *Server) handleRooms(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sib, ok := s.sib(w, r, appRooms, scopeRoomsRead)
	if !ok {
		return
	}
	v, err := getJSONFrom(ctx, sib, "/api/v1/plans", nil)
	if err != nil {
		s.appFail(w, appRooms, err)
		return
	}
	out := []map[string]any{}
	plans := rows(v, "plans")
	for i, p := range plans {
		if i >= 6 || p["archived"] == true {
			continue
		}
		pid := idOf(p)
		sv, err := getJSONFrom(ctx, sib, "/api/v1/plans/"+url.PathEscape(pid)+"/summary", nil)
		if err != nil {
			continue
		}
		sm, _ := sv.(map[string]any)
		for _, f := range rows(sm["floors"]) {
			for _, rm := range rows(f["rooms"]) {
				name := str(rm, "name")
				if name == "" {
					continue
				}
				out = append(out, map[string]any{"ref": pid + ":" + idOf(rm), "name": name, "floor": str(f, "floor_name"), "plan": str(p, "name"),
					"url": linkTo(sib, "/plans/"+url.PathEscape(pid))})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"rooms": out})
}

func (s *Server) handleHomeThings(w http.ResponseWriter, r *http.Request) {
	sib, ok := s.sib(w, r, appHome, scopeHomeRead)
	if !ok {
		return
	}
	q := url.Values{}
	if v := r.URL.Query().Get("q"); v != "" {
		q.Set("q", v)
	}
	v, err := getJSONFrom(r.Context(), sib, "/api/v1/things", q)
	if err != nil {
		s.appFail(w, appHome, err)
		return
	}
	out := []map[string]any{}
	for _, t := range rows(v, "things") {
		out = append(out, map[string]any{"id": idOf(t), "name": str(t, "name"), "room": str(t, "room"), "brand": str(t, "brand"), "model": str(t, "model"), "url": linkTo(sib, "/things/"+idOf(t))})
	}
	writeJSON(w, http.StatusOK, map[string]any{"things": out})
}

// homeLog writes into a camera's Pocket Home log (once per key).
func (s *Server) homeLog(ctx context.Context, cam store.Camera, title, notes, key string) {
	if cam.HomeRef == "" {
		return
	}
	sib, err := s.sibFor(ctx, cam.UserID, appHome, scopeHomeWrite)
	if err != nil {
		return
	}
	body := map[string]any{"thing_id": cam.HomeRef, "kind": "repair", "title": clip(title, 200), "notes": notes, "date": time.Now().Format("2006-01-02"), "external_id": "cameras:" + key}
	if n, err := strconv.ParseInt(cam.HomeRef, 10, 64); err == nil {
		body["thing_id"] = n
	}
	if _, err := postJSONTo(ctx, sib, "/api/v1/records", body); err != nil {
		s.log.Debug("pocket home: log failed", "err", err)
	}
}

// ---- lists for the "send to" menus ----

func (s *Server) handleChats(w http.ResponseWriter, r *http.Request) {
	sib, ok := s.sib(w, r, appChat, scopeChatRead)
	if !ok {
		return
	}
	v, err := getJSONFrom(r.Context(), sib, "/api/v1/conversations", nil)
	if err != nil {
		s.appFail(w, appChat, err)
		return
	}
	out := []map[string]any{}
	for _, c := range rows(v, "conversations") {
		if c["archived"] == true {
			continue
		}
		out = append(out, map[string]any{"id": idOf(c), "name": firstNonEmpty(str(c, "name"), str(c, "title"))})
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": out})
}

func (s *Server) handleTaskLists(w http.ResponseWriter, r *http.Request) {
	sib, ok := s.sib(w, r, appTasks, scopeTasksRead)
	if !ok {
		return
	}
	v, err := getJSONFrom(r.Context(), sib, "/api/v1/lists", nil)
	if err != nil {
		s.appFail(w, appTasks, err)
		return
	}
	out := []map[string]any{}
	for _, l := range rows(v, "lists") {
		out = append(out, map[string]any{"id": idOf(l), "name": str(l, "name", "title")})
	}
	writeJSON(w, http.StatusOK, map[string]any{"lists": out})
}

// ---- an event, sent somewhere ----

func (s *Server) eventFor(w http.ResponseWriter, r *http.Request) (*store.Event, *store.Camera, bool) {
	ctx := r.Context()
	ev, err := s.store.GetEvent(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "event")
		return nil, nil, false
	}
	cam, err := s.store.GetCamera(ctx, actorID(ctx), ev.CameraID)
	if err != nil {
		s.fail(w, err, "camera")
		return nil, nil, false
	}
	return ev, cam, true
}

func eventWords(ev *store.Event, cam *store.Camera) (title, text string) {
	t, _ := time.Parse(time.RFC3339Nano, ev.StartedAt)
	title = eventTitle(ev, cam.Name) + ", " + t.Local().Format("2 Jan 15:04")
	text = firstNonEmpty(ev.Summary, "Movement at "+cam.Name+".")
	if ev.Note != "" {
		text += "\n\n" + ev.Note
	}
	return title, text
}

func (s *Server) handleEventChat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ev, cam, ok := s.eventFor(w, r)
	if !ok {
		return
	}
	var in struct {
		To   string `json:"to"`
		Clip bool   `json:"clip"`
	}
	_ = decode(r, &in)
	sib, ok := s.sib(w, r, appChat, scopeChatRead, scopeChatWrite)
	if !ok {
		return
	}
	st := s.settingsOf(ctx, actorID(ctx))
	to := firstNonEmpty(in.To, st.ChatTo)
	title, text := eventWords(ev, cam)
	link := s.link(fmt.Sprintf("/events/%d", ev.ID))
	cid := s.chatID(ctx, sib, to)
	var atts []int64
	if cid != "" {
		name, data, ctype := "", []byte(nil), ""
		if in.Clip {
			if p, err := s.eventClip(ctx, ev, cam); err == nil {
				if b, err := os.ReadFile(p); err == nil && len(b) < 45<<20 {
					name, data, ctype = fmt.Sprintf("event-%d.mp4", ev.ID), b, "video/mp4"
				}
			}
		}
		if data == nil {
			if b, err := s.eventPicture(ev, 0); err == nil {
				name, data, ctype = fmt.Sprintf("event-%d.jpg", ev.ID), b, "image/jpeg"
			}
		}
		if data != nil {
			if id, err := s.chatUpload(ctx, sib, cid, name, ctype, data); err == nil {
				atts = append(atts, id)
			}
		}
	}
	card := map[string]any{"title": title, "app": "cameras", "kind": "event", "subtitle": cam.Name, "text": firstNonEmpty(ev.Summary, ""), "url": link, "icon": "camera"}
	var err error
	var res map[string]any
	if cid != "" {
		res, err = postJSONTo(ctx, sib, "/api/v1/conversations/"+url.PathEscape(cid)+"/messages", map[string]any{"text": title + "\n" + text, "attachments": atts, "card": card})
	} else {
		res, err = postJSONTo(ctx, sib, "/api/v1/send", map[string]any{"to": firstNonEmpty(to, "me"), "text": title + "\n" + text, "card": card})
	}
	if err != nil {
		s.appFail(w, appChat, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "text": "Sent to Pocket Chat", "url": linkTo(sib, "/c/"+firstNonEmpty(cid, str(res, "conversation_id")))})
}

func (s *Server) chatID(ctx context.Context, sib *siblingConn, to string) string {
	if to == "" || to == "me" || strings.Contains(to, "@") {
		return ""
	}
	v, err := getJSONFrom(ctx, sib, "/api/v1/conversations", nil)
	if err != nil {
		return ""
	}
	for _, c := range rows(v, "conversations") {
		if idOf(c) == to || strings.EqualFold(firstNonEmpty(str(c, "name"), str(c, "title")), to) {
			return idOf(c)
		}
	}
	return ""
}

func multipartFile(field, name, ctype string, data []byte, extra map[string]string) (*bytes.Buffer, string) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range extra {
		_ = mw.WriteField(k, v)
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, name))
	h.Set("Content-Type", ctype)
	fw, _ := mw.CreatePart(h)
	_, _ = fw.Write(data)
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

func (s *Server) chatUpload(ctx context.Context, sib *siblingConn, cid, name, ctype string, data []byte) (int64, error) {
	body, ct := multipartFile("file", name, ctype, data, nil)
	var res map[string]any
	if err := sib.client.PostRaw(ctx, "/api/v1/conversations/"+url.PathEscape(cid)+"/upload", nil, body, ct, &res); err != nil {
		return 0, err
	}
	return int64(num(res, "id")), nil
}

func (s *Server) handleEventTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ev, cam, ok := s.eventFor(w, r)
	if !ok {
		return
	}
	var in struct {
		Title string `json:"title"`
		Due   string `json:"due"`
	}
	_ = decode(r, &in)
	sib, ok := s.sib(w, r, appTasks, scopeTasksWrite)
	if !ok {
		return
	}
	st := s.settingsOf(ctx, actorID(ctx))
	title, text := eventWords(ev, cam)
	body := map[string]any{"title": clip(firstNonEmpty(strings.TrimSpace(in.Title), "Look into: "+title), 200), "notes": text + "\n\n" + s.link(fmt.Sprintf("/events/%d", ev.ID)),
		"url": s.link(fmt.Sprintf("/events/%d", ev.ID)), "tags": []string{"cameras"}}
	if validDate(in.Due) && in.Due != "" {
		body["due"] = in.Due
	}
	if st.TasksList != "" {
		body["list_id"] = st.TasksList
	}
	res, err := postJSONTo(ctx, sib, "/api/v1/tasks", body)
	if err != nil {
		s.appFail(w, appTasks, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "text": "Task added in Pocket Tasks", "url": linkTo(sib, "/tasks/"+idOf(res))})
}

func (s *Server) handleEventNote(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ev, cam, ok := s.eventFor(w, r)
	if !ok {
		return
	}
	sib, ok := s.sib(w, r, appNotes, scopeNotesWrite)
	if !ok {
		return
	}
	st := s.settingsOf(ctx, actorID(ctx))
	title, text := eventWords(ev, cam)
	t, _ := time.Parse(time.RFC3339Nano, ev.StartedAt)
	var lines []string
	lines = append(lines, text, "", "- Camera: "+cam.Name, "- When: "+t.Local().Format("Monday 2 January 2006, 15:04:05"))
	if len(ev.Labels) > 0 {
		lines = append(lines, "- Seen: "+strings.Join(ev.Labels, ", "))
	}
	if ev.Weather != "" {
		lines = append(lines, "- Weather: "+ev.Weather)
	}
	lines = append(lines, "- In Pocket Cameras: "+s.link(fmt.Sprintf("/events/%d", ev.ID)))
	res, err := postJSONTo(ctx, sib, "/api/v1/notes", map[string]any{"title": clip(title, 150), "content": strings.Join(lines, "\n"), "category": st.NotesFolder})
	if err != nil {
		s.appFail(w, appNotes, err)
		return
	}
	nid := idOf(res)
	if pic, err := s.eventPicture(ev, 0); err == nil && nid != "" {
		body, ct := multipartFile("file", fmt.Sprintf("event-%d.jpg", ev.ID), "image/jpeg", pic, nil)
		var att map[string]any
		if err := sib.client.PostRaw(ctx, "/api/v1/notes/"+url.PathEscape(nid)+"/attachments", nil, body, ct, &att); err == nil {
			if md := str(att, "markdown"); md != "" {
				_, _ = postJSONTo(ctx, sib, "/api/v1/notes/"+url.PathEscape(nid)+"/append", map[string]any{"text": "\n" + md + "\n"})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "text": "Written down in Pocket Notes", "url": linkTo(sib, "/notes/"+nid)})
}

func (s *Server) handleEventPhotos(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ev, _, ok := s.eventFor(w, r)
	if !ok {
		return
	}
	var in struct {
		Frame int `json:"frame"`
	}
	_ = decode(r, &in)
	pic, err := s.eventPicture(ev, in.Frame)
	if err != nil {
		writeErr(w, http.StatusNotFound, "this event has no picture")
		return
	}
	sib, ok := s.sib(w, r, appPhotos, scopePhotosWrite)
	if !ok {
		return
	}
	t, _ := time.Parse(time.RFC3339Nano, ev.StartedAt)
	when := t.UTC().Format(time.RFC3339)
	name := fmt.Sprintf("camera-%s-%d.jpg", t.Local().Format("20060102-150405"), ev.ID)
	body, ct := multipartFile("assetData", name, "image/jpeg", pic, map[string]string{
		"deviceAssetId": fmt.Sprintf("pocket-cameras-%d-%d", ev.ID, firstPos(in.Frame, ev.Best)), "deviceId": "pocket-cameras",
		"fileCreatedAt": when, "fileModifiedAt": when, "filename": name})
	var res map[string]any
	if err := sib.client.PostRaw(ctx, "/api/v1/immich/assets", nil, body, ct, &res); err != nil {
		s.appFail(w, appPhotos, err)
		return
	}
	msg := "Kept in Pocket Photos"
	if str(res, "status") == "duplicate" {
		msg = "Already in Pocket Photos"
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "text": msg, "url": linkTo(sib, "/")})
}

func (s *Server) handleEventFiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ev, cam, ok := s.eventFor(w, r)
	if !ok {
		return
	}
	sib, ok := s.sib(w, r, appFiles, scopeFilesWrite)
	if !ok {
		return
	}
	st := s.settingsOf(ctx, actorID(ctx))
	t, _ := time.Parse(time.RFC3339Nano, ev.StartedAt)
	base := fileSafe(cam.Name) + " " + t.Local().Format("2006-01-02 15.04.05")
	folder := "/" + strings.Trim(st.FilesFolder, "/")
	put := func(name, ctype string, data []byte) (string, error) {
		var res map[string]any
		q := url.Values{"space": {"home"}, "path": {folder}, "name": {name}, "conflict": {"rename"}}
		if err := sib.client.PutRaw(ctx, "/api/v1/fs/upload", q, data, ctype, &res); err != nil {
			return "", err
		}
		return str(res, "path"), nil
	}
	n := 0
	var last string
	if pic, err := s.eventPicture(ev, 0); err == nil {
		p, err := put(base+".jpg", "image/jpeg", pic)
		if err != nil {
			s.appFail(w, appFiles, err)
			return
		}
		n, last = n+1, p
	}
	if p, err := s.eventClip(ctx, ev, cam); err == nil {
		if b, err := os.ReadFile(p); err == nil {
			if _, err := put(base+".mp4", "video/mp4", b); err == nil {
				n++
			}
		}
	}
	if n == 0 {
		writeErr(w, http.StatusNotFound, "this event has no picture or clip to keep")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "text": fmt.Sprintf("%d %s in Pocket Files → %s", n, plural(n, "file", "files"), st.FilesFolder), "url": linkTo(sib, "/?path="+url.QueryEscape(filepath.Dir(firstNonEmpty(last, folder+"/x"))))})
}

func fileSafe(v string) string {
	v = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '-'
		}
		return r
	}, strings.TrimSpace(v))
	return firstNonEmpty(clip(v, 80), "camera")
}
