package protocol

type Code string

const (
	CodeInvalidMessage  Code = "INVALID_MESSAGE"
	CodeVersionMismatch Code = "VERSION_MISMATCH"
	CodeStaleRound      Code = "STALE_ROUND"
	CodeStaleConnection Code = "STALE_CONNECTION"
	CodeNotMember       Code = "NOT_MEMBER"
	CodeNotActive       Code = "NOT_ACTIVE"
	CodeModeMismatch    Code = "MODE_MISMATCH"
	CodeCapacityReached Code = "CAPACITY_REACHED"
	CodeInviteExpired   Code = "INVITE_EXPIRED"
	CodeKeyTimeout      Code = "KEY_TIMEOUT"
	CodeMessageTooLarge Code = "MESSAGE_TOO_LARGE"
	CodeRateLimited     Code = "RATE_LIMITED"
	CodeIDConflict      Code = "ID_CONFLICT"
	CodeCryptoFailed    Code = "CRYPTO_FAILED"
	CodeResumeExpired   Code = "RESUME_EXPIRED"
	CodeClassEnded      Code = "CLASS_ENDED"
	CodeSlowClient      Code = "SLOW_CLIENT"
	CodeUnauthorized    Code = "UNAUTHORIZED"
	CodeForbidden       Code = "FORBIDDEN"
	CodeSessionExists   Code = "SESSION_EXISTS"
	CodeInternal        Code = "INTERNAL"
)

type Error struct {
	Code      Code   `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return string(e.Code) + ": " + e.Message
}
