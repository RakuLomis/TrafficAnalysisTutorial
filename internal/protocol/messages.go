package protocol

type Message interface {
	MessageType() string
}

type Mode string

const (
	ModePlaintext Mode = "plaintext"
	ModeEncrypted Mode = "encrypted"
)

type Join struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	RoundID   string `json:"round_id"`
	Nickname  string `json:"nickname"`
}

func (*Join) MessageType() string { return "join" }

type Resume struct {
	V           int    `json:"v"`
	Type        string `json:"type"`
	RequestID   string `json:"request_id"`
	RoundID     string `json:"round_id"`
	ResumeToken string `json:"resume_token"`
}

func (*Resume) MessageType() string { return "resume" }

type Ping struct {
	V    int    `json:"v"`
	Type string `json:"type"`
}

func (*Ping) MessageType() string { return "ping" }

type Joined struct {
	V               int    `json:"v"`
	Type            string `json:"type"`
	RequestID       string `json:"request_id"`
	RoundID         string `json:"round_id"`
	ConnectionEpoch string `json:"connection_epoch"`
}

type SessionCommand struct {
	Joined
	SessionID string `json:"session_id"`
}

type Leave struct {
	Joined
}

func (*Leave) MessageType() string { return "leave" }

type InviteCreate struct {
	Joined
	TargetID string `json:"target_id"`
	Mode     Mode   `json:"mode"`
}

func (*InviteCreate) MessageType() string { return "invite.create" }

type InviteRespond struct {
	SessionCommand
	Accept *bool `json:"accept"`
}

func (*InviteRespond) MessageType() string { return "invite.respond" }

type InviteCancel struct {
	SessionCommand
}

func (*InviteCancel) MessageType() string { return "invite.cancel" }

type SessionClose struct {
	SessionCommand
	Reason string `json:"reason"`
}

func (*SessionClose) MessageType() string { return "session.close" }

type KeyPublish struct {
	SessionCommand
	PublicKeyB64 string `json:"public_key_b64"`
}

func (*KeyPublish) MessageType() string { return "key.publish" }

type KeyConfirm struct {
	SessionCommand
	Seq           string `json:"seq"`
	NonceB64      string `json:"nonce_b64"`
	CiphertextB64 string `json:"ciphertext_b64"`
}

func (*KeyConfirm) MessageType() string { return "key.confirm" }

type KeyReady struct {
	SessionCommand
	Status string `json:"status"`
}

func (*KeyReady) MessageType() string { return "key.ready" }

type ChatSend struct {
	SessionCommand
	MessageID     string  `json:"message_id"`
	Seq           string  `json:"seq"`
	Text          *string `json:"text,omitempty"`
	NonceB64      string  `json:"nonce_b64,omitempty"`
	CiphertextB64 string  `json:"ciphertext_b64,omitempty"`
}

func (*ChatSend) MessageType() string { return "chat.send" }

type ChatReceipt struct {
	SessionCommand
	MessageID string `json:"message_id"`
	Status    string `json:"status"`
}

func (*ChatReceipt) MessageType() string { return "chat.receipt" }

type AdminAuth struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Token     string `json:"token"`
}

func (*AdminAuth) MessageType() string { return "admin.auth" }

type ObserverSubscribe struct {
	V         int     `json:"v"`
	Type      string  `json:"type"`
	RequestID string  `json:"request_id"`
	SessionID *string `json:"session_id"`
}

func (*ObserverSubscribe) MessageType() string { return "observer.subscribe" }

type RoundReset struct {
	V               int    `json:"v"`
	Type            string `json:"type"`
	RequestID       string `json:"request_id"`
	ExpectedRoundID string `json:"expected_round_id"`
}

func (*RoundReset) MessageType() string { return "round.reset" }

type ClassEnd struct {
	V               int    `json:"v"`
	Type            string `json:"type"`
	RequestID       string `json:"request_id"`
	ExpectedRoundID string `json:"expected_round_id"`
}

func (*ClassEnd) MessageType() string { return "class.end" }

type ClassStart struct {
	V               int    `json:"v"`
	Type            string `json:"type"`
	RequestID       string `json:"request_id"`
	ExpectedRoundID string `json:"expected_round_id"`
}

func (*ClassStart) MessageType() string { return "class.start" }

type ServerShutdown struct {
	V         int    `json:"v"`
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
}

func (*ServerShutdown) MessageType() string { return "server.shutdown" }
