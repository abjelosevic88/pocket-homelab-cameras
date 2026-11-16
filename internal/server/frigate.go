package server

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/abjelosevic88/pocket-homelab-cameras/internal/capture"
	"github.com/abjelosevic88/pocket-homelab-cameras/internal/store"
)

// ---- coming from Frigate ----
//
// A Frigate config.yml brings its cameras over: the streams (go2rtc
// restreams are followed back to the camera), which one is recorded and
// which one is checked, how long recordings are kept, the zones and motion
// masks, and the objects it tracks. Passwords written as
// {FRIGATE_RTSP_PASSWORD} are left for the person to fill in.

type frigateConfig struct {
	Go2RTC struct {
		Streams map[string]any `yaml:"streams"`
	} `yaml:"go2rtc"`
	Record  frigateRecord            `yaml:"record"`
	Objects frigateObjects           `yaml:"objects"`
	Cameras map[string]frigateCamera `yaml:"cameras"`
}

type frigateRecord struct {
	Enabled *bool `yaml:"enabled"`
	Retain  struct {
		Days float64 `yaml:"days"`
		Mode string  `yaml:"mode"`
	} `yaml:"retain"`
	Events struct {
		Retain struct {
			Default float64 `yaml:"default"`
		} `yaml:"retain"`
	} `yaml:"events"`
	Alerts struct {
		Retain struct {
			Days float64 `yaml:"days"`
		} `yaml:"retain"`
	} `yaml:"alerts"`
}

type frigateObjects struct {
	Track []string `yaml:"track"`
}

type frigateCamera struct {
	Enabled *bool `yaml:"enabled"`
	FFmpeg  struct {
		Inputs []struct {
			Path  string   `yaml:"path"`
			Roles []string `yaml:"roles"`
		} `yaml:"inputs"`
	} `yaml:"ffmpeg"`
	Detect struct {
		Enabled *bool `yaml:"enabled"`
		Width   int   `yaml:"width"`
		Height  int   `yaml:"height"`
	} `yaml:"detect"`
	Record  frigateRecord  `yaml:"record"`
	Objects frigateObjects `yaml:"objects"`
	Zones   map[string]struct {
		Coordinates any `yaml:"coordinates"`
	} `yaml:"zones"`
	Motion struct {
		Mask any `yaml:"mask"`
	} `yaml:"motion"`
}

func (s *Server) importFrigate(ctx context.Context, uid int64, b []byte) (map[string]any, error) {
	var cfg frigateConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, &userErr{"this is not a Pocket Cameras export or a Frigate config (" + clip(err.Error(), 120) + ")"}
	}
	if len(cfg.Cameras) == 0 {
		return nil, &userErr{"there are no cameras in this Frigate config"}
	}
	have, _ := s.store.ListCameras(ctx, uid)
	names := []string{}
	for k := range cfg.Cameras {
		names = append(names, k)
	}
	slices.Sort(names)
	added, skipped, warnings := 0, 0, []string{}
	for _, key := range names {
		fc := cfg.Cameras[key]
		name := frigateName(key)
		if slices.ContainsFunc(have, func(c store.Camera) bool { return c.Mine && strings.EqualFold(c.Name, name) }) {
			skipped++
			continue
		}
		var record, detect string
		for _, in := range fc.FFmpeg.Inputs {
			p := s.followGo2RTC(cfg, in.Path)
			if slices.Contains(in.Roles, "record") && record == "" {
				record = p
			}
			if slices.Contains(in.Roles, "detect") && detect == "" {
				detect = p
			}
		}
		if record == "" && len(fc.FFmpeg.Inputs) > 0 {
			record = s.followGo2RTC(cfg, fc.FFmpeg.Inputs[0].Path)
		}
		if record == "" {
			warnings = append(warnings, name+": no stream address")
			continue
		}
		if strings.Contains(record+detect, "{FRIGATE_") {
			warnings = append(warnings, name+": fill in the password (the config had a {FRIGATE_…} placeholder)")
		}
		c := newCamera(uid)
		c.Name, c.Kind = name, "rtsp"
		if !strings.HasPrefix(strings.ToLower(record), "rtsp") {
			c.Kind = "mjpeg"
		}
		c.MainURL = record
		if detect != record {
			c.SubURL = detect
		}
		c.Host = capture.HostOf(record)
		c.Brand = capture.GuessBrand(record)
		if fc.Enabled != nil {
			c.Enabled = *fc.Enabled
		}
		if fc.Detect.Enabled != nil {
			c.Detect = *fc.Detect.Enabled
		}
		rec := cfg.Record
		if fc.Record.Enabled != nil || fc.Record.Retain.Days > 0 {
			rec = fc.Record
		}
		switch {
		case rec.Enabled != nil && !*rec.Enabled:
			c.Record = "off"
		case rec.Retain.Mode == "all":
			c.Record = "always"
		}
		if rec.Retain.Days > 0 {
			c.RetainDays = clampInt(int(rec.Retain.Days+0.5), 1, 365)
		}
		for _, d := range []float64{rec.Alerts.Retain.Days, rec.Events.Retain.Default} {
			if d > 0 {
				c.EventDays = clampInt(int(d+0.5), 1, 3650)
				break
			}
		}
		track := fc.Objects.Track
		if len(track) == 0 {
			track = cfg.Objects.Track
		}
		if len(track) > 0 {
			c.Labels = cleanLabels(track, false)
		}
		w, h := fc.Detect.Width, fc.Detect.Height
		for zn, z := range fc.Zones {
			if pts := frigatePoints(z.Coordinates, w, h); len(pts) >= 3 {
				c.Zones = append(c.Zones, store.Zone{Name: frigateName(zn), Kind: "watch", Points: pts})
			}
		}
		for i, m := range frigateMasks(fc.Motion.Mask) {
			if pts := frigatePoints(m, w, h); len(pts) >= 3 {
				c.Zones = append(c.Zones, store.Zone{Name: fmt.Sprintf("Mask %d", i+1), Kind: "ignore", Points: pts})
			}
		}
		if err := s.store.CreateCamera(ctx, &c); err != nil {
			return nil, err
		}
		added++
	}
	if added > 0 {
		s.engine.reload(ctx)
	}
	text := fmt.Sprintf("Brought in %d %s from Frigate", added, plural(added, "camera", "cameras"))
	if skipped > 0 {
		text += fmt.Sprintf(" (%d you already have)", skipped)
	}
	return map[string]any{"cameras": added, "skipped": skipped, "warnings": warnings, "text": text}, nil
}

func frigateName(key string) string {
	words := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(key))
	for i, w := range words {
		if i == 0 && w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return clip(strings.Join(words, " "), 60)
}

// followGo2RTC turns rtsp://127.0.0.1:8554/<name> into the camera's own address.
func (s *Server) followGo2RTC(cfg frigateConfig, p string) string {
	p = strings.TrimSpace(p)
	u, err := url.Parse(p)
	if err != nil || !strings.HasPrefix(u.Scheme, "rtsp") || u.Port() != "8554" {
		return p
	}
	host := u.Hostname()
	if host != "127.0.0.1" && host != "localhost" && host != "frigate" {
		return p
	}
	name := strings.Trim(u.Path, "/")
	v, ok := cfg.Go2RTC.Streams[name]
	if !ok {
		return p
	}
	list := []string{}
	switch t := v.(type) {
	case string:
		list = append(list, t)
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok {
				list = append(list, s)
			}
		}
	}
	for _, src := range list {
		src = strings.TrimPrefix(src, "ffmpeg:")
		if i := strings.Index(src, "#"); i >= 0 {
			src = src[:i]
		}
		if strings.HasPrefix(src, "rtsp") || strings.HasPrefix(src, "http") {
			return src
		}
	}
	return p
}

func frigateMasks(v any) []any {
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		return t
	}
	return []any{v}
}

// frigatePoints reads "x1,y1,x2,y2,…" (0..1 in Frigate 0.14+, pixels before).
func frigatePoints(v any, w, h int) [][2]float64 {
	var nums []float64
	add := func(s string) {
		for _, p := range strings.Split(s, ",") {
			if f, err := strconv.ParseFloat(strings.TrimSpace(p), 64); err == nil {
				nums = append(nums, f)
			}
		}
	}
	switch t := v.(type) {
	case string:
		add(t)
	case []any:
		for _, x := range t {
			add(fmt.Sprint(x))
		}
	}
	if len(nums) < 6 || len(nums)%2 != 0 {
		return nil
	}
	pixels := slices.ContainsFunc(nums, func(f float64) bool { return f > 1 })
	if w <= 0 {
		w = 1280
	}
	if h <= 0 {
		h = 720
	}
	out := [][2]float64{}
	for i := 0; i+1 < len(nums); i += 2 {
		x, y := nums[i], nums[i+1]
		if pixels {
			x, y = x/float64(w), y/float64(h)
		}
		out = append(out, [2]float64{clamp01(x), clamp01(y)})
	}
	return out
}
