package protocol

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

func ParseClient(data []byte, limits Limits) (Message, *Error) {
	limits = limits.withDefaults()
	if len(data) > limits.MaxWSMessageBytes {
		return nil, withContext(data, &Error{
			Code:    CodeMessageTooLarge,
			Message: fmt.Sprintf("message exceeds %d bytes", limits.MaxWSMessageBytes),
		})
	}
	if !utf8.Valid(data) {
		return nil, invalid(data, "message is not valid UTF-8")
	}
	if err := checkNoDuplicateKeys(data); err != nil {
		return nil, invalid(data, "invalid JSON: %v", err)
	}

	var head struct {
		V    *int    `json:"v"`
		Type *string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, invalid(data, "invalid JSON: %v", err)
	}
	if head.V == nil || head.Type == nil {
		return nil, invalid(data, "missing v or type")
	}
	if *head.V != 1 {
		return nil, withContext(data, &Error{Code: CodeVersionMismatch, Message: "unsupported protocol version"})
	}

	switch *head.Type {
	case "join":
		msg, err := decodeStrict[Join](data)
		if err != nil {
			return nil, invalid(data, "invalid join: %v", err)
		}
		if err := validateJoin(data, msg, limits); err != nil {
			return nil, err
		}
		msg.Nickname = strings.TrimSpace(msg.Nickname)
		return msg, nil
	case "resume":
		msg, err := decodeStrict[Resume](data)
		if err != nil {
			return nil, invalid(data, "invalid resume: %v", err)
		}
		if err := validateResume(data, msg); err != nil {
			return nil, err
		}
		return msg, nil
	case "ping":
		msg, err := decodeStrict[Ping](data)
		if err != nil {
			return nil, invalid(data, "invalid ping: %v", err)
		}
		return msg, nil
	case "leave":
		msg, err := decodeStrict[Leave](data)
		if err != nil {
			return nil, invalid(data, "invalid leave: %v", err)
		}
		if err := validateJoined(data, msg.RequestID, msg.RoundID, msg.ConnectionEpoch); err != nil {
			return nil, err
		}
		return msg, nil
	case "invite.create":
		msg, err := decodeStrict[InviteCreate](data)
		if err != nil {
			return nil, invalid(data, "invalid invite.create: %v", err)
		}
		if err := validateJoined(data, msg.RequestID, msg.RoundID, msg.ConnectionEpoch); err != nil {
			return nil, err
		}
		if !ValidID(msg.TargetID) {
			return nil, invalidField(data, "target_id", "must be 32 lowercase hex characters")
		}
		if msg.Mode != ModePlaintext && msg.Mode != ModeEncrypted {
			return nil, invalidField(data, "mode", "must be plaintext or encrypted")
		}
		return msg, nil
	case "invite.respond":
		msg, err := decodeStrict[InviteRespond](data)
		if err != nil {
			return nil, invalid(data, "invalid invite.respond: %v", err)
		}
		if err := validateSessionCommand(data, msg.RequestID, msg.RoundID, msg.ConnectionEpoch, msg.SessionID); err != nil {
			return nil, err
		}
		if msg.Accept == nil {
			return nil, invalidField(data, "accept", "must be a boolean")
		}
		return msg, nil
	case "invite.cancel":
		msg, err := decodeStrict[InviteCancel](data)
		if err != nil {
			return nil, invalid(data, "invalid invite.cancel: %v", err)
		}
		if err := validateSessionCommand(data, msg.RequestID, msg.RoundID, msg.ConnectionEpoch, msg.SessionID); err != nil {
			return nil, err
		}
		return msg, nil
	case "session.close":
		msg, err := decodeStrict[SessionClose](data)
		if err != nil {
			return nil, invalid(data, "invalid session.close: %v", err)
		}
		if err := validateSessionCommand(data, msg.RequestID, msg.RoundID, msg.ConnectionEpoch, msg.SessionID); err != nil {
			return nil, err
		}
		if msg.Reason != "user" && msg.Reason != "leave" {
			return nil, invalidField(data, "reason", "must be user or leave")
		}
		return msg, nil
	case "key.publish":
		msg, err := decodeStrict[KeyPublish](data)
		if err != nil {
			return nil, invalid(data, "invalid key.publish: %v", err)
		}
		if err := validateSessionCommand(data, msg.RequestID, msg.RoundID, msg.ConnectionEpoch, msg.SessionID); err != nil {
			return nil, err
		}
		if _, err := checkBase64(data, "public_key_b64", msg.PublicKeyB64, 32, 32); err != nil {
			return nil, err
		}
		return msg, nil
	case "key.confirm":
		msg, err := decodeStrict[KeyConfirm](data)
		if err != nil {
			return nil, invalid(data, "invalid key.confirm: %v", err)
		}
		if err := validateSessionCommand(data, msg.RequestID, msg.RoundID, msg.ConnectionEpoch, msg.SessionID); err != nil {
			return nil, err
		}
		if err := checkSeq(data, msg.Seq); err != nil {
			return nil, err
		}
		if _, err := checkBase64(data, "nonce_b64", msg.NonceB64, 24, 24); err != nil {
			return nil, err
		}
		if _, err := checkBase64(data, "ciphertext_b64", msg.CiphertextB64, 16, limits.MaxCiphertextBytes); err != nil {
			return nil, err
		}
		return msg, nil
	case "key.ready":
		msg, err := decodeStrict[KeyReady](data)
		if err != nil {
			return nil, invalid(data, "invalid key.ready: %v", err)
		}
		if err := validateSessionCommand(data, msg.RequestID, msg.RoundID, msg.ConnectionEpoch, msg.SessionID); err != nil {
			return nil, err
		}
		if msg.Status != "ready" && msg.Status != "failed" {
			return nil, invalidField(data, "status", "must be ready or failed")
		}
		return msg, nil
	case "chat.send":
		wire, err := decodeStrict[chatSendWire](data)
		if err != nil {
			return nil, invalid(data, "invalid chat.send: %v", err)
		}
		return validateChatSend(data, wire, limits)
	case "chat.receipt":
		msg, err := decodeStrict[ChatReceipt](data)
		if err != nil {
			return nil, invalid(data, "invalid chat.receipt: %v", err)
		}
		if err := validateSessionCommand(data, msg.RequestID, msg.RoundID, msg.ConnectionEpoch, msg.SessionID); err != nil {
			return nil, err
		}
		if !ValidID(msg.MessageID) {
			return nil, invalidField(data, "message_id", "must be 32 lowercase hex characters")
		}
		if msg.Status != "processed" && msg.Status != "crypto_failed" {
			return nil, invalidField(data, "status", "must be processed or crypto_failed")
		}
		return msg, nil
	case "admin.auth":
		msg, err := decodeStrict[AdminAuth](data)
		if err != nil {
			return nil, invalid(data, "invalid admin.auth: %v", err)
		}
		if !ValidID(msg.RequestID) {
			return nil, invalidField(data, "request_id", "must be 32 lowercase hex characters")
		}
		if !ValidToken(msg.Token) {
			return nil, invalidField(data, "token", "must be a 32-byte base64url token")
		}
		return msg, nil
	case "observer.subscribe":
		msg, err := decodeStrict[ObserverSubscribe](data)
		if err != nil {
			return nil, invalid(data, "invalid observer.subscribe: %v", err)
		}
		if !ValidID(msg.RequestID) {
			return nil, invalidField(data, "request_id", "must be 32 lowercase hex characters")
		}
		if msg.SessionID != nil && !ValidID(*msg.SessionID) {
			return nil, invalidField(data, "session_id", "must be null or 32 lowercase hex characters")
		}
		return msg, nil
	case "round.reset", "class.end", "class.start":
		msg, err := decodeClassControl(data, *head.Type)
		if err != nil {
			return nil, err
		}
		return msg, nil
	case "server.shutdown":
		msg, err := decodeStrict[ServerShutdown](data)
		if err != nil {
			return nil, invalid(data, "invalid server.shutdown: %v", err)
		}
		if !ValidID(msg.RequestID) {
			return nil, invalidField(data, "request_id", "must be 32 lowercase hex characters")
		}
		return msg, nil
	default:
		return nil, invalid(data, "unsupported message type")
	}
}

type chatSendWire struct {
	SessionCommand
	MessageID     string          `json:"message_id"`
	Seq           string          `json:"seq"`
	Text          json.RawMessage `json:"text"`
	NonceB64      string          `json:"nonce_b64"`
	CiphertextB64 string          `json:"ciphertext_b64"`
}

func validateJoin(data []byte, msg *Join, limits Limits) *Error {
	if !ValidID(msg.RequestID) {
		return invalidField(data, "request_id", "must be 32 lowercase hex characters")
	}
	if !ValidID(msg.RoundID) {
		return invalidField(data, "round_id", "must be 32 lowercase hex characters")
	}
	nickname := strings.TrimSpace(msg.Nickname)
	runes := []rune(nickname)
	if len(runes) < 1 || len(runes) > limits.MaxNicknameRunes {
		return invalidField(data, "nickname", "must be 1 to %d Unicode code points", limits.MaxNicknameRunes)
	}
	for _, r := range runes {
		if unicode.IsControl(r) {
			return invalidField(data, "nickname", "must not contain control characters")
		}
	}
	return nil
}

func validateResume(data []byte, msg *Resume) *Error {
	if !ValidID(msg.RequestID) {
		return invalidField(data, "request_id", "must be 32 lowercase hex characters")
	}
	if !ValidID(msg.RoundID) {
		return invalidField(data, "round_id", "must be 32 lowercase hex characters")
	}
	if !ValidToken(msg.ResumeToken) {
		return invalidField(data, "resume_token", "must be a 32-byte base64url token")
	}
	return nil
}

func validateJoined(data []byte, requestID, roundID, epoch string) *Error {
	if !ValidID(requestID) {
		return invalidField(data, "request_id", "must be 32 lowercase hex characters")
	}
	if !ValidID(roundID) {
		return invalidField(data, "round_id", "must be 32 lowercase hex characters")
	}
	if !ValidID(epoch) {
		return invalidField(data, "connection_epoch", "must be 32 lowercase hex characters")
	}
	return nil
}

func validateSessionCommand(data []byte, requestID, roundID, epoch, sessionID string) *Error {
	if err := validateJoined(data, requestID, roundID, epoch); err != nil {
		return err
	}
	if !ValidID(sessionID) {
		return invalidField(data, "session_id", "must be 32 lowercase hex characters")
	}
	return nil
}

func validateChatSend(data []byte, wire *chatSendWire, limits Limits) (Message, *Error) {
	if err := validateSessionCommand(data, wire.RequestID, wire.RoundID, wire.ConnectionEpoch, wire.SessionID); err != nil {
		return nil, err
	}
	if !ValidID(wire.MessageID) {
		return nil, invalidField(data, "message_id", "must be 32 lowercase hex characters")
	}
	if err := checkSeq(data, wire.Seq); err != nil {
		return nil, err
	}

	textPresent := len(wire.Text) > 0
	if textPresent {
		if string(wire.Text) == "null" {
			return nil, invalidField(data, "text", "must be a string")
		}
		var text string
		if err := json.Unmarshal(wire.Text, &text); err != nil {
			return nil, invalidField(data, "text", "must be a string")
		}
		if wire.NonceB64 != "" || wire.CiphertextB64 != "" {
			return nil, invalidField(data, "text", "must not be present in encrypted messages")
		}
		if len(text) > limits.MaxBodyBytes {
			return nil, invalidField(data, "text", "exceeds %d UTF-8 bytes", limits.MaxBodyBytes)
		}
		return &ChatSend{
			SessionCommand: wire.SessionCommand,
			MessageID:      wire.MessageID,
			Seq:            wire.Seq,
			Text:           &text,
		}, nil
	}

	if wire.NonceB64 == "" || wire.CiphertextB64 == "" {
		return nil, invalidField(data, "chat.send", "requires text or nonce_b64 with ciphertext_b64")
	}
	if _, err := checkBase64(data, "nonce_b64", wire.NonceB64, 24, 24); err != nil {
		return nil, err
	}
	if _, err := checkBase64(data, "ciphertext_b64", wire.CiphertextB64, 16, limits.MaxCiphertextBytes); err != nil {
		return nil, err
	}
	return &ChatSend{
		SessionCommand: wire.SessionCommand,
		MessageID:      wire.MessageID,
		Seq:            wire.Seq,
		NonceB64:       wire.NonceB64,
		CiphertextB64:  wire.CiphertextB64,
	}, nil
}

func decodeClassControl(data []byte, messageType string) (Message, *Error) {
	switch messageType {
	case "round.reset":
		msg, err := decodeStrict[RoundReset](data)
		if err != nil {
			return nil, invalid(data, "invalid round.reset: %v", err)
		}
		if !ValidID(msg.RequestID) {
			return nil, invalidField(data, "request_id", "must be 32 lowercase hex characters")
		}
		if !ValidID(msg.ExpectedRoundID) {
			return nil, invalidField(data, "expected_round_id", "must be 32 lowercase hex characters")
		}
		return msg, nil
	case "class.end":
		msg, err := decodeStrict[ClassEnd](data)
		if err != nil {
			return nil, invalid(data, "invalid class.end: %v", err)
		}
		if !ValidID(msg.RequestID) {
			return nil, invalidField(data, "request_id", "must be 32 lowercase hex characters")
		}
		if !ValidID(msg.ExpectedRoundID) {
			return nil, invalidField(data, "expected_round_id", "must be 32 lowercase hex characters")
		}
		return msg, nil
	case "class.start":
		msg, err := decodeStrict[ClassStart](data)
		if err != nil {
			return nil, invalid(data, "invalid class.start: %v", err)
		}
		if !ValidID(msg.RequestID) {
			return nil, invalidField(data, "request_id", "must be 32 lowercase hex characters")
		}
		if !ValidID(msg.ExpectedRoundID) {
			return nil, invalidField(data, "expected_round_id", "must be 32 lowercase hex characters")
		}
		return msg, nil
	default:
		return nil, invalid(data, "unsupported class control")
	}
}

func checkSeq(data []byte, value string) *Error {
	if _, err := ParseSeq(value); err != nil {
		return invalidField(data, "seq", "%v", err)
	}
	return nil
}

func checkBase64(data []byte, field, value string, min, max int) ([]byte, *Error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, invalidField(data, field, "must be standard Base64")
	}
	if len(decoded) < min || (max > 0 && len(decoded) > max) {
		if min == max {
			return nil, invalidField(data, field, "must decode to %d bytes", min)
		}
		return nil, invalidField(data, field, "must decode to %d to %d bytes", min, max)
	}
	return decoded, nil
}

func decodeStrict[T any](data []byte) (*T, error) {
	var out T
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	if err := expectEOF(dec); err != nil {
		return nil, err
	}
	return &out, nil
}

func expectEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func checkNoDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := checkJSONValue(dec); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func checkJSONValue(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if seen[key] {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = true
			if err := checkJSONValue(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := checkJSONValue(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	default:
		return errors.New("unexpected JSON delimiter")
	}
}

func invalid(data []byte, format string, args ...any) *Error {
	return withContext(data, &Error{
		Code:    CodeInvalidMessage,
		Message: fmt.Sprintf(format, args...),
	})
}

func invalidField(data []byte, field, format string, args ...any) *Error {
	return withContext(data, &Error{
		Code:    CodeInvalidMessage,
		Message: field + ": " + fmt.Sprintf(format, args...),
	})
}

func withContext(data []byte, e *Error) *Error {
	if e == nil {
		return nil
	}
	var ctx struct {
		RequestID string `json:"request_id"`
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(data, &ctx) == nil {
		if e.RequestID == "" && ValidID(ctx.RequestID) {
			e.RequestID = ctx.RequestID
		}
		if e.SessionID == "" && ValidID(ctx.SessionID) {
			e.SessionID = ctx.SessionID
		}
	}
	return e
}
