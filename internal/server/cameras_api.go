package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-cameras/internal/capture"
	"github.com/abjelosevic88/pocket-homelab-cameras/internal/store"
)

// ---- cameras ----

var (
	cameraKinds = []string{"rtsp", "mjpeg", "snapshot", "demo"}
	recordModes = []string{"off", "events", "always"}
	alertModes  = []string{"home", "away", "night"}
)

// camOut is a camera as the page sees it: secrets hidden, how it is doing now.
func (s *Server) camOut(ctx context.Context, c store.Camera, use map[int64]int64) map[string]any {
	st := s.engine.state(c.ID)
	out := map[string]any{
		"id": c.ID, "name": c.Name, "kind": c.Kind, "brand": c.Brand, "host": c.Host, "username": c.Username, "has_password": c.Password != "",
		"main_url": capture.Redact(c.MainURL), "sub_url": capture.Redact(c.SubURL), "snapshot_url": capture.Redact(c.SnapshotURL),
		"enabled": c.Enabled, "record": c.Record, "retain_days": c.RetainDays, "event_days": c.EventDays, "detect": c.Detect, "sensitivity": c.Sensitivity,
		"zones": c.Zones, "labels": c.Labels, "modes": c.Modes, "doorbell": c.Doorbell, "ai_check": c.AICheck,
		"room": c.Room, "room_ref": c.RoomRef, "home_ref": c.HomeRef, "position": c.Position, "notes": c.Notes,
		"last_seen_at": c.LastSeenAt, "last_error": c.LastError, "created_at": c.CreatedAt, "updated_at": c.UpdatedAt,
		"mine": c.Mine, "owner": c.Owner, "state": st, "bytes": use[c.ID], "can_record": c.Kind == "rtsp",
	}
	if c.Mine && c.ShareKey != "" {
		out["share_urls"] = s.shareURLs(c.ShareKey)
	}
	if !c.Mine {
		out["notify"] = s.store.ShareNotify(ctx, actorID(ctx), c.ID)
	}
	if ev, err := s.store.LastEvent(ctx, c.ID); err == nil {
		out["last_event"] = map[string]any{"id": ev.ID, "started_at": ev.StartedAt, "labels": ev.Labels, "summary": ev.Summary, "kind": ev.Kind, "pet": ev.Pet}
	}
	return out
}

func (s *Server) shareURLs(key string) map[string]string {
	return map[string]string{"snapshot": s.link("/s/" + key + "/snapshot.jpg"), "live": s.link("/s/" + key + "/live.mjpg")}
}

func (s *Server) handleCameras(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := s.store.ListCameras(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "cameras")
		return
	}
	use, total, _ := s.store.StorageUse(ctx)
	out := []map[string]any{}
	for _, c := range list {
		out = append(out, s.camOut(ctx, c, use))
	}
	writeJSON(w, http.StatusOK, map[string]any{"cameras": out, "ffmpeg": capture.FFmpeg() != "", "storage": total, "storage_cap": int64(s.cfg.MaxStorageGB * (1 << 30))})
}

func (s *Server) handleGetCamera(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.store.GetCamera(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "camera")
		return
	}
	use, _, _ := s.store.StorageUse(ctx)
	out := s.camOut(ctx, *c, use)
	from, to := s.store.RecordedSpan(ctx, c.ID)
	out["recorded_from"], out["recorded_to"] = from, to
	if c.Mine {
		shares, _ := s.store.ListShares(ctx, c.ID)
		out["shares"] = shares
	}
	writeJSON(w, http.StatusOK, out)
}

// cameraIn is what a person sends to add or change a camera.
type cameraIn struct {
	Name        *string        `json:"name"`
	Kind        *string        `json:"kind"`
	Brand       *string        `json:"brand"`
	Host        *string        `json:"host"`
	Username    *string        `json:"username"`
	Password    *string        `json:"password"`
	MainURL     *string        `json:"main_url"`
	SubURL      *string        `json:"sub_url"`
	SnapshotURL *string        `json:"snapshot_url"`
	Enabled     *bool          `json:"enabled"`
	Record      *string        `json:"record"`
	RetainDays  *int           `json:"retain_days"`
	EventDays   *int           `json:"event_days"`
	Detect      *bool          `json:"detect"`
	Sensitivity *int           `json:"sensitivity"`
	Zones       *[]store.Zone  `json:"zones"`
	Labels      *[]string      `json:"labels"`
	Modes       *[]string      `json:"modes"`
	Doorbell    *bool          `json:"doorbell"`
	AICheck     *bool          `json:"ai_check"`
	Room        *string        `json:"room"`
	RoomRef     *string        `json:"room_ref"`
	HomeRef     *string        `json:"home_ref"`
	Notes       *string        `json:"notes"`
	Shares      *[]store.Share `json:"shares"`
}

// keepSecret takes a new value unless it is the masked old one.
func keepSecret(dst *string, v *string) {
	if v == nil || strings.Contains(*v, "•") {
		return
	}
	*dst = strings.TrimSpace(*v)
}

// apply puts the input on c, checking it; the error is for the person.
func (in cameraIn) apply(c *store.Camera) error {
	if in.Name != nil {
		c.Name = cleanText(*in.Name, 60)
	}
	if c.Name == "" {
		return &userErr{"give the camera a name"}
	}
	if in.Kind != nil {
		if !slices.Contains(cameraKinds, *in.Kind) {
			return &userErr{"a camera is rtsp, mjpeg, snapshot or demo"}
		}
		c.Kind = *in.Kind
	}
	if in.Brand != nil {
		c.Brand = cleanText(*in.Brand, 30)
	}
	if in.Host != nil {
		c.Host = cleanText(*in.Host, 120)
	}
	if in.Username != nil {
		c.Username = cleanText(*in.Username, 120)
	}
	keepSecret(&c.Password, in.Password)
	keepSecret(&c.MainURL, in.MainURL)
	keepSecret(&c.SubURL, in.SubURL)
	keepSecret(&c.SnapshotURL, in.SnapshotURL)
	// a brand and an address fill in what is missing
	if b, ok := capture.BrandByID(c.Brand); ok && c.Host != "" {
		if c.MainURL == "" {
			c.MainURL = capture.Fill(b.Main, c.Host, c.Username, c.Password)
		}
		if c.SubURL == "" && in.SubURL == nil {
			c.SubURL = capture.Fill(b.Sub, c.Host, c.Username, c.Password)
		}
		if c.SnapshotURL == "" && in.SnapshotURL == nil {
			c.SnapshotURL = capture.Fill(b.Snapshot, c.Host, c.Username, c.Password)
		}
	}
	if c.Kind == "demo" {
		scene := strings.TrimPrefix(c.MainURL, "demo:")
		if !slices.Contains(capture.DemoScenes, scene) {
			scene = "door"
		}
		c.MainURL = "demo:" + scene
	} else {
		if c.MainURL == "" && !(c.Kind == "snapshot" && c.SnapshotURL != "") {
			return &userErr{"add the camera's address (or choose its make and type its IP address)"}
		}
		for _, u := range []string{c.MainURL, c.SubURL, c.SnapshotURL} {
			if u == "" {
				continue
			}
			low := strings.ToLower(u)
			if !(strings.HasPrefix(low, "rtsp://") || strings.HasPrefix(low, "rtsps://") || strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "rtmp://")) {
				return &userErr{"a camera address starts with rtsp://, rtsps:// or http://"}
			}
		}
		if c.Host == "" {
			c.Host = capture.HostOf(firstNonEmpty(c.MainURL, c.SnapshotURL))
		}
	}
	if in.Enabled != nil {
		c.Enabled = *in.Enabled
	}
	if in.Record != nil {
		if !slices.Contains(recordModes, *in.Record) {
			return &userErr{"recording is off, events or always"}
		}
		c.Record = *in.Record
	}
	if in.RetainDays != nil {
		c.RetainDays = clampInt(*in.RetainDays, 1, 365)
	}
	if in.EventDays != nil {
		c.EventDays = clampInt(*in.EventDays, 1, 3650)
	}
	if in.Detect != nil {
		c.Detect = *in.Detect
	}
	if in.Sensitivity != nil {
		c.Sensitivity = clampInt(*in.Sensitivity, 1, 10)
	}
	if in.Zones != nil {
		zs := []store.Zone{}
		for i, z := range *in.Zones {
			if i >= 20 {
				break
			}
			if z.Kind != "ignore" {
				z.Kind = "watch"
			}
			z.Name = cleanText(z.Name, 40)
			pts := [][2]float64{}
			for j, p := range z.Points {
				if j >= 40 {
					break
				}
				pts = append(pts, [2]float64{clamp01(p[0]), clamp01(p[1])})
			}
			if len(pts) < 3 {
				continue
			}
			z.Points = pts
			zs = append(zs, z)
		}
		c.Zones = zs
	}
	if in.Labels != nil {
		c.Labels = []string{}
		for _, l := range *in.Labels {
			if slices.Contains(Labels, l) && !slices.Contains(c.Labels, l) {
				c.Labels = append(c.Labels, l)
			}
		}
	}
	if in.Modes != nil {
		c.Modes = []string{}
		for _, m := range *in.Modes {
			if slices.Contains(alertModes, m) && !slices.Contains(c.Modes, m) {
				c.Modes = append(c.Modes, m)
			}
		}
	}
	if in.Doorbell != nil {
		c.Doorbell = *in.Doorbell
	}
	if in.AICheck != nil {
		c.AICheck = *in.AICheck
	}
	if in.Room != nil {
		c.Room = cleanText(*in.Room, 60)
	}
	if in.RoomRef != nil {
		c.RoomRef = cleanText(*in.RoomRef, 80)
	}
	if in.HomeRef != nil {
		c.HomeRef = cleanText(*in.HomeRef, 80)
	}
	if in.Notes != nil {
		c.Notes = cleanText(*in.Notes, 2000)
	}
	return nil
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func newCamera(uid int64) store.Camera {
	return store.Camera{UserID: uid, Kind: "rtsp", Enabled: true, Record: "events", RetainDays: 7, EventDays: 30, Detect: true, Sensitivity: 5,
		Zones: []store.Zone{}, Labels: []string{"person", "vehicle", "parcel"}, Modes: []string{"away", "night"}, AICheck: true}
}

func (s *Server) handleCreateCamera(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if linkApp(ctx) != "" {
		writeErr(w, http.StatusForbidden, "apps cannot add cameras")
		return
	}
	var in cameraIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	c := newCamera(actorID(ctx))
	if err := in.apply(&c); err != nil {
		s.userFail(w, err, "camera")
		return
	}
	if err := s.store.CreateCamera(ctx, &c); err != nil {
		s.fail(w, err, "camera")
		return
	}
	if in.Shares != nil {
		_ = s.store.SetShares(ctx, c.UserID, c.ID, *in.Shares)
	}
	s.engine.reload(ctx)
	s.publish(ctx, c.UserID, "cameras.camera_added", c.Name, map[string]any{"camera_id": c.ID, "camera": c.Name})
	c.Mine = true
	writeJSON(w, http.StatusCreated, s.camOut(ctx, c, nil))
}

func (s *Server) handleUpdateCamera(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.store.GetOwnCamera(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "camera")
		return
	}
	var in cameraIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := in.apply(c); err != nil {
		s.userFail(w, err, "camera")
		return
	}
	if err := s.store.UpdateCamera(ctx, c); err != nil {
		s.fail(w, err, "camera")
		return
	}
	if in.Shares != nil {
		_ = s.store.SetShares(ctx, c.UserID, c.ID, *in.Shares)
	}
	s.engine.reload(ctx)
	writeJSON(w, http.StatusOK, s.camOut(ctx, *c, nil))
}

func (s *Server) handleDeleteCamera(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	c, err := s.store.GetOwnCamera(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "camera")
		return
	}
	evs, _, _ := s.store.ListEvents(ctx, uid, store.EventFilter{CameraID: c.ID, Nothing: true, Limit: 500})
	if err := s.store.DeleteCamera(ctx, uid, c.ID); err != nil {
		s.fail(w, err, "camera")
		return
	}
	s.engine.reload(ctx)
	go func() {
		for _, e := range evs {
			s.dropEventFiles(e.UserID, e.ID)
		}
		s.dropRecordings(c.ID)
	}()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleOrderCameras(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.store.OrderCameras(r.Context(), actorID(r.Context()), in.IDs); err != nil {
		s.fail(w, err, "order")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleBrands(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"brands": capture.Brands, "scenes": capture.DemoScenes, "ffmpeg": capture.FFmpeg() != ""})
}

// ---- trying a camera before saving it ----

// grabFrame reads one picture from a camera.
func (s *Server) grabFrame(ctx context.Context, c store.Camera) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var pic []byte
	var once sync.Once
	src := s.source(c)
	if fs, ok := src.(capture.FFmpegSource); ok {
		fs.FPS, fs.Width = 2, 960
		src = fs
	}
	err := src.Run(ctx, func(b []byte) { once.Do(func() { pic = b; cancel() }) })
	if pic != nil {
		return pic, nil
	}
	if err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		err = errors.New("no picture from the camera within 20 seconds")
	}
	return nil, err
}

func (s *Server) handleTestCamera(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		cameraIn
		ID int64 `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	c := newCamera(actorID(ctx))
	if in.ID > 0 {
		old, err := s.store.GetOwnCamera(ctx, actorID(ctx), in.ID)
		if err != nil {
			s.fail(w, err, "camera")
			return
		}
		c = *old
	}
	if in.Name == nil && c.Name == "" {
		n := "test"
		in.Name = &n
	}
	if err := in.cameraIn.apply(&c); err != nil {
		s.userFail(w, err, "camera")
		return
	}
	out := map[string]any{"ok": false, "main_url": capture.Redact(c.MainURL), "sub_url": capture.Redact(c.SubURL), "snapshot_url": capture.Redact(c.SnapshotURL)}
	if c.Kind == "rtsp" {
		if capture.FFmpeg() == "" {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": capture.ErrNoFFmpeg.Error()})
			return
		}
		info, err := capture.Probe(ctx, capture.WithLogin(c.MainURL, c.Username, c.Password))
		if err != nil {
			out["error"] = "Full-quality stream: " + err.Error()
			writeJSON(w, http.StatusOK, out)
			return
		}
		out["main"] = info
		if strings.Contains(info.Codec, "hevc") || strings.Contains(info.Codec, "265") {
			out["warning"] = "This camera sends H.265: recordings play in Safari and Pocket Hub, but not in Chrome or Firefox. If the camera can, switch its main stream to H.264."
		}
		if c.SubURL != "" && c.SubURL != c.MainURL {
			if sub, err := capture.Probe(ctx, capture.WithLogin(c.SubURL, c.Username, c.Password)); err == nil {
				out["sub"] = sub
			} else {
				out["sub_error"] = err.Error()
			}
		}
	}
	pic, err := s.grabFrame(ctx, c)
	if err != nil {
		out["error"] = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["ok"] = true
	out["picture"] = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(pic)
	writeJSON(w, http.StatusOK, out)
}

// ---- finding cameras ----

type found struct {
	IP     string `json:"ip"`
	Name   string `json:"name"`
	Vendor string `json:"vendor"`
	Brand  string `json:"brand"`
	RTSP   bool   `json:"rtsp"`
	Web    bool   `json:"web"`
	From   string `json:"from"` // network | scan
	Online bool   `json:"online"`
	Added  bool   `json:"added"`
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		Subnet string `json:"subnet"`
	}
	_ = decode(r, &in)
	out := []found{}
	seen := map[string]int{}
	add := func(f found) {
		if i, ok := seen[f.IP]; ok {
			o := &out[i]
			o.RTSP, o.Web = o.RTSP || f.RTSP, o.Web || f.Web
			o.Brand, o.Name, o.Vendor = firstNonEmpty(o.Brand, f.Brand), firstNonEmpty(o.Name, f.Name), firstNonEmpty(o.Vendor, f.Vendor)
			return
		}
		seen[f.IP] = len(out)
		out = append(out, f)
	}
	note := ""
	if list, err := s.networkCameras(ctx, uid); err == nil {
		for _, f := range list {
			add(f)
		}
	} else if !isNotConnected(err) {
		note = "Pocket Network: " + err.Error()
	}
	subnet := strings.TrimSpace(in.Subnet)
	if subnet != "" {
		list, err := scanSubnet(ctx, subnet)
		if err != nil {
			s.userFail(w, err, "scan")
			return
		}
		for _, f := range list {
			add(f)
		}
	}
	cams, _ := s.store.ListCameras(ctx, uid)
	for i := range out {
		for _, c := range cams {
			if c.Host == out[i].IP || capture.HostOf(c.MainURL) == out[i].IP {
				out[i].Added = true
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"found": out, "note": note, "subnet": subnet})
}

// scanSubnet looks for RTSP (554) and web (80) on every address of a /24.
func scanSubnet(ctx context.Context, subnet string) ([]found, error) {
	if !strings.Contains(subnet, "/") {
		subnet += "/24"
	}
	_, ipnet, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil, &userErr{"write the network like 192.168.1.0/24"}
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 || ones < 22 {
		return nil, &userErr{"scan at most a /22 (1024 addresses), like 192.168.1.0/24"}
	}
	base := ipnet.IP.To4()
	n := 1 << (32 - ones)
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	var mu sync.Mutex
	out := []found{}
	sem := make(chan struct{}, 96)
	var wg sync.WaitGroup
	for i := 1; i < n-1; i++ {
		ip := make(net.IP, 4)
		v := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
		v += uint32(i)
		ip[0], ip[1], ip[2], ip[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
		wg.Add(1)
		sem <- struct{}{}
		go func(ip string) {
			defer wg.Done()
			defer func() { <-sem }()
			if !portOpen(ctx, ip, 554) && !portOpen(ctx, ip, 8554) {
				return
			}
			f := found{IP: ip, RTSP: true, From: "scan", Online: true}
			if portOpen(ctx, ip, 80) {
				f.Web = true
				if title := webTitle(ctx, ip); title != "" {
					f.Name, f.Brand = title, capture.GuessBrand(title)
				}
			}
			mu.Lock()
			out = append(out, f)
			mu.Unlock()
		}(ip.String())
	}
	wg.Wait()
	slices.SortFunc(out, func(a, b found) int { return strings.Compare(fmt.Sprintf("%15s", a.IP), fmt.Sprintf("%15s", b.IP)) })
	return out, nil
}

func portOpen(ctx context.Context, ip string, port int) bool {
	d := net.Dialer{Timeout: 700 * time.Millisecond}
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, fmt.Sprint(port)))
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// webTitle is a camera's web page title and server name (they often say the make).
func webTitle(ctx context.Context, ip string) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+ip+"/", nil)
	res, err := (&http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}).Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	b := make([]byte, 16<<10)
	n, _ := res.Body.Read(b)
	page := string(b[:n])
	title := ""
	if i := strings.Index(strings.ToLower(page), "<title>"); i >= 0 {
		rest := page[i+7:]
		if j := strings.Index(strings.ToLower(rest), "</title>"); j >= 0 {
			title = strings.TrimSpace(rest[:j])
		}
	}
	return strings.TrimSpace(clip(joinNonEmpty(" · ", title, res.Header.Get("Server")), 80))
}

// ---- sharing ----

func (s *Server) handleSetShares(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.store.GetOwnCamera(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "camera")
		return
	}
	var in struct {
		Shares []store.Share `json:"shares"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.store.SetShares(ctx, c.UserID, c.ID, in.Shares); err != nil {
		s.fail(w, err, "shares")
		return
	}
	list, _ := s.store.ListShares(ctx, c.ID)
	writeJSON(w, http.StatusOK, map[string]any{"shares": list})
}

// handleShareNotify lets someone a camera was shared with switch its alerts for themselves.
func (s *Server) handleShareNotify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Notify bool `json:"notify"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.store.SetShareNotify(ctx, actorID(ctx), idParam(r, "id"), in.Notify); err != nil {
		s.fail(w, err, "share")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notify": in.Notify})
}

// handleShareKey switches a camera's secret picture address on (new) or off.
func (s *Server) handleShareKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.store.GetOwnCamera(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "camera")
		return
	}
	var in struct {
		On bool `json:"on"`
	}
	_ = decode(r, &in)
	c.ShareKey = ""
	if in.On {
		c.ShareKey = randomHex(20)
	}
	if err := s.store.UpdateCamera(ctx, c); err != nil {
		s.fail(w, err, "camera")
		return
	}
	out := map[string]any{"on": in.On}
	if in.On {
		out["share_urls"] = s.shareURLs(c.ShareKey)
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- pictures and live ----

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.store.GetCamera(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "camera")
		return
	}
	s.serveSnapshot(w, r, c)
}

func (s *Server) serveSnapshot(w http.ResponseWriter, r *http.Request, c *store.Camera) {
	pic, at := s.engine.latest(c.ID)
	if pic == nil || time.Since(at) > 30*time.Second {
		if !c.Enabled && !queryBool(r, "fresh") {
			writeErr(w, http.StatusConflict, "this camera is switched off")
			return
		}
		b, err := s.grabFrame(r.Context(), *c)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		pic, at = b, time.Now()
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Last-Modified", at.UTC().Format(http.TimeFormat))
	if queryBool(r, "download") {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s %s.jpg"`, fileSafe(c.Name), at.Local().Format("2006-01-02 15.04.05")))
	}
	_, _ = w.Write(pic)
}

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.store.GetCamera(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "camera")
		return
	}
	s.serveLive(w, r, c)
}

// serveLive streams pictures as MJPEG: the running camera's pictures, or
// (hd=1, RTSP cameras) the full-quality stream decoded for this viewer.
func (s *Server) serveLive(w http.ResponseWriter, r *http.Request, c *store.Camera) {
	ctx := r.Context()
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming is not possible here")
		return
	}
	limit := 30 * time.Minute
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	write := func(b []byte) error {
		if _, err := fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(b)); err != nil {
			return err
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
		if _, err := w.Write([]byte("\r\n")); err != nil {
			return err
		}
		fl.Flush()
		return nil
	}
	head := func() {
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
	}
	if queryBool(r, "hd") && c.Kind == "rtsp" {
		head()
		_ = capture.FFmpegSource{URL: capture.WithLogin(c.MainURL, c.Username, c.Password), FPS: 10, Width: 1280}.Run(ctx, func(b []byte) {
			if write(b) != nil {
				cancel()
			}
		})
		return
	}
	ch, stop, ok := s.engine.subscribe(c.ID)
	if !ok {
		writeErr(w, http.StatusConflict, "this camera is switched off")
		return
	}
	defer stop()
	head()
	if pic, _ := s.engine.latest(c.ID); pic != nil {
		if write(pic) != nil {
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-ch:
			if write(b) != nil {
				return
			}
		}
	}
}

// the secret addresses: a picture and the live stream, no sign-in (for
// Home Assistant, a wall tablet, Homepage)
func (s *Server) handlePublicSnapshot(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.CameraByShareKey(r.Context(), chiParam(r, "key"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.serveSnapshot(w, r, c)
}

func (s *Server) handlePublicLive(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.CameraByShareKey(r.Context(), chiParam(r, "key"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.serveLive(w, r, c)
}
