# Backup

Everything lives in `DATA_DIR`: the SQLite file `pocket-cameras.db` (WAL mode), `media/<user id>/events/<event id>/` — each event's pictures (`f01.jpg`…) and clip (`clip.mp4`) — and, unless `RECORDINGS_DIR` points elsewhere, `recordings/<camera id>/` with one MP4 per minute. Take a consistent copy of the database with `pocket-cameras backup` (writes `DATA_DIR/backups/pocket-cameras-<time>.db` with `VACUUM INTO`) or Settings → Backup & data → Download database (admin), and copy `media/` as it is. Back up that copy, not the live file. Recordings are usually left out of backups (they are big and go by themselves after their keep time); starred events keep their clip in `media/`. `DATA_DIR/core.token*` is the Pocket Core registration.

Restore: stop the app, replace `pocket-cameras.db` (remove the `-wal`/`-shm` files) and `media/`, start.

Every person can also export everything they set up and saw — their cameras (without passwords), every event with what was seen and the changes of mode — as one JSON file (Settings → Backup & data), and bring it back on any box; importing the same file twice adds nothing twice. Camera passwords are filled in again on each camera.
