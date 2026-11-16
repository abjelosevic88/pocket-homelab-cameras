CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'member',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, setup flag); everything else is personal.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

CREATE TABLE cache (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  fetched_at TEXT NOT NULL
);


-- ---------- Pocket Cameras ----------
-- Everything belongs to the person who added the camera (user_id). Another
-- person on the box sees a camera only when its owner shares it with them
-- (camera_shares), and then only to watch. Times are UTC ISO strings.
-- Event pictures live in DATA_DIR/media/<user id>/events/<event id>/,
-- recordings in RECORDINGS_DIR/<camera id>/.

CREATE TABLE cameras (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  kind          TEXT NOT NULL DEFAULT 'rtsp',     -- rtsp | mjpeg | snapshot | demo
  brand         TEXT NOT NULL DEFAULT '',         -- reolink, hikvision… ('' = typed by hand)
  host          TEXT NOT NULL DEFAULT '',
  username      TEXT NOT NULL DEFAULT '',
  password      TEXT NOT NULL DEFAULT '',
  main_url      TEXT NOT NULL DEFAULT '',         -- the full-quality stream (recorded)
  sub_url       TEXT NOT NULL DEFAULT '',         -- the small stream (watched and checked for movement); '' = main
  snapshot_url  TEXT NOT NULL DEFAULT '',
  enabled       INTEGER NOT NULL DEFAULT 1,
  record        TEXT NOT NULL DEFAULT 'events',   -- off | events | always
  retain_days   INTEGER NOT NULL DEFAULT 7,       -- recordings
  event_days    INTEGER NOT NULL DEFAULT 30,      -- events (starred ones stay)
  detect        INTEGER NOT NULL DEFAULT 1,       -- look for movement
  sensitivity   INTEGER NOT NULL DEFAULT 5,       -- 1 (only big things) … 10 (everything)
  zones         TEXT NOT NULL DEFAULT '[]',       -- [{name, kind: watch|ignore, points: [[x,y]…] 0..1}]
  labels        TEXT NOT NULL DEFAULT '["person","vehicle","parcel"]', -- what alerts
  modes         TEXT NOT NULL DEFAULT '["away","night"]',              -- in which modes it alerts
  doorbell      INTEGER NOT NULL DEFAULT 0,       -- a doorbell: a ring always alerts
  ai_check      INTEGER NOT NULL DEFAULT 1,       -- ask the AI what is in the picture
  room          TEXT NOT NULL DEFAULT '',
  room_ref      TEXT NOT NULL DEFAULT '',         -- Pocket Rooms
  home_ref      TEXT NOT NULL DEFAULT '',         -- Pocket Home thing
  share_key     TEXT NOT NULL DEFAULT '',         -- secret address for the picture/stream ('' = none)
  position      INTEGER NOT NULL DEFAULT 0,
  notes         TEXT NOT NULL DEFAULT '',
  last_seen_at  TEXT,
  last_error    TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);
CREATE INDEX cameras_user ON cameras(user_id, position);

-- Who else may watch a camera (never change it).
CREATE TABLE camera_shares (
  camera_id  INTEGER NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  notify     INTEGER NOT NULL DEFAULT 1,          -- they get its alerts too
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (camera_id, user_id)
);
CREATE INDEX camera_shares_user ON camera_shares(user_id);

-- Something happened in front of a camera.
CREATE TABLE events (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  camera_id   INTEGER NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL DEFAULT 'motion',     -- motion | doorbell | trigger
  started_at  TEXT NOT NULL,
  ended_at    TEXT,
  score       REAL NOT NULL DEFAULT 0,            -- the most of the picture that moved, 0..1
  box         TEXT NOT NULL DEFAULT '',           -- where it moved: "x,y,w,h" 0..1
  labels      TEXT NOT NULL DEFAULT '[]',         -- person, vehicle, animal, parcel (from the AI)
  summary     TEXT NOT NULL DEFAULT '',           -- one sentence from the AI
  ai_state    TEXT NOT NULL DEFAULT '',           -- '' | pending | done | off | failed
  worry       INTEGER NOT NULL DEFAULT 0,         -- 0 nothing, 1 worth a look, 2 urgent
  frames      INTEGER NOT NULL DEFAULT 0,         -- pictures kept (f01.jpg…)
  best        INTEGER NOT NULL DEFAULT 1,         -- the clearest one
  clip        INTEGER NOT NULL DEFAULT 0,         -- clip.mp4 saved with the event
  mode        TEXT NOT NULL DEFAULT '',           -- home | away | night | off at the time
  alerted     INTEGER NOT NULL DEFAULT 0,
  reviewed    INTEGER NOT NULL DEFAULT 0,
  starred     INTEGER NOT NULL DEFAULT 0,         -- kept forever
  false_alarm INTEGER NOT NULL DEFAULT 0,         -- a false alarm (shadows, rain, the cat)
  pet         TEXT NOT NULL DEFAULT '',           -- one of your pets (Pocket Pets)
  parcel_ref  TEXT NOT NULL DEFAULT '',           -- the parcel it probably was (Pocket Parcels)
  parcel_text TEXT NOT NULL DEFAULT '',
  weather     TEXT NOT NULL DEFAULT '',           -- "wind 48 km/h, rain"
  note        TEXT NOT NULL DEFAULT '',
  source      TEXT NOT NULL DEFAULT '',           -- the app that made it (doorbell webhook…)
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX events_user ON events(user_id, started_at);
CREATE INDEX events_camera ON events(camera_id, started_at);

-- Recorded pieces of video (one file each, usually a minute).
CREATE TABLE segments (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  camera_id  INTEGER NOT NULL REFERENCES cameras(id) ON DELETE CASCADE,
  started_at TEXT NOT NULL,
  ended_at   TEXT NOT NULL,
  path       TEXT NOT NULL UNIQUE,                -- relative to RECORDINGS_DIR
  bytes      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX segments_camera ON segments(camera_id, started_at);
CREATE INDEX segments_user ON segments(user_id, started_at);

-- Home / Away / Night / Off, as it changed.
CREATE TABLE mode_log (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  mode    TEXT NOT NULL,
  reason  TEXT NOT NULL DEFAULT '',
  set_by  TEXT NOT NULL DEFAULT '',               -- you | auto | <app slug>
  at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX mode_log_user ON mode_log(user_id, at);
