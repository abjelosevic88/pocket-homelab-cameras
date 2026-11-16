# Configuration

Environment variables; every one has a default. Personal settings (modes, notifications, connections, AI) live in the UI per person — the `OPENAI_*`/`NOTIFY_*` values below are copied once into the first admin's settings.

| Variable | Default | What it does |
|---|---|---|
| `BASE_URL` | – | Where the app is (links in alerts, events and other apps, the OIDC callback) |
| `HOST` / `PORT` | `0.0.0.0` / `8080` | Where it listens |
| `DATA_DIR` | `./data` | The database, event pictures and clips (`media/<user>/events/<id>/`) and the Pocket Core token |
| `DB_PATH` | `$DATA_DIR/pocket-cameras.db` | The SQLite file |
| `RECORDINGS_DIR` | `$DATA_DIR/recordings` | Recorded video, one folder per camera, one MP4 per minute (a NAS mount works) |
| `RECORDINGS_MAX_GB` | `0` | A cap for all recordings together; the oldest go first. `0` = only each camera's keep time |
| `DETECT_FPS` | `3` | Pictures a second checked for movement per camera (and the live view's rate) |
| `ENGINE` | `true` | Run the cameras. `false` serves the pages and API only (tests, a second copy) |
| `FFMPEG` / `FFPROBE` | from `PATH` | ffmpeg for RTSP cameras, recording and clips (in the container image) |
| `AUTH_MODE` | `local` | `local` (passwords), `proxy` (a header from your reverse proxy), `none` (one user, for trying it out) |
| `SECURE_COOKIES` | `false` | Set `true` behind HTTPS |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` / `ADMIN_NAME` | – | The first account, made on an empty database |
| `OIDC_ISSUER` / `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | – | Sign in with an OpenID Connect provider (not needed with Pocket Core) |
| `NOTIFY_URL` / `NOTIFY_FORMAT` | – / `apprise` | Alerts: an Apprise URL, an ntfy topic or a JSON webhook |
| `OPENAI_API_KEY` / `OPENAI_MODEL` / `OPENAI_BASE_URL` | – | AI without Pocket Core (a model that can look at pictures, like gpt-4o-mini) |
| `CORE_URL` / `CORE_REGISTRATION_KEY` / `CORE_APP_URL` | – | Pocket Core |
| `TRUST_PROXY` | `true` | Take the client address from `X-Forwarded-For` |
| `METRICS_ENABLED` | `true` | Prometheus `/metrics` |
| `MAX_BODY_BYTES` | `67108864` | Largest request (an export being brought back) |
| `DEMO_DATA` | `false` | Four demo cameras (front door, driveway, back garden, living room) that draw their own pictures with people, couriers, cars and cats passing, and three weeks of events |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `text` | |

Connections to Pocket Alerts, Network, Movement, Travel, Parcels, Pets, Weather, Chat, Tasks, Notes, Photos, Files, Rooms and Home are made per person under Settings → Connected apps (through Pocket Core with nothing to paste, or directly with a URL and a token).

## Cameras

| Kind | Address | Notes |
|---|---|---|
| RTSP | `rtsp://IP:554/…` (the app fills it in for the common makes) | Needs ffmpeg. Add the **small stream** too: it is watched and checked, the full one is recorded. H.264 plays everywhere; H.265 recordings play in Safari and Pocket Hub |
| MJPEG | `http://IP/video.mjpg` | A stream of JPEGs; no recording, each event keeps its pictures (and a clip made from them) |
| Snapshot | `http://IP/snapshot.jpg` | Asked once or twice a second; basic and digest passwords both work |
| Demo | – | Draws its own picture |

The server reaches the cameras over your network — a container on the same box does. CPU: decoding a small stream (640×360) a few times a second is light; prefer it over the full stream for watching.

## Resource use

Each RTSP camera runs two ffmpeg processes (one reading the small stream, one copying the full stream to disk without re-encoding). Recording at 4 Mbit/s is about 43 GB a day per camera; “around events” keeps a fraction of that.
