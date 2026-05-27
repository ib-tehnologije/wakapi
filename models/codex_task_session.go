package models

const (
	CodexTaskSessionStatusOpen   = "open"
	CodexTaskSessionStatusClosed = "closed"
	CodexTaskSessionStatusStale  = "stale"

	CodexTaskWorklogSource = "CodexTask"
)

type CodexTaskSession struct {
	ID                   string      `gorm:"primaryKey" json:"id"`
	User                 *User       `json:"-" gorm:"not null; constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	UserID               string      `gorm:"not null; size:128; index:idx_codex_task_user_external,unique; index:idx_codex_task_user_started" json:"user_id"`
	ExternalKey          string      `gorm:"not null; size:255; index:idx_codex_task_user_external,unique" json:"external_key"`
	Project              string      `gorm:"not null; size:191; index:idx_codex_task_user_started" json:"project"`
	WorkspaceRoot        string      `gorm:"size:512" json:"workspace_root"`
	Repository           string      `gorm:"size:255" json:"repository"`
	Branch               string      `gorm:"size:255" json:"branch"`
	StartedAt            CustomTime  `gorm:"not null; timeScale:3; index:idx_codex_task_user_started" json:"started_at"`
	EndedAt              *CustomTime `gorm:"timeScale:3; index" json:"ended_at"`
	DurationSeconds      float64     `gorm:"not null; default:0" json:"duration_seconds"`
	Status               string      `gorm:"not null; size:32; index" json:"status"`
	SummaryHR            string      `gorm:"type:text" json:"summary_hr"`
	SummaryHROriginal    string      `gorm:"type:text" json:"summary_hr_original"`
	SummaryHRNormalized  string      `gorm:"type:text" json:"summary_hr_normalized"`
	SummarySource        string      `gorm:"size:32" json:"summary_source"`
	SummaryConfidence    float64     `gorm:"not null; default:0" json:"summary_confidence"`
	ClientMessageHR      string      `gorm:"type:text" json:"client_message_hr"`
	InternalMessageHR    string      `gorm:"type:text" json:"internal_message_hr"`
	ReviewStatus         string      `gorm:"size:32; index" json:"review_status"`
	Prompt               string      `gorm:"type:text" json:"prompt"`
	LastAssistantMessage string      `gorm:"type:text" json:"last_assistant_message"`
	EvidenceJSON         string      `gorm:"type:text" json:"evidence_json"`
	TechnicalNote        string      `gorm:"type:text" json:"technical_note"`
	CreatedAt            CustomTime  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt            CustomTime  `gorm:"autoUpdateTime" json:"updated_at"`
}
