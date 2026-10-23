package protocol

type RoundView struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type ParticipantView struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
	ShortID  string `json:"short_id"`
	Color    string `json:"color"`
	Online   bool   `json:"online"`
}

type SessionView struct {
	SessionID    string `json:"session_id"`
	AID          string `json:"a_id"`
	BID          string `json:"b_id"`
	Mode         Mode   `json:"mode"`
	State        string `json:"state"`
	CreatedAtUTC string `json:"created_at_utc,omitempty"`
	ExpiresAtUTC string `json:"expires_at_utc,omitempty"`
	ClosedReason string `json:"closed_reason,omitempty"`
}

type Welcome struct {
	V               int               `json:"v"`
	Type            string            `json:"type"`
	RequestID       string            `json:"request_id"`
	ParticipantID   string            `json:"participant_id"`
	Nickname        string            `json:"nickname"`
	ShortID         string            `json:"short_id"`
	Color           string            `json:"color"`
	ResumeToken     string            `json:"resume_token"`
	ConnectionEpoch string            `json:"connection_epoch"`
	Round           RoundView         `json:"round"`
	TopologyVersion uint64            `json:"topology_version"`
	Participants    []ParticipantView `json:"participants"`
	Sessions        []SessionView     `json:"sessions"`
}

type Snapshot struct {
	V               int               `json:"v"`
	Type            string            `json:"type"`
	Round           RoundView         `json:"round"`
	TopologyVersion uint64            `json:"topology_version"`
	Participants    []ParticipantView `json:"participants"`
	Sessions        []SessionView     `json:"sessions"`
}

type TopologyChanged struct {
	V               int               `json:"v"`
	Type            string            `json:"type"`
	RoundID         string            `json:"round_id"`
	ConnectionEpoch string            `json:"connection_epoch"`
	TopologyVersion uint64            `json:"topology_version"`
	Participants    []ParticipantView `json:"participants"`
	Sessions        []SessionView     `json:"sessions"`
}

type Pong struct {
	V             int    `json:"v"`
	Type          string `json:"type"`
	ServerTimeUTC string `json:"server_time_utc"`
}

type AdminWelcome struct {
	V               int               `json:"v"`
	Type            string            `json:"type"`
	RequestID       string            `json:"request_id"`
	ConnectionEpoch string            `json:"connection_epoch"`
	ServerVersion   string            `json:"server_version"`
	ProtocolVersion int               `json:"protocol_version"`
	Round           RoundView         `json:"round"`
	TopologyVersion uint64            `json:"topology_version"`
	Participants    []ParticipantView `json:"participants"`
	Sessions        []SessionView     `json:"sessions"`
}

type ErrorMessage struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	Code      Code   `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

func NewErrorMessage(err *Error) *ErrorMessage {
	if err == nil {
		return &ErrorMessage{V: 1, Type: "error", Code: CodeInternal, Message: "internal error"}
	}
	return &ErrorMessage{
		V:         1,
		Type:      "error",
		Code:      err.Code,
		Message:   err.Message,
		RequestID: err.RequestID,
		SessionID: err.SessionID,
	}
}

type SessionChanged struct {
	V               int         `json:"v"`
	Type            string      `json:"type"`
	RoundID         string      `json:"round_id"`
	ConnectionEpoch string      `json:"connection_epoch"`
	Session         SessionView `json:"session"`
}

type ChatAccepted struct {
	V               int    `json:"v"`
	Type            string `json:"type"`
	RequestID       string `json:"request_id"`
	RoundID         string `json:"round_id"`
	ConnectionEpoch string `json:"connection_epoch"`
	SessionID       string `json:"session_id"`
	MessageID       string `json:"message_id"`
	Seq             string `json:"seq"`
	AcceptedAtUTC   string `json:"accepted_at_utc"`
}

type ChatDeliver struct {
	V               int     `json:"v"`
	Type            string  `json:"type"`
	RoundID         string  `json:"round_id"`
	ConnectionEpoch string  `json:"connection_epoch"`
	SessionID       string  `json:"session_id"`
	MessageID       string  `json:"message_id"`
	SenderID        string  `json:"sender_id"`
	Seq             string  `json:"seq"`
	Text            *string `json:"text,omitempty"`
	NonceB64        string  `json:"nonce_b64,omitempty"`
	CiphertextB64   string  `json:"ciphertext_b64,omitempty"`
	SentAtUTC       string  `json:"sent_at_utc"`
}

type ChatStatus struct {
	V               int    `json:"v"`
	Type            string `json:"type"`
	RoundID         string `json:"round_id"`
	ConnectionEpoch string `json:"connection_epoch"`
	SessionID       string `json:"session_id"`
	MessageID       string `json:"message_id"`
	Status          string `json:"status"`
	AtUTC           string `json:"at_utc"`
}

type ActivityEvent struct {
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
	SenderID  string `json:"sender_id"`
	Direction string `json:"direction"`
	AtUTC     string `json:"at_utc"`
}

type ActivityBatch struct {
	V               int             `json:"v"`
	Type            string          `json:"type"`
	RoundID         string          `json:"round_id"`
	ConnectionEpoch string          `json:"connection_epoch"`
	Events          []ActivityEvent `json:"events"`
}

type KeyPublishDeliver struct {
	V               int    `json:"v"`
	Type            string `json:"type"`
	RoundID         string `json:"round_id"`
	ConnectionEpoch string `json:"connection_epoch"`
	SessionID       string `json:"session_id"`
	SenderID        string `json:"sender_id"`
	PublicKeyB64    string `json:"public_key_b64"`
}

type KeyConfirmDeliver struct {
	V               int    `json:"v"`
	Type            string `json:"type"`
	RoundID         string `json:"round_id"`
	ConnectionEpoch string `json:"connection_epoch"`
	SessionID       string `json:"session_id"`
	SenderID        string `json:"sender_id"`
	Seq             string `json:"seq"`
	NonceB64        string `json:"nonce_b64"`
	CiphertextB64   string `json:"ciphertext_b64"`
}

type ObserverRecord struct {
	SessionID      string  `json:"session_id,omitempty"`
	MessageID      string  `json:"message_id"`
	SenderID       string  `json:"sender_id"`
	Direction      string  `json:"direction"`
	Mode           string  `json:"mode"`
	PayloadBytes   int     `json:"payload_bytes"`
	WSMessageBytes int     `json:"ws_message_bytes"`
	AtUTC          string  `json:"at_utc"`
	MonoMS         int64   `json:"mono_ms"`
	IntervalMS     *int64  `json:"interval_ms"`
	ReceiptStatus  string  `json:"receipt_status"`
	Text           *string `json:"text"`
	NonceB64       *string `json:"nonce_b64"`
	CiphertextB64  *string `json:"ciphertext_b64"`
}

type ObserverStats struct {
	MessageCount        int    `json:"message_count"`
	PayloadBytesTotal   int64  `json:"payload_bytes_total"`
	WSMessageBytesTotal int64  `json:"ws_message_bytes_total"`
	LastDirection       string `json:"last_direction"`
	DirectionSwitches   int    `json:"direction_switches"`
	ConfirmedCount      int    `json:"confirmed_count"`
	UnconfirmedCount    int    `json:"unconfirmed_count"`
	FailedCount         int    `json:"failed_count"`
	FirstAtUTC          string `json:"first_at_utc"`
	LastAtUTC           string `json:"last_at_utc"`
}

type ObserverSnapshot struct {
	V         int              `json:"v"`
	Type      string           `json:"type"`
	RequestID string           `json:"request_id,omitempty"`
	SessionID string           `json:"session_id"`
	Mode      string           `json:"mode"`
	Stats     ObserverStats    `json:"stats"`
	Truncated bool             `json:"truncated"`
	Messages  []ObserverRecord `json:"messages"`
}

type ObserverEvent struct {
	V         int            `json:"v"`
	Type      string         `json:"type"`
	SessionID string         `json:"session_id"`
	Message   ObserverRecord `json:"message"`
	Update    bool           `json:"update"`
	Stats     ObserverStats  `json:"stats"`
	Truncated bool           `json:"truncated"`
}

type RoundChanged struct {
	V         int       `json:"v"`
	Type      string    `json:"type"`
	RequestID string    `json:"request_id"`
	Round     RoundView `json:"round"`
	Action    string    `json:"action"`
}

type RoundEnded struct {
	V          int    `json:"v"`
	Type       string `json:"type"`
	Reason     string `json:"reason"`
	NewRoundID string `json:"new_round_id,omitempty"`
}
