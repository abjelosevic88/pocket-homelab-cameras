package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-cameras/internal/config"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// moveLinks starts Cameras behind a fake Core whose pcx_<who>_<export|import|read>
// tokens are Pocket Move links acting for alice@ or bob@.
func moveLinks(t *testing.T) *app {
	t.Helper()
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/auth/introspect" {
			io.WriteString(w, `{}`)
			return
		}
		var in struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		parts := strings.SplitN(strings.TrimPrefix(in.Token, "pcx_"), "_", 2)
		if len(parts) != 2 {
			io.WriteString(w, `{"active":false}`)
			return
		}
		scope := map[string]string{"export": core.ScopeMoveExport, "import": core.ScopeMoveImport, "read": "cameras:read"}[parts[1]]
		email := parts[0] + "@example.com"
		fmt.Fprintf(w, `{"active":true,"kind":"delegated","scope":%q,"audience":"cameras","user":{"id":1,"sub":%q,"email":%q,"name":%q,"role":"member"},"app":{"id":7,"slug":"move","name":"Pocket Move"}}`, scope, email, email, parts[0])
	}))
	t.Cleanup(coreSrv.Close)
	return newApp(t, func(c *config.Config) { c.CoreURL = coreSrv.URL })
}

func (a *app) raw(tok, method, path, ctype string, body []byte) (int, []byte) {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.url+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func (a *app) moveImport(tok, ctype string, data []byte) map[string]any {
	a.t.Helper()
	code, out := a.raw(tok, "POST", "/api/v1/import", ctype, data)
	if code != 200 {
		a.t.Fatalf("POST /api/v1/import → %d %s", code, out)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	return m
}

func TestMoveLinkScopes(t *testing.T) {
	a := moveLinks(t)
	exp, imp, read := "pcx_bob_export", "pcx_bob_import", "pcx_bob_read"
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{exp, "GET", core.PortablePath, 200},
		{exp, "GET", "/api/v1/export.json", 200},
		{exp, "GET", "/api/v1/export.csv", 200},
		{exp, "POST", "/api/v1/import", 403},
		{exp, "GET", "/api/v1/cameras", 403},
		{exp, "GET", "/api/v1/settings", 403},
		{exp, "GET", "/api/v1/backup", 403},
		{imp, "GET", core.PortablePath, 200},
		{imp, "GET", "/api/v1/export.json", 403},
		{imp, "POST", "/api/v1/cameras", 403},
		{imp, "PUT", "/api/v1/settings", 403},
		{imp, "PUT", "/api/v1/mode", 403},
		{read, "GET", core.PortablePath, 403},
		{read, "GET", "/api/v1/export.json", 403},
		{read, "POST", "/api/v1/import", 403},
	} {
		if code, body := a.raw(c.tok, c.method, c.path, "application/json", []byte("{}")); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, code, c.want, body)
		}
	}
	_, body := a.raw(exp, "GET", core.PortablePath, "", nil)
	var p core.Portable
	if err := json.Unmarshal(body, &p); err != nil || len(p.Exports) != 2 || len(p.Imports) != 2 || p.Imports[0].Kind != core.KindPocket {
		t.Fatalf("portable: %s", body)
	}
}

// Move brings Alice's cameras to Bob: each import adds once, no password
// travels, and nothing of one person shows up for the other.
func TestMoveImportsAddOnce(t *testing.T) {
	a := moveLinks(t)
	c := a.ok(a.alice, "POST", "/api/v1/cameras", map[string]any{"name": "Porch", "kind": "rtsp", "main_url": "rtsp://10.0.0.9/live", "password": "pw"})
	a.addEvent(int64(num(c, "id")), "Alice", []string{"parcel"}, "A parcel on the step.", time.Now().Add(-time.Hour))

	code, export := a.raw("pcx_alice_export", "GET", "/api/v1/export.json", "", nil)
	if code != 200 || !strings.Contains(string(export), "Porch") || !strings.Contains(string(export), "A parcel on the step.") || strings.Contains(string(export), `"password":"pw"`) {
		t.Fatalf("alice's export: %d %s", code, export)
	}
	if _, bob := a.raw("pcx_bob_export", "GET", "/api/v1/export.json", "", nil); strings.Contains(string(bob), "Porch") || strings.Contains(string(bob), "parcel on the step") {
		t.Fatalf("bob's export holds alice's cameras: %s", bob)
	}

	first := a.moveImport("pcx_bob_import", "application/json", export)
	if num(first, "cameras") != 1 || num(first, "events") != 1 {
		t.Fatalf("first import: %v", first)
	}
	second := a.moveImport("pcx_bob_import", "application/json", export)
	if num(second, "cameras")+num(second, "events")+num(second, "modes") != 0 {
		t.Fatalf("second import added: %v", second)
	}

	frigate := []byte("cameras:\n  back_yard:\n    ffmpeg:\n      inputs:\n        - path: rtsp://192.168.1.30/stream1\n          roles: [detect, record]\n")
	f1 := a.moveImport("pcx_bob_import", "application/yaml", frigate)
	f2 := a.moveImport("pcx_bob_import", "application/yaml", frigate)
	if num(f1, "cameras") != 1 || num(f2, "cameras") != 0 || num(f2, "skipped") != 1 {
		t.Fatalf("frigate imports: %v then %v", f1, f2)
	}

	if l := a.list(a.bob, "/api/v1/cameras"); len(l) != 2 {
		t.Fatalf("bob has %d cameras", len(l))
	}
	if l := a.list(a.alice, "/api/v1/cameras"); len(l) != 1 {
		t.Fatalf("alice's cameras changed: %d", len(l))
	}
}
