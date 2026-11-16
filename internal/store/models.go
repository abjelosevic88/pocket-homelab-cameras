package store

// User is an account on this box.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Conn is one person's connection to a sibling Pocket app.
type Conn struct {
	Mode      string `json:"mode,omitempty"`
	URL       string `json:"url,omitempty"`
	Token     string `json:"token,omitempty"`
	PublicURL string `json:"public_url,omitempty"`
}

// Settings are the box-wide values plus one person's preferences and their
// own connections.
type Settings struct {
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`

	DateFormat string `json:"date_format"`
	TimeFormat string `json:"time_format"`
	WeekStart  int    `json:"week_start"` // 1 = Monday, 7 = Sunday

	// Home / Away / Night / Off: which cameras alert. Set by hand, or by
	// itself (AutoMode) from the night hours, trips in Pocket Travel, where
	// you are (Pocket Movement) and whose phones are on the Wi-Fi (Pocket Network).
	Mode       string   `json:"mode"` // home | away | night | off
	ModeAt     string   `json:"mode_at"`
	ModeBy     string   `json:"mode_by"` // you | auto | <app>
	ModeReason string   `json:"mode_reason"`
	AutoMode   bool     `json:"auto_mode"`
	NightFrom  int      `json:"night_from"` // hour, 0–23
	NightTo    int      `json:"night_to"`
	AwayTrips  bool     `json:"away_trips"`  // Away while a trip in Pocket Travel is on
	AwayOut    bool     `json:"away_out"`    // Away while you are away from home (Pocket Movement)
	AwayPhones bool     `json:"away_phones"` // Away when nobody's phone is on the home Wi-Fi (Pocket Network)
	Phones     []string `json:"phones"`      // whose phones count (Pocket Network owners; empty = anyone's)
	HomeRadius int      `json:"home_radius"` // metres

	// The other apps.
	FilesFolder string `json:"files_folder"` // where clips and pictures go in Pocket Files
	NotesFolder string `json:"notes_folder"` // where events go in Pocket Notes
	TasksList   string `json:"tasks_list"`   // which list (empty = the inbox)
	ChatTo      string `json:"chat_to"`      // the Pocket Chat conversation pictures go to
	WeatherCalm bool   `json:"weather_calm"` // no movement-only alerts in a storm
	PetsQuiet   bool   `json:"pets_quiet"`   // no alerts for your own pets
	HomeLog     bool   `json:"home_log"`     // write a camera that stays offline a day into its Pocket Home log
	AIHelp      bool   `json:"ai_help"`      // ask the AI what is in each event
	AIPhotos    bool   `json:"ai_photos"`    // pictures may go to the AI

	// Notifications.
	NotifyEvents   bool   `json:"notify_events"`   // what each camera alerts on, in the modes it alerts in
	NotifyDoorbell bool   `json:"notify_doorbell"` // a doorbell ring, always
	NotifyOffline  bool   `json:"notify_offline"`  // a camera stops sending pictures
	Cooldown       int    `json:"cooldown"`        // minutes between alerts from one camera
	NotifyURL      string `json:"notify_url"`
	NotifyFormat   string `json:"notify_format"` // apprise | ntfy | json

	// Each sibling Pocket app, by slug.
	Conns map[string]Conn `json:"conns"`

	// AI: Pocket AI through Core, or an OpenAI-compatible API with the
	// person's own key.
	AIProvider    string `json:"ai_provider"` // auto | pocket | openai
	AIPocketModel string `json:"ai_pocket_model"`
	OpenAIKey     string `json:"openai_api_key"`
	OpenAIModel   string `json:"openai_model"`
	OpenAIBaseURL string `json:"openai_base_url"`
}

// ---- cameras ----

// Zone is a part of the picture: only movement inside "watch" zones counts,
// movement inside "ignore" zones never does. Points are 0..1 of the picture.
type Zone struct {
	Name   string       `json:"name"`
	Kind   string       `json:"kind"` // watch | ignore
	Points [][2]float64 `json:"points"`
}

// Camera is one camera.
type Camera struct {
	ID          int64    `json:"id"`
	UserID      int64    `json:"-"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Brand       string   `json:"brand"`
	Host        string   `json:"host"`
	Username    string   `json:"username"`
	Password    string   `json:"password,omitempty"`
	MainURL     string   `json:"main_url"`
	SubURL      string   `json:"sub_url"`
	SnapshotURL string   `json:"snapshot_url"`
	Enabled     bool     `json:"enabled"`
	Record      string   `json:"record"`
	RetainDays  int      `json:"retain_days"`
	EventDays   int      `json:"event_days"`
	Detect      bool     `json:"detect"`
	Sensitivity int      `json:"sensitivity"`
	Zones       []Zone   `json:"zones"`
	Labels      []string `json:"labels"`
	Modes       []string `json:"modes"`
	Doorbell    bool     `json:"doorbell"`
	AICheck     bool     `json:"ai_check"`
	Room        string   `json:"room"`
	RoomRef     string   `json:"room_ref"`
	HomeRef     string   `json:"home_ref"`
	ShareKey    string   `json:"share_key,omitempty"`
	Position    int      `json:"position"`
	Notes       string   `json:"notes"`
	LastSeenAt  *string  `json:"last_seen_at"`
	LastError   string   `json:"last_error"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   *string  `json:"updated_at"`

	// Mine is false for a camera someone shared with you.
	Mine  bool   `json:"mine"`
	Owner string `json:"owner,omitempty"`
}

// Share is another person who may watch a camera.
type Share struct {
	CameraID int64  `json:"camera_id"`
	UserID   int64  `json:"user_id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Notify   bool   `json:"notify"`
}

// Event is something that happened in front of a camera.
type Event struct {
	ID         int64    `json:"id"`
	UserID     int64    `json:"-"`
	CameraID   int64    `json:"camera_id"`
	Camera     string   `json:"camera"`
	Kind       string   `json:"kind"`
	StartedAt  string   `json:"started_at"`
	EndedAt    *string  `json:"ended_at"`
	Score      float64  `json:"score"`
	Box        string   `json:"box"`
	Labels     []string `json:"labels"`
	Summary    string   `json:"summary"`
	AIState    string   `json:"ai_state"`
	Worry      int      `json:"worry"`
	Frames     int      `json:"frames"`
	Best       int      `json:"best"`
	Clip       bool     `json:"clip"`
	Mode       string   `json:"mode"`
	Alerted    bool     `json:"alerted"`
	Reviewed   bool     `json:"reviewed"`
	Starred    bool     `json:"starred"`
	Nothing    bool     `json:"nothing"`
	Pet        string   `json:"pet"`
	ParcelRef  string   `json:"parcel_ref"`
	ParcelText string   `json:"parcel_text"`
	Weather    string   `json:"weather"`
	Note       string   `json:"note"`
	Source     string   `json:"source"`
	CreatedAt  string   `json:"created_at"`
}

// Segment is one recorded piece of video.
type Segment struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"-"`
	CameraID  int64  `json:"camera_id"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at"`
	Path      string `json:"-"`
	Bytes     int64  `json:"bytes"`
}

// ModeChange is one change of Home / Away / Night / Off.
type ModeChange struct {
	ID     int64  `json:"id"`
	Mode   string `json:"mode"`
	Reason string `json:"reason"`
	By     string `json:"by"`
	At     string `json:"at"`
}
