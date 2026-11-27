# API

Everything is under `/api/v1`, JSON in and out, with a session cookie, `Authorization: Bearer pcam_…` (Settings → API & shortcuts) or a Pocket Core link token. Errors are `{"error": "…"}`. Everything is per person: another person's camera or event is a 404 — unless its owner shared the camera with you (then you may watch it, its events and recordings, but not change them).

Times are ISO 8601 UTC; days are local `YYYY-MM-DD`.

## At a glance

| | |
|---|---|
| `GET /summary` | The mode, each camera (online, recording, last event), today's counts (person, vehicle, parcel, animal, doorbell, alerts), the last five events, storage, and one sentence — for Homepage and Pocket AI |
| `GET /home` | Counts for the sidebar |
| `GET /day/YYYY-MM-DD` | `{date, items: [{kind: event\|doorbell\|trigger\|mode, id, text, title, time, camera, labels, status, worry, url}], counts, text}` — for a journal |
| `GET /search?q=&limit=` | `{hits: [{kind: camera\|event, id, title, subtitle, path, date, labels}]}`; `q=parcel` (or person, people, vehicle, animal, delivery) finds by what was seen |
| `GET /stats?days=30` | Events per day by what they were, the hours of the week, each camera, false alarms, storage |

## For Pocket Search, Day and Hub

The same things in the shape every Pocket app answers in (Pocket Core's capabilities). A signed-in person may call them; a Pocket Core link token needs the scope and reaches nothing else with it. They only read, from what Cameras keeps: no camera is asked for a picture and no AI is called.

| | |
|---|---|
| `GET /pocket/search?q=&limit=` | `pocket:search` → `{items: [{id: "camera:3"\|"event:12", title, snippet, kind: camera\|event, date, url}]}`: cameras first, then events, newest first; an event's title is what was seen, its snippet the camera and the AI's sentence. Words without a letter or digit find nothing |
| `GET /pocket/day/YYYY-MM-DD?tz=` | `pocket:day` → `{date, summary, items: [{id: "event:12"\|"mode:4", time, title, sub, kind: person\|parcel\|vehicle\|animal\|doorbell\|alarm\|movement\|mode, note, url}], metrics}`: what `/day` has, in the person's time zone when `tz` says it. At most 200 items: on a busier day plain movement that alerted nobody is left out first, then the earliest |
| `GET /pocket/widget` | `pocket:widget` → `{line: "Away · 2 new events", metrics, items: [{id, title, detail, url}], url}`: cameras that are offline first, then the five newest events of your own cameras you have not looked at |

## The mode

| | |
|---|---|
| `GET /mode` | `{mode: home\|away\|night\|off, at, by, reason, auto_mode, auto: {mode, reason}, night_from, night_to, recent}` |
| `PUT /mode` | `{mode, reason}` and/or `{auto_mode: true}` |

## Cameras

| | |
|---|---|
| `GET /cameras`, `GET /cameras/ID` | Yours and the ones shared with you (`mine`, `owner`), with `state` (online, recording, error, something moving now), the last event, the bytes recorded; one camera also has `shares` and the recorded span |
| `POST /cameras`, `PUT /cameras/ID`, `DELETE /cameras/ID` | `{name, kind (rtsp\|mjpeg\|snapshot\|demo), brand, host, username, password, main_url, sub_url, snapshot_url, enabled, record (off\|events\|always), retain_days, event_days, detect, sensitivity (1–10), zones: [{name, kind: watch\|ignore, points: [[x, y]…]}] (0..1), labels (person, vehicle, animal, parcel), modes (home, away, night), doorbell, ai_check, room, room_ref, home_ref, notes, shares}` — with a brand and a host the addresses are filled in; passwords are never sent back, addresses come back with the password hidden (send them back as they are to keep them) |
| `POST /cameras/test` | The same body (or `{id}`) → `{ok, error, warning, main: {codec, width, height, fps, audio}, sub, picture (data: URL)}` |
| `GET /cameras/brands` | The makes and their addresses |
| `POST /cameras/discover` | `{subnet: "192.168.1.0/24"}` → cameras from Pocket Network and the scan |
| `PUT /cameras/order` | `{ids: [3, 1, 2]}` |
| `GET /cameras/ID/snapshot.jpg` | The picture now (`?download=1`) |
| `GET /cameras/ID/live.mjpg` | The live MJPEG stream (`?hd=1` the full stream, RTSP cameras) |
| `GET /cameras/ID/export.mp4?from=&to=` | Video cut from the recordings (at most 30 minutes) |
| `PUT /cameras/ID/shares` | `{shares: [{user_id, notify}]}` |
| `PUT /cameras/ID/notify` | `{notify}` — alerts of a camera shared with you, for you |
| `POST /cameras/ID/share-key` | `{on}` → `share_urls` (`/s/<key>/snapshot.jpg`, `/s/<key>/live.mjpg`, no sign-in) |
| `POST /cameras/ID/ring` | The doorbell: an event with the picture now and an alert in every mode (`{note}`) |
| `POST /cameras/ID/trigger` | An outside alarm (`{note: "Back door opened"}`): an event with the picture, alerting like movement |

## Events

| | |
|---|---|
| `GET /events` | `?camera=&label=person\|vehicle\|animal\|parcel\|doorbell\|motion&day=YYYY-MM-DD&from=&to=&hours=&unreviewed=1&starred=1&nothing=1&q=&limit=&offset=` → `{events, total, unreviewed}` |
| `GET /events/ID` | One event: `labels`, `summary` (the AI), `worry` (0–2), `frames`, `best`, `clip`, `mode`, `alerted`, `pet`, `parcel_text`, `weather`, `recorded`, `prev_id` |
| `PUT /events/ID` | `{reviewed, starred, nothing, note, labels}` — a starred event keeps its clip |
| `POST /events/review` | `{camera_id, before}` — mark seen |
| `DELETE /events/ID` | With its pictures and clip |
| `GET /events/ID/picture.jpg` | `?n=` a kept picture (default the clearest) |
| `GET /events/ID/clip.mp4` | The clip (cut from the recordings, or made from the pictures) |
| `POST /events/ID/chat` · `/task` · `/note` · `/photos` · `/files` | Send it to Pocket Chat (`{to, clip}`), Tasks (`{title, due}`), Notes, Photos (`{frame}`), Files |

## Recordings

| | |
|---|---|
| `GET /timeline?day=&camera=` | Per camera: recorded `spans`, the `pieces` (one file each) and the events; and the changes of `modes` |
| `GET /segments/ID/video` | One recorded piece (MP4, `Range` works) |

## Data

`GET /export.json`, `GET /export.csv` (every event), `POST /import` (a Pocket Cameras export or a Frigate `config.yml`), `GET /backup` (admin: the database).

Pocket Move can copy your cameras and events out and bring them in: `GET /pocket/portable` describes `export.json` and `export.csv` (link scope `move:export`) and `POST /import` (`move:import`, raw body). Imports only add; a second run of the same file adds nothing. Passwords never travel.

Health: `GET /healthz` (`{status, version, uptime, core_outbox}`), `GET /readyz`, `GET /metrics`.
