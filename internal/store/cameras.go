package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// A camera is its owner's. Someone it is shared with may watch it (live,
// events, recordings) but never change it. visibleCams is the SQL for "the
// cameras this person may watch"; it takes the user id twice.
const visibleCams = `(SELECT id FROM cameras WHERE user_id = ? UNION SELECT camera_id FROM camera_shares WHERE user_id = ?)`

const cameraCols = `c.id, c.user_id, c.name, c.kind, c.brand, c.host, c.username, c.password, c.main_url, c.sub_url, c.snapshot_url,
	c.enabled, c.record, c.retain_days, c.event_days, c.detect, c.sensitivity, c.zones, c.labels, c.modes, c.doorbell, c.ai_check,
	c.room, c.room_ref, c.home_ref, c.share_key, c.position, c.notes, c.last_seen_at, c.last_error, c.created_at, c.updated_at,
	COALESCE(u.name, u.email, '')`

func scanCamera(row interface{ Scan(...any) error }) (*Camera, error) {
	var c Camera
	var zones, labels, modes string
	var enabled, detect, doorbell, ai int
	if err := row.Scan(&c.ID, &c.UserID, &c.Name, &c.Kind, &c.Brand, &c.Host, &c.Username, &c.Password, &c.MainURL, &c.SubURL, &c.SnapshotURL,
		&enabled, &c.Record, &c.RetainDays, &c.EventDays, &detect, &c.Sensitivity, &zones, &labels, &modes, &doorbell, &ai,
		&c.Room, &c.RoomRef, &c.HomeRef, &c.ShareKey, &c.Position, &c.Notes, &c.LastSeenAt, &c.LastError, &c.CreatedAt, &c.UpdatedAt, &c.Owner); err != nil {
		return nil, notFound(err)
	}
	c.Enabled, c.Detect, c.Doorbell, c.AICheck = enabled == 1, detect == 1, doorbell == 1, ai == 1
	_ = json.Unmarshal([]byte(zones), &c.Zones)
	_ = json.Unmarshal([]byte(labels), &c.Labels)
	_ = json.Unmarshal([]byte(modes), &c.Modes)
	if c.Zones == nil {
		c.Zones = []Zone{}
	}
	if c.Labels == nil {
		c.Labels = []string{}
	}
	if c.Modes == nil {
		c.Modes = []string{}
	}
	return &c, nil
}

func (s *Store) cameras(ctx context.Context, where string, args ...any) ([]Camera, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+cameraCols+` FROM cameras c LEFT JOIN users u ON u.id = c.user_id WHERE `+where+` ORDER BY c.position, c.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Camera{}
	for rows.Next() {
		c, err := scanCamera(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// ListCameras is every camera a person may watch: their own first, then the
// ones shared with them.
func (s *Store) ListCameras(ctx context.Context, uid int64) ([]Camera, error) {
	list, err := s.cameras(ctx, `c.id IN `+visibleCams, uid, uid)
	if err != nil {
		return nil, err
	}
	mine, shared := []Camera{}, []Camera{}
	for _, c := range list {
		c.Mine = c.UserID == uid
		if c.Mine {
			c.Owner = ""
			mine = append(mine, c)
		} else {
			c.Password, c.ShareKey = "", ""
			shared = append(shared, c)
		}
	}
	return append(mine, shared...), nil
}

// GetCamera is one camera the person may watch.
func (s *Store) GetCamera(ctx context.Context, uid, id int64) (*Camera, error) {
	list, err := s.cameras(ctx, `c.id = ? AND c.id IN `+visibleCams, id, uid, uid)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	c := list[0]
	c.Mine = c.UserID == uid
	if !c.Mine {
		c.Password, c.ShareKey = "", ""
	} else {
		c.Owner = ""
	}
	return &c, nil
}

// GetOwnCamera is one of the person's own cameras (to change it).
func (s *Store) GetOwnCamera(ctx context.Context, uid, id int64) (*Camera, error) {
	c, err := s.GetCamera(ctx, uid, id)
	if err != nil {
		return nil, err
	}
	if !c.Mine {
		return nil, ErrNotFound
	}
	return c, nil
}

// CameraByID is any camera (the background work only).
func (s *Store) CameraByID(ctx context.Context, id int64) (*Camera, error) {
	list, err := s.cameras(ctx, `c.id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	c := list[0]
	c.Mine = true
	return &c, nil
}

// CameraByShareKey finds the camera behind a secret picture address.
func (s *Store) CameraByShareKey(ctx context.Context, key string) (*Camera, error) {
	if len(key) < 16 {
		return nil, ErrNotFound
	}
	list, err := s.cameras(ctx, `c.share_key = ?`, key)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

// AllCameras is every camera on the box (the background work only).
func (s *Store) AllCameras(ctx context.Context) ([]Camera, error) {
	return s.cameras(ctx, `1 = 1`)
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// CreateCamera adds a camera at the end of the person's list.
func (s *Store) CreateCamera(ctx context.Context, c *Camera) error {
	var pos int
	_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) + 1 FROM cameras WHERE user_id = ?`, c.UserID).Scan(&pos)
	c.Position = pos
	res, err := s.DB.ExecContext(ctx, `INSERT INTO cameras (user_id, name, kind, brand, host, username, password, main_url, sub_url, snapshot_url,
		enabled, record, retain_days, event_days, detect, sensitivity, zones, labels, modes, doorbell, ai_check, room, room_ref, home_ref, share_key, position, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.UserID, c.Name, c.Kind, c.Brand, c.Host, c.Username, c.Password, c.MainURL, c.SubURL, c.SnapshotURL,
		boolInt(c.Enabled), c.Record, c.RetainDays, c.EventDays, boolInt(c.Detect), c.Sensitivity, jsonText(c.Zones), jsonText(c.Labels), jsonText(c.Modes),
		boolInt(c.Doorbell), boolInt(c.AICheck), c.Room, c.RoomRef, c.HomeRef, c.ShareKey, c.Position, c.Notes)
	if err != nil {
		return err
	}
	c.ID, _ = res.LastInsertId()
	c.CreatedAt = Now()
	return nil
}

// UpdateCamera saves one of the person's own cameras.
func (s *Store) UpdateCamera(ctx context.Context, c *Camera) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE cameras SET name = ?, kind = ?, brand = ?, host = ?, username = ?, password = ?, main_url = ?, sub_url = ?, snapshot_url = ?,
		enabled = ?, record = ?, retain_days = ?, event_days = ?, detect = ?, sensitivity = ?, zones = ?, labels = ?, modes = ?, doorbell = ?, ai_check = ?,
		room = ?, room_ref = ?, home_ref = ?, share_key = ?, notes = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		c.Name, c.Kind, c.Brand, c.Host, c.Username, c.Password, c.MainURL, c.SubURL, c.SnapshotURL,
		boolInt(c.Enabled), c.Record, c.RetainDays, c.EventDays, boolInt(c.Detect), c.Sensitivity, jsonText(c.Zones), jsonText(c.Labels), jsonText(c.Modes),
		boolInt(c.Doorbell), boolInt(c.AICheck), c.Room, c.RoomRef, c.HomeRef, c.ShareKey, c.Notes, Now(), c.ID, c.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteCamera removes one of the person's cameras with its events and recordings (rows).
func (s *Store) DeleteCamera(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM cameras WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// OrderCameras puts the person's cameras in this order.
func (s *Store) OrderCameras(ctx context.Context, uid int64, ids []int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE cameras SET position = ? WHERE id = ? AND user_id = ?`, i+1, id, uid); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// SetCameraSeen notes the last picture (or the last error) of a camera.
func (s *Store) SetCameraSeen(ctx context.Context, id int64, at time.Time, errText string) error {
	if at.IsZero() {
		_, err := s.DB.ExecContext(ctx, `UPDATE cameras SET last_error = ? WHERE id = ?`, errText, id)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE cameras SET last_seen_at = ?, last_error = ? WHERE id = ?`, at.UTC().Format(time.RFC3339Nano), errText, id)
	return err
}

// ---- sharing ----

// ListShares is who else may watch one of the person's cameras.
func (s *Store) ListShares(ctx context.Context, cameraID int64) ([]Share, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT sh.camera_id, sh.user_id, u.name, u.email, sh.notify FROM camera_shares sh JOIN users u ON u.id = sh.user_id WHERE sh.camera_id = ? ORDER BY u.name, u.email`, cameraID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		var sh Share
		var n int
		if err := rows.Scan(&sh.CameraID, &sh.UserID, &sh.Name, &sh.Email, &n); err != nil {
			return nil, err
		}
		sh.Notify = n == 1
		out = append(out, sh)
	}
	return out, rows.Err()
}

// SetShares replaces who may watch a camera (the owner is never in the list).
func (s *Store) SetShares(ctx context.Context, owner, cameraID int64, shares []Share) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM camera_shares WHERE camera_id = ?`, cameraID); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, sh := range shares {
		if sh.UserID == owner || sh.UserID <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO camera_shares (camera_id, user_id, notify) SELECT ?, id, ? FROM users WHERE id = ?`, cameraID, boolInt(sh.Notify), sh.UserID); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// SetShareNotify lets a person who was shared a camera switch its alerts on or off for themselves.
func (s *Store) SetShareNotify(ctx context.Context, uid, cameraID int64, on bool) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE camera_shares SET notify = ? WHERE camera_id = ? AND user_id = ?`, boolInt(on), cameraID, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ShareNotify says whether a person who was shared a camera wants its alerts.
func (s *Store) ShareNotify(ctx context.Context, uid, cameraID int64) bool {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT notify FROM camera_shares WHERE camera_id = ? AND user_id = ?`, cameraID, uid).Scan(&n)
	return n == 1
}

// ---- events ----

const eventCols = `e.id, e.user_id, e.camera_id, COALESCE(c.name, ''), e.kind, e.started_at, e.ended_at, e.score, e.box, e.labels, e.summary, e.ai_state, e.worry,
	e.frames, e.best, e.clip, e.mode, e.alerted, e.reviewed, e.starred, e.false_alarm, e.pet, e.parcel_ref, e.parcel_text, e.weather, e.note, e.source, e.created_at`

func scanEvent(row interface{ Scan(...any) error }) (*Event, error) {
	var e Event
	var labels string
	var clip, alerted, reviewed, starred, nothing int
	if err := row.Scan(&e.ID, &e.UserID, &e.CameraID, &e.Camera, &e.Kind, &e.StartedAt, &e.EndedAt, &e.Score, &e.Box, &labels, &e.Summary, &e.AIState, &e.Worry,
		&e.Frames, &e.Best, &clip, &e.Mode, &alerted, &reviewed, &starred, &nothing, &e.Pet, &e.ParcelRef, &e.ParcelText, &e.Weather, &e.Note, &e.Source, &e.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	e.Clip, e.Alerted, e.Reviewed, e.Starred, e.Nothing = clip == 1, alerted == 1, reviewed == 1, starred == 1, nothing == 1
	_ = json.Unmarshal([]byte(labels), &e.Labels)
	if e.Labels == nil {
		e.Labels = []string{}
	}
	return &e, nil
}

// CreateEvent stores a new event.
func (s *Store) CreateEvent(ctx context.Context, e *Event) error {
	if e.Labels == nil {
		e.Labels = []string{}
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO events (user_id, camera_id, kind, started_at, ended_at, score, box, labels, summary, ai_state, worry, frames, best, clip, mode,
		alerted, reviewed, starred, false_alarm, pet, parcel_ref, parcel_text, weather, note, source, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.UserID, e.CameraID, e.Kind, e.StartedAt, e.EndedAt, e.Score, e.Box, jsonText(e.Labels), e.Summary, e.AIState, e.Worry, e.Frames, e.Best, boolInt(e.Clip), e.Mode,
		boolInt(e.Alerted), boolInt(e.Reviewed), boolInt(e.Starred), boolInt(e.Nothing), e.Pet, e.ParcelRef, e.ParcelText, e.Weather, e.Note, e.Source, firstNonEmptyS(e.CreatedAt, Now()))
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	return nil
}

func firstNonEmptyS(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// SaveEvent writes back what the background work learned about an event
// (end, pictures, the AI's answer, links found).
func (s *Store) SaveEvent(ctx context.Context, e *Event) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE events SET ended_at = ?, score = ?, box = ?, labels = ?, summary = ?, ai_state = ?, worry = ?, frames = ?, best = ?, clip = ?,
		alerted = ?, false_alarm = ?, pet = ?, parcel_ref = ?, parcel_text = ?, weather = ? WHERE id = ?`,
		e.EndedAt, e.Score, e.Box, jsonText(e.Labels), e.Summary, e.AIState, e.Worry, e.Frames, e.Best, boolInt(e.Clip),
		boolInt(e.Alerted), boolInt(e.Nothing), e.Pet, e.ParcelRef, e.ParcelText, e.Weather, e.ID)
	return err
}

// GetEvent is one event on a camera the person may watch.
func (s *Store) GetEvent(ctx context.Context, uid, id int64) (*Event, error) {
	return scanEvent(s.DB.QueryRowContext(ctx, `SELECT `+eventCols+` FROM events e LEFT JOIN cameras c ON c.id = e.camera_id WHERE e.id = ? AND e.camera_id IN `+visibleCams, id, uid, uid))
}

// EventByID is any event (the background work only).
func (s *Store) EventByID(ctx context.Context, id int64) (*Event, error) {
	return scanEvent(s.DB.QueryRowContext(ctx, `SELECT `+eventCols+` FROM events e LEFT JOIN cameras c ON c.id = e.camera_id WHERE e.id = ?`, id))
}

// EventFilter narrows a list of events.
type EventFilter struct {
	CameraID   int64
	Label      string // person | vehicle | animal | parcel | doorbell | motion
	From, To   string // UTC ISO, To exclusive
	Unreviewed bool
	Starred    bool
	Nothing    bool // include false alarms
	Q          string
	Limit      int
	Offset     int
}

func (f EventFilter) where(uid int64) (string, []any) {
	w := []string{`e.camera_id IN ` + visibleCams}
	args := []any{uid, uid}
	if f.CameraID > 0 {
		w = append(w, `e.camera_id = ?`)
		args = append(args, f.CameraID)
	}
	switch f.Label {
	case "":
	case "doorbell":
		w = append(w, `e.kind = 'doorbell'`)
	case "motion":
		w = append(w, `e.labels = '[]' AND e.kind = 'motion'`)
	default:
		w = append(w, `e.labels LIKE ?`)
		args = append(args, `%"`+f.Label+`"%`)
	}
	if f.From != "" {
		w = append(w, `e.started_at >= ?`)
		args = append(args, f.From)
	}
	if f.To != "" {
		w = append(w, `e.started_at < ?`)
		args = append(args, f.To)
	}
	if f.Unreviewed {
		w = append(w, `e.reviewed = 0`)
	}
	if f.Starred {
		w = append(w, `e.starred = 1`)
	}
	if !f.Nothing && !f.Starred {
		w = append(w, `e.false_alarm = 0`)
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		w = append(w, `(e.summary LIKE ? OR e.note LIKE ? OR c.name LIKE ? OR e.labels LIKE ? OR e.pet LIKE ? OR e.parcel_text LIKE ?)`)
		like := "%" + q + "%"
		args = append(args, like, like, like, like, like, like)
	}
	return strings.Join(w, " AND "), args
}

// ListEvents is the newest first.
func (s *Store) ListEvents(ctx context.Context, uid int64, f EventFilter) ([]Event, int, error) {
	where, args := f.where(uid)
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM events e LEFT JOIN cameras c ON c.id = e.camera_id WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+eventCols+` FROM events e LEFT JOIN cameras c ON c.id = e.camera_id WHERE `+where+` ORDER BY e.started_at DESC, e.id DESC LIMIT ? OFFSET ?`,
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *e)
	}
	return out, total, rows.Err()
}

// EventFlags are what a person may change on an event of their own camera.
type EventFlags struct {
	Reviewed *bool
	Starred  *bool
	Nothing  *bool
	Note     *string
	Labels   *[]string
}

// SetEventFlags changes an event on one of the person's own cameras.
func (s *Store) SetEventFlags(ctx context.Context, uid, id int64, f EventFlags) error {
	sets, args := []string{}, []any{}
	if f.Reviewed != nil {
		sets, args = append(sets, `reviewed = ?`), append(args, boolInt(*f.Reviewed))
	}
	if f.Starred != nil {
		sets, args = append(sets, `starred = ?`), append(args, boolInt(*f.Starred))
	}
	if f.Nothing != nil {
		sets, args = append(sets, `false_alarm = ?`), append(args, boolInt(*f.Nothing))
	}
	if f.Note != nil {
		sets, args = append(sets, `note = ?`), append(args, *f.Note)
	}
	if f.Labels != nil {
		sets, args = append(sets, `labels = ?`), append(args, jsonText(*f.Labels))
	}
	if len(sets) == 0 {
		return nil
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE events SET `+strings.Join(sets, ", ")+` WHERE id = ? AND user_id = ?`, append(args, id, uid)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReviewAll marks every event of the person's own cameras (or one camera) as seen.
func (s *Store) ReviewAll(ctx context.Context, uid, cameraID int64, before string) (int64, error) {
	q, args := `UPDATE events SET reviewed = 1 WHERE user_id = ? AND reviewed = 0`, []any{uid}
	if cameraID > 0 {
		q, args = q+` AND camera_id = ?`, append(args, cameraID)
	}
	if before != "" {
		q, args = q+` AND started_at <= ?`, append(args, before)
	}
	res, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteEvent removes an event of the person's own camera.
func (s *Store) DeleteEvent(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM events WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ExpiredEvents are events past their camera's keep time (starred ones stay).
func (s *Store) ExpiredEvents(ctx context.Context, now time.Time) ([]Event, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+eventCols+` FROM events e JOIN cameras c ON c.id = e.camera_id
		WHERE e.starred = 0 AND c.event_days > 0 AND e.started_at < strftime('%Y-%m-%dT%H:%M:%fZ', ?, '-' || c.event_days || ' days') LIMIT 500`, now.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// EventByIDRaw deletes without a person (retention).
func (s *Store) DropEvent(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM events WHERE id = ?`, id)
	return err
}

// LastEvent is the newest event of a camera.
func (s *Store) LastEvent(ctx context.Context, cameraID int64) (*Event, error) {
	return scanEvent(s.DB.QueryRowContext(ctx, `SELECT `+eventCols+` FROM events e LEFT JOIN cameras c ON c.id = e.camera_id WHERE e.camera_id = ? AND e.false_alarm = 0 ORDER BY e.started_at DESC LIMIT 1`, cameraID))
}

// LastAlert is when a camera last alerted.
func (s *Store) LastAlert(ctx context.Context, cameraID int64) string {
	var at sql.NullString
	_ = s.DB.QueryRowContext(ctx, `SELECT MAX(started_at) FROM events WHERE camera_id = ? AND alerted = 1`, cameraID).Scan(&at)
	return at.String
}

// ---- recordings ----

// AddSegment stores one recorded file (once).
func (s *Store) AddSegment(ctx context.Context, g *Segment) error {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO segments (user_id, camera_id, started_at, ended_at, path, bytes) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET ended_at = excluded.ended_at, bytes = excluded.bytes`, g.UserID, g.CameraID, g.StartedAt, g.EndedAt, g.Path, g.Bytes)
	if err != nil {
		return err
	}
	g.ID, _ = res.LastInsertId()
	return nil
}

func scanSegments(rows *sql.Rows) ([]Segment, error) {
	defer rows.Close()
	out := []Segment{}
	for rows.Next() {
		var g Segment
		if err := rows.Scan(&g.ID, &g.UserID, &g.CameraID, &g.StartedAt, &g.EndedAt, &g.Path, &g.Bytes); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// Segments of one camera that overlap [from, to).
func (s *Store) Segments(ctx context.Context, cameraID int64, from, to string) ([]Segment, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, camera_id, started_at, ended_at, path, bytes FROM segments WHERE camera_id = ? AND ended_at > ? AND started_at < ? ORDER BY started_at`, cameraID, from, to)
	if err != nil {
		return nil, err
	}
	return scanSegments(rows)
}

// GetSegment is one recorded file of a camera the person may watch.
func (s *Store) GetSegment(ctx context.Context, uid, id int64) (*Segment, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, camera_id, started_at, ended_at, path, bytes FROM segments WHERE id = ? AND camera_id IN `+visibleCams, id, uid, uid)
	if err != nil {
		return nil, err
	}
	list, err := scanSegments(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

// SegmentPaths lists every path known for a camera.
func (s *Store) SegmentPaths(ctx context.Context, cameraID int64) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT path FROM segments WHERE camera_id = ?`, cameraID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out[p] = true
	}
	return out, rows.Err()
}

// OldSegments are a camera's files that started before t.
func (s *Store) OldSegments(ctx context.Context, cameraID int64, before string) ([]Segment, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, camera_id, started_at, ended_at, path, bytes FROM segments WHERE camera_id = ? AND started_at < ? ORDER BY started_at LIMIT 2000`, cameraID, before)
	if err != nil {
		return nil, err
	}
	return scanSegments(rows)
}

// OldestSegments are the oldest files on the box (when the disk is full).
func (s *Store) OldestSegments(ctx context.Context, n int) ([]Segment, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, camera_id, started_at, ended_at, path, bytes FROM segments ORDER BY started_at LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	return scanSegments(rows)
}

// DeleteSegment forgets a file.
func (s *Store) DeleteSegment(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM segments WHERE id = ?`, id)
	return err
}

// EventsBetween are a camera's events overlapping [from, to) (to keep the video around them).
func (s *Store) EventsBetween(ctx context.Context, cameraID int64, from, to string) ([][2]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT started_at, COALESCE(ended_at, started_at) FROM events WHERE camera_id = ? AND false_alarm = 0 AND started_at < ? AND COALESCE(ended_at, ?) > ?`, cameraID, to, to, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := [][2]string{}
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return nil, err
		}
		out = append(out, [2]string{a, b})
	}
	return out, rows.Err()
}

// StorageUse is the bytes recorded per camera (all cameras).
func (s *Store) StorageUse(ctx context.Context) (map[int64]int64, int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT camera_id, COALESCE(SUM(bytes), 0) FROM segments GROUP BY camera_id`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, total := map[int64]int64{}, int64(0)
	for rows.Next() {
		var id, n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, 0, err
		}
		out[id] = n
		total += n
	}
	return out, total, rows.Err()
}

// RecordedSpan is the first and last recorded moment of a camera.
func (s *Store) RecordedSpan(ctx context.Context, cameraID int64) (string, string) {
	var a, b sql.NullString
	_ = s.DB.QueryRowContext(ctx, `SELECT MIN(started_at), MAX(ended_at) FROM segments WHERE camera_id = ?`, cameraID).Scan(&a, &b)
	return a.String, b.String
}

// ---- modes ----

// AddModeChange writes down a change of mode.
func (s *Store) AddModeChange(ctx context.Context, uid int64, mode, reason, by string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO mode_log (user_id, mode, reason, set_by, at) VALUES (?, ?, ?, ?, ?)`, uid, mode, reason, by, Now())
	return err
}

// ModeChanges are a person's changes of mode in [from, to), oldest first.
func (s *Store) ModeChanges(ctx context.Context, uid int64, from, to string, limit int) ([]ModeChange, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, mode, reason, set_by, at FROM mode_log WHERE user_id = ? AND at >= ? AND at < ? ORDER BY at LIMIT ?`, uid, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ModeChange{}
	for rows.Next() {
		var m ModeChange
		if err := rows.Scan(&m.ID, &m.Mode, &m.Reason, &m.By, &m.At); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RecentModeChanges are the newest changes, newest first.
func (s *Store) RecentModeChanges(ctx context.Context, uid int64, limit int) ([]ModeChange, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, mode, reason, set_by, at FROM mode_log WHERE user_id = ? ORDER BY at DESC, id DESC LIMIT ?`, uid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ModeChange{}
	for rows.Next() {
		var m ModeChange
		if err := rows.Scan(&m.ID, &m.Mode, &m.Reason, &m.By, &m.At); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---- numbers ----

// EventCount is events per day and label.
type EventCount struct {
	Day    string `json:"day"`
	Label  string `json:"label"`
	Hour   int    `json:"hour"`
	Camera int64  `json:"camera_id"`
	N      int    `json:"n"`
}

// EventRows are the (start, labels, kind, camera) of a person's visible events since `from` — the numbers are counted in Go (local time).
func (s *Store) EventRows(ctx context.Context, uid int64, from string) ([]Event, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT e.started_at, e.labels, e.kind, e.camera_id, e.false_alarm, e.alerted FROM events e WHERE e.camera_id IN `+visibleCams+` AND e.started_at >= ? ORDER BY e.started_at`, uid, uid, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var labels string
		var nothing, alerted int
		if err := rows.Scan(&e.StartedAt, &labels, &e.Kind, &e.CameraID, &nothing, &alerted); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(labels), &e.Labels)
		e.Nothing, e.Alerted = nothing == 1, alerted == 1
		out = append(out, e)
	}
	return out, rows.Err()
}

// UnreviewedCount is how many events the person has not looked at yet (own cameras).
func (s *Store) UnreviewedCount(ctx context.Context, uid int64) int {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM events WHERE user_id = ? AND reviewed = 0 AND false_alarm = 0`, uid).Scan(&n)
	return n
}

// FinishEvent writes the end of an event and its pictures.
func (s *Store) FinishEvent(ctx context.Context, e *Event) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE events SET ended_at = ?, score = ?, box = ?, frames = ?, best = ?, clip = ? WHERE id = ?`,
		e.EndedAt, e.Score, e.Box, e.Frames, e.Best, boolInt(e.Clip), e.ID)
	return err
}

// SaveEventCheck writes what the AI and the other apps said about an event,
// and whether it alerted.
func (s *Store) SaveEventCheck(ctx context.Context, e *Event) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE events SET labels = ?, summary = ?, ai_state = ?, worry = ?, false_alarm = ?, pet = ?, parcel_ref = ?, parcel_text = ?, weather = ?, alerted = ? WHERE id = ?`,
		jsonText(e.Labels), e.Summary, e.AIState, e.Worry, boolInt(e.Nothing), e.Pet, e.ParcelRef, e.ParcelText, e.Weather, boolInt(e.Alerted), e.ID)
	return err
}

// SetEventClip notes that an event's clip was saved with it.
func (s *Store) SetEventClip(ctx context.Context, id int64, on bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE events SET clip = ? WHERE id = ?`, boolInt(on), id)
	return err
}

// AllEvents is every event of the person's own cameras, oldest first (export).
func (s *Store) AllEvents(ctx context.Context, uid int64) ([]Event, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+eventCols+` FROM events e LEFT JOIN cameras c ON c.id = e.camera_id WHERE e.user_id = ? ORDER BY e.started_at`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// EventExists says whether a camera has an event starting at that moment (import).
func (s *Store) EventExists(ctx context.Context, cameraID int64, startedAt string) bool {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM events WHERE camera_id = ? AND started_at = ?`, cameraID, startedAt).Scan(&n)
	return n > 0
}

// ModeChangeExists says whether a change of mode is already written (import).
func (s *Store) ModeChangeExists(ctx context.Context, uid int64, at string) bool {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM mode_log WHERE user_id = ? AND at = ?`, uid, at).Scan(&n)
	return n > 0
}

// AddModeChangeAt writes down a change of mode at a given time (import, demo).
func (s *Store) AddModeChangeAt(ctx context.Context, uid int64, mode, reason, by, at string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO mode_log (user_id, mode, reason, set_by, at) VALUES (?, ?, ?, ?, ?)`, uid, mode, reason, by, at)
	return err
}
