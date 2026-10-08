package platform

import "time"

type Actor struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Enabled   bool      `json:"enabled"`
	Available bool      `json:"available"`
	Online    bool      `json:"online"`
	CreatedAt time.Time `json:"created_at"`
}

type Conversation struct {
	ID                 string     `json:"id"`
	VisitorID          string     `json:"visitor_id"`
	VisitorName        string     `json:"visitor_name"`
	AgentID            string     `json:"agent_id"`
	AgentName          string     `json:"agent_name"`
	Status             string     `json:"status"`
	Title              string     `json:"title"`
	Version            int64      `json:"version"`
	LastSeq            int64      `json:"last_seq"`
	LastMessage        string     `json:"last_message"`
	UnreadCount        int64      `json:"unread_count"`
	AIPhase            string     `json:"ai_phase"`
	AIHandledSeq       int64      `json:"ai_handled_seq"`
	ClarificationCount int        `json:"clarification_count"`
	Summary            string     `json:"summary"`
	HandoffReason      string     `json:"handoff_reason"`
	Feedback           string     `json:"feedback"`
	HandedOff          bool       `json:"handed_off"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	WaitingAt          *time.Time `json:"waiting_at,omitempty"`
	ClaimedAt          *time.Time `json:"claimed_at,omitempty"`
	FirstResponseAt    *time.Time `json:"first_response_at,omitempty"`
	ClosedAt           *time.Time `json:"closed_at,omitempty"`
}

type Citation struct {
	ID         string  `json:"id"`
	ChunkID    string  `json:"chunk_id"`
	DocumentID string  `json:"document_id"`
	Title      string  `json:"title"`
	Page       int     `json:"page"`
	Section    string  `json:"section"`
	Text       string  `json:"text"`
	Score      float64 `json:"score,omitempty"`
}

type Message struct {
	ID             string     `json:"id"`
	ConversationID string     `json:"conversation_id"`
	Seq            int64      `json:"seq"`
	SenderID       string     `json:"sender_id"`
	SenderRole     string     `json:"sender_role"`
	SenderName     string     `json:"sender_name"`
	ClientID       string     `json:"client_id"`
	Body           string     `json:"body"`
	ImageID        string     `json:"image_id,omitempty"`
	ImageURL       string     `json:"image_url,omitempty"`
	Citations      []Citation `json:"citations"`
	CreatedAt      time.Time  `json:"created_at"`
}

type Attachment struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	UploaderID     string    `json:"uploader_id"`
	Path           string    `json:"-"`
	MIME           string    `json:"mime"`
	Name           string    `json:"name"`
	Size           int64     `json:"size"`
	URL            string    `json:"url"`
	CreatedAt      time.Time `json:"created_at"`
}

type Document struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Enabled         bool      `json:"enabled"`
	Status          string    `json:"status"`
	ActiveVersionID string    `json:"active_version_id"`
	LatestVersionID string    `json:"latest_version_id"`
	Error           string    `json:"error"`
	ChunkCount      int       `json:"chunk_count"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
