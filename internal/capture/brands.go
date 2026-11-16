package capture

import (
	"net"
	"net/url"
	"strings"
)

// Brand is how one maker's cameras are reached. In the paths {host},
// {user} and {pass} are filled in; RTSP addresses get user:pass@ in front.
type Brand struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Main     string `json:"main"`     // full-quality stream
	Sub      string `json:"sub"`      // small stream
	Snapshot string `json:"snapshot"` // a still picture over HTTP ('' = none)
	Port     int    `json:"port"`
	Note     string `json:"note"`
}

// Brands are the common camera makers, with the addresses their cameras use.
var Brands = []Brand{
	{ID: "reolink", Name: "Reolink", Main: "rtsp://{host}:554/h264Preview_01_main", Sub: "rtsp://{host}:554/h264Preview_01_sub", Snapshot: "http://{host}/cgi-bin/api.cgi?cmd=Snap&channel=0&user={user}&password={pass}", Port: 554, Note: "Switch RTSP on in the Reolink app: Settings → Network → Advanced → Port settings. Newer 4K models use h265Preview_01_main."},
	{ID: "hikvision", Name: "Hikvision / Annke", Main: "rtsp://{host}:554/Streaming/Channels/101", Sub: "rtsp://{host}:554/Streaming/Channels/102", Snapshot: "http://{host}/ISAPI/Streaming/channels/101/picture", Port: 554},
	{ID: "dahua", Name: "Dahua / Amcrest / Lorex", Main: "rtsp://{host}:554/cam/realmonitor?channel=1&subtype=0", Sub: "rtsp://{host}:554/cam/realmonitor?channel=1&subtype=1", Snapshot: "http://{host}/cgi-bin/snapshot.cgi?channel=1", Port: 554},
	{ID: "tapo", Name: "TP-Link Tapo", Main: "rtsp://{host}:554/stream1", Sub: "rtsp://{host}:554/stream2", Port: 554, Note: "Make a camera account in the Tapo app first: the camera → Settings → Advanced → Camera account. Use that user name and password here."},
	{ID: "unifi", Name: "Ubiquiti UniFi Protect", Main: "", Sub: "", Port: 7441, Note: "In UniFi Protect open the camera → Settings → Advanced → RTSP, switch a quality on and paste the rtsps:// address it shows."},
	{ID: "axis", Name: "Axis", Main: "rtsp://{host}:554/axis-media/media.amp", Sub: "rtsp://{host}:554/axis-media/media.amp?resolution=640x360", Snapshot: "http://{host}/axis-cgi/jpg/image.cgi", Port: 554},
	{ID: "uniview", Name: "Uniview", Main: "rtsp://{host}:554/unicast/c1/s0/live", Sub: "rtsp://{host}:554/unicast/c1/s1/live", Port: 554},
	{ID: "foscam", Name: "Foscam", Main: "rtsp://{host}:88/videoMain", Sub: "rtsp://{host}:88/videoSub", Snapshot: "http://{host}:88/cgi-bin/CGIProxy.fcgi?cmd=snapPicture2&usr={user}&pwd={pass}", Port: 88},
	{ID: "eufy", Name: "eufy", Main: "rtsp://{host}:554/live0", Sub: "", Port: 554, Note: "Switch RTSP on for the camera in the eufy app (not every eufy camera has it)."},
	{ID: "wyze", Name: "Wyze (RTSP firmware)", Main: "rtsp://{host}:554/live", Sub: "", Port: 554},
	{ID: "imou", Name: "Imou", Main: "rtsp://{host}:554/cam/realmonitor?channel=1&subtype=0", Sub: "rtsp://{host}:554/cam/realmonitor?channel=1&subtype=1", Port: 554, Note: "The user name is admin and the password is the safety code on the camera's label."},
	{ID: "onvif", Name: "Another make (ONVIF / RTSP)", Main: "rtsp://{host}:554/", Sub: "", Port: 554, Note: "Look for “RTSP” in the camera's manual or app, and paste the address it gives."},
}

// BrandByID finds a brand.
func BrandByID(id string) (Brand, bool) {
	for _, b := range Brands {
		if b.ID == id {
			return b, true
		}
	}
	return Brand{}, false
}

// Fill makes an address from a brand template.
func Fill(tpl, host, user, pass string) string {
	if tpl == "" || host == "" {
		return ""
	}
	out := strings.ReplaceAll(tpl, "{host}", host)
	out = strings.ReplaceAll(out, "{user}", url.QueryEscape(user))
	out = strings.ReplaceAll(out, "{pass}", url.QueryEscape(pass))
	return out
}

// WithLogin puts user:pass@ into an RTSP address that has none.
func WithLogin(addr, user, pass string) string {
	if user == "" {
		return addr
	}
	u, err := url.Parse(addr)
	if err != nil || u.User != nil || !strings.HasPrefix(u.Scheme, "rtsp") {
		return addr
	}
	u.User = url.UserPassword(user, pass)
	return u.String()
}

// Redact hides the password in an address (for logs and the page).
func Redact(addr string) string {
	u, err := url.Parse(addr)
	if err != nil {
		return addr
	}
	if u.User != nil {
		if _, ok := u.User.Password(); ok {
			u.User = url.UserPassword(u.User.Username(), "•••")
		}
	}
	q := u.Query()
	for _, k := range []string{"password", "pwd", "pass"} {
		if q.Has(k) {
			q.Set(k, "•••")
		}
	}
	u.RawQuery = q.Encode()
	return strings.ReplaceAll(u.String(), "%E2%80%A2", "•")
}

// GuessBrand reads a maker from a network vendor name or a web page title.
func GuessBrand(text string) string {
	low := strings.ToLower(text)
	for _, k := range []struct{ word, id string }{
		{"reolink", "reolink"}, {"hikvision", "hikvision"}, {"annke", "hikvision"}, {"hangzhou", "hikvision"}, {"ezviz", "hikvision"},
		{"dahua", "dahua"}, {"amcrest", "dahua"}, {"lorex", "dahua"}, {"imou", "imou"}, {"tp-link", "tapo"}, {"tapo", "tapo"},
		{"ubiquiti", "unifi"}, {"unifi", "unifi"}, {"axis", "axis"}, {"uniview", "uniview"}, {"zhejiang uniview", "uniview"},
		{"foscam", "foscam"}, {"shenzhen reecam", "foscam"}, {"anker", "eufy"}, {"eufy", "eufy"}, {"wyze", "wyze"},
	} {
		if strings.Contains(low, k.word) {
			return k.id
		}
	}
	return ""
}

// HostOf is the host part of an address.
func HostOf(addr string) string {
	u, err := url.Parse(addr)
	if err != nil {
		return ""
	}
	h := u.Hostname()
	if net.ParseIP(h) == nil && h == "" {
		return ""
	}
	return h
}
