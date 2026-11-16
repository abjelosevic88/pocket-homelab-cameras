package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-cameras/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core), Cameras as
// the target: another Pocket app the person allowed in Core may act here for
// them with a pcx_ token, only within these scopes. Every route not listed
// in linkRoutes stays closed to it (403).

var linkOffers = []core.Offer{
	{Scope: "cameras:read", Name: "See your cameras", Description: "Read your cameras and how they are doing, the mode the house is in (Home, Away, Night, Off), and what the cameras saw — each event with what it was (person, vehicle, animal, parcel), the AI's sentence and its picture: for your journal, search and Pocket AI."},
	{Scope: "cameras:write", Name: "Change your cameras' mode", Description: "Switch the mode (Home, Away, Night, Off), mark events seen or keep them, add a note to an event, and ring the doorbell or raise an alarm on a camera (a Home Assistant button). Never changes a camera's settings, never deletes anything."},
}

// linkRoute is one request a Pocket Core link token may make.
type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string // "" = any linked app
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(home|summary|search|stats|mode|cameras|events|timeline|day/\d{4}-\d{2}-\d{2})$`), "cameras:read"},
	{"GET", regexp.MustCompile(`^/api/v1/(cameras|events)/\d+$`), "cameras:read"},
	{"GET", regexp.MustCompile(`^/api/v1/(cameras/\d+/snapshot\.jpg|events/\d+/picture\.jpg)$`), "cameras:read"},
	{"PUT", regexp.MustCompile(`^/api/v1/mode$`), "cameras:write"},
	{"PUT", regexp.MustCompile(`^/api/v1/events/\d+$`), "cameras:write"},
	{"POST", regexp.MustCompile(`^/api/v1/events/review$`), "cameras:write"},
	{"POST", regexp.MustCompile(`^/api/v1/cameras/\d+/(ring|trigger)$`), "cameras:write"},
}

var errLinkScope = errors.New("outside the link's scope")

// linkAllows reports whether a link token with these scopes may make this request.
func linkAllows(scopes []string, r *http.Request) bool {
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// linkUser validates a pcx_ token, checks the route against its scopes and
// maps it to the person it acts for. The second value is the acting app's slug.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, string, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, "", err
	}
	if !in.Active || !in.Delegated() || in.Audience != "cameras" || in.User == nil || in.User.Email == "" || in.App == nil || in.App.Slug == "" {
		return nil, "", http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, "", errLinkScope
	}
	u, err := s.coreIdentityUser(ctx, in)
	return u, in.App.Slug, err
}

const linkAppKey ctxKey = 2

// linkApp is the acting app's slug when the request came in on a link token.
func linkApp(ctx context.Context) string {
	a, _ := ctx.Value(linkAppKey).(string)
	return a
}

// withLinkApp marks a context as acting for another app (tests, and the auth middleware).
func withLinkApp(ctx context.Context, slug string) context.Context {
	return context.WithValue(ctx, linkAppKey, slug)
}
