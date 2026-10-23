package protocol

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

const (
	testRoundID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testReqID   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testEpoch   = "cccccccccccccccccccccccccccccccc"
	testSessID  = "dddddddddddddddddddddddddddddddd"
	testMsgID   = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

func testToken() string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
}

func b64(n int, fill byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, n))
}

func parse(t *testing.T, body string) (Message, *Error) {
	t.Helper()
	return ParseClient([]byte(body), DefaultLimits())
}

func requireCode(t *testing.T, err *Error, want Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error %s, got nil", want)
	}
	if err.Code != want {
		t.Fatalf("error code = %s (%s), want %s", err.Code, err.Message, want)
	}
}

func TestParseJoin(t *testing.T) {
	msg, err := parse(t, fmt.Sprintf(`{"v":1,"type":"join","request_id":%q,"round_id":%q,"nickname":"小明"}`, testReqID, testRoundID))
	if err != nil {
		t.Fatalf("ParseJoin error = %v", err)
	}
	join, ok := msg.(*Join)
	if !ok {
		t.Fatalf("message type = %T", msg)
	}
	if join.Nickname != "小明" {
		t.Fatalf("nickname = %q", join.Nickname)
	}
	if join.MessageType() != "join" {
		t.Fatalf("MessageType = %q", join.MessageType())
	}
}

func TestJoinRejectsExtraEnvelopeFields(t *testing.T) {
	_, err := parse(t, fmt.Sprintf(`{"v":1,"type":"join","request_id":%q,"round_id":%q,"connection_epoch":%q,"nickname":"a"}`, testReqID, testRoundID, testEpoch))
	requireCode(t, err, CodeInvalidMessage)
}

func TestJoinValidatesNickname(t *testing.T) {
	base := func(nickname string) string {
		return fmt.Sprintf(`{"v":1,"type":"join","request_id":%q,"round_id":%q,"nickname":%s}`, testReqID, testRoundID, nickname)
	}
	if _, err := parse(t, base(`"一二三四五六七八九十一二三四五六"`)); err != nil {
		t.Fatalf("16-rune nickname rejected: %v", err)
	}
	for _, nickname := range []string{`""`, `"   "`, `"一二三四五六七八九十一二三四五六七"`, `"a\u0000b"`, `"a\tb"`} {
		_, err := parse(t, base(nickname))
		requireCode(t, err, CodeInvalidMessage)
	}
}

func TestParseResume(t *testing.T) {
	msg, err := parse(t, fmt.Sprintf(`{"v":1,"type":"resume","request_id":%q,"round_id":%q,"resume_token":%q}`, testReqID, testRoundID, testToken()))
	if err != nil {
		t.Fatalf("ParseResume error = %v", err)
	}
	resume, ok := msg.(*Resume)
	if !ok {
		t.Fatalf("message type = %T", msg)
	}
	if resume.ResumeToken != testToken() {
		t.Fatalf("token = %q", resume.ResumeToken)
	}
}

func TestResumeRejectsInvalidToken(t *testing.T) {
	for _, token := range []string{"", "short", strings.Repeat("A", 42), strings.Repeat("!", 43)} {
		_, err := parse(t, fmt.Sprintf(`{"v":1,"type":"resume","request_id":%q,"round_id":%q,"resume_token":%q}`, testReqID, testRoundID, token))
		requireCode(t, err, CodeInvalidMessage)
	}
}

func TestParseCommonRejections(t *testing.T) {
	cases := []struct {
		name string
		body string
		code Code
	}{
		{"version", fmt.Sprintf(`{"v":2,"type":"join","request_id":%q,"round_id":%q,"nickname":"a"}`, testReqID, testRoundID), CodeVersionMismatch},
		{"missing version", fmt.Sprintf(`{"type":"join","request_id":%q,"round_id":%q,"nickname":"a"}`, testReqID, testRoundID), CodeInvalidMessage},
		{"missing type", fmt.Sprintf(`{"v":1,"request_id":%q,"round_id":%q,"nickname":"a"}`, testReqID, testRoundID), CodeInvalidMessage},
		{"unknown type", fmt.Sprintf(`{"v":1,"type":"nope","request_id":%q}`, testReqID), CodeInvalidMessage},
		{"array", `[]`, CodeInvalidMessage},
		{"scalar", `1`, CodeInvalidMessage},
		{"trailing", fmt.Sprintf(`{"v":1,"type":"ping"} {}`), CodeInvalidMessage},
		{"unknown field", fmt.Sprintf(`{"v":1,"type":"join","request_id":%q,"round_id":%q,"nickname":"a","extra":1}`, testReqID, testRoundID), CodeInvalidMessage},
		{"duplicate field", fmt.Sprintf(`{"v":1,"type":"join","request_id":%q,"round_id":%q,"nickname":"a","nickname":"b"}`, testReqID, testRoundID), CodeInvalidMessage},
		{"invalid request id", fmt.Sprintf(`{"v":1,"type":"join","request_id":"bad","round_id":%q,"nickname":"a"}`, testRoundID), CodeInvalidMessage},
		{"invalid round id", fmt.Sprintf(`{"v":1,"type":"join","request_id":%q,"round_id":"bad","nickname":"a"}`, testReqID), CodeInvalidMessage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parse(t, tc.body)
			requireCode(t, err, tc.code)
		})
	}
}

func TestParseRejectsInvalidUTF8(t *testing.T) {
	body := []byte(fmt.Sprintf(`{"v":1,"type":"join","request_id":%q,"round_id":%q,"nickname":"`, testReqID, testRoundID))
	body = append(body, 0xff, '"', '}')
	_, err := ParseClient(body, DefaultLimits())
	requireCode(t, err, CodeInvalidMessage)
}

func TestParseRejectsOversizedWSMessage(t *testing.T) {
	body := fmt.Sprintf(`{"v":1,"type":"join","request_id":%q,"round_id":%q,"nickname":"%s"}`, testReqID, testRoundID, strings.Repeat("a", 70000))
	_, err := parse(t, body)
	requireCode(t, err, CodeMessageTooLarge)
}

func TestParseInviteCreate(t *testing.T) {
	body := fmt.Sprintf(`{"v":1,"type":"invite.create","request_id":%q,"round_id":%q,"connection_epoch":%q,"target_id":"%s","mode":"encrypted"}`, testReqID, testRoundID, testEpoch, strings.Repeat("1", 32))
	msg, err := parse(t, body)
	if err != nil {
		t.Fatalf("ParseInviteCreate error = %v", err)
	}
	invite, ok := msg.(*InviteCreate)
	if !ok {
		t.Fatalf("message type = %T", msg)
	}
	if invite.Mode != ModeEncrypted {
		t.Fatalf("mode = %q", invite.Mode)
	}

	for _, bad := range []string{
		fmt.Sprintf(`{"v":1,"type":"invite.create","request_id":%q,"round_id":%q,"connection_epoch":%q,"target_id":"bad","mode":"encrypted"}`, testReqID, testRoundID, testEpoch),
		fmt.Sprintf(`{"v":1,"type":"invite.create","request_id":%q,"round_id":%q,"connection_epoch":%q,"target_id":"%s","mode":"both"}`, testReqID, testRoundID, testEpoch, strings.Repeat("1", 32)),
		fmt.Sprintf(`{"v":1,"type":"invite.create","request_id":%q,"round_id":%q,"connection_epoch":%q,"mode":"plaintext"}`, testReqID, testRoundID, testEpoch),
	} {
		_, err := parse(t, bad)
		requireCode(t, err, CodeInvalidMessage)
	}
}

func TestParseInviteRespond(t *testing.T) {
	base := func(accept string) string {
		return fmt.Sprintf(`{"v":1,"type":"invite.respond","request_id":%q,"round_id":%q,"connection_epoch":%q,"session_id":%q,"accept":%s}`, testReqID, testRoundID, testEpoch, testSessID, accept)
	}
	msg, err := parse(t, base("true"))
	if err != nil {
		t.Fatalf("ParseInviteRespond error = %v", err)
	}
	respond := msg.(*InviteRespond)
	if respond.Accept == nil || !*respond.Accept {
		t.Fatalf("accept = %v", respond.Accept)
	}
	if _, err := parse(t, base("false")); err != nil {
		t.Fatalf("false rejected: %v", err)
	}
	for _, accept := range []string{`"true"`, `1`, `null`} {
		_, err := parse(t, base(accept))
		requireCode(t, err, CodeInvalidMessage)
	}
	_, err = parse(t, fmt.Sprintf(`{"v":1,"type":"invite.respond","request_id":%q,"round_id":%q,"connection_epoch":%q,"session_id":%q}`, testReqID, testRoundID, testEpoch, testSessID))
	requireCode(t, err, CodeInvalidMessage)
}

func TestParseSessionClose(t *testing.T) {
	body := fmt.Sprintf(`{"v":1,"type":"session.close","request_id":%q,"round_id":%q,"connection_epoch":%q,"session_id":%q,"reason":"user"}`, testReqID, testRoundID, testEpoch, testSessID)
	if _, err := parse(t, body); err != nil {
		t.Fatalf("ParseSessionClose error = %v", err)
	}
	bad := fmt.Sprintf(`{"v":1,"type":"session.close","request_id":%q,"round_id":%q,"connection_epoch":%q,"session_id":%q,"reason":"bored"}`, testReqID, testRoundID, testEpoch, testSessID)
	_, err := parse(t, bad)
	requireCode(t, err, CodeInvalidMessage)
}

func TestParseKeyPublish(t *testing.T) {
	body := fmt.Sprintf(`{"v":1,"type":"key.publish","request_id":%q,"round_id":%q,"connection_epoch":%q,"session_id":%q,"public_key_b64":%q}`, testReqID, testRoundID, testEpoch, testSessID, b64(32, 7))
	if _, err := parse(t, body); err != nil {
		t.Fatalf("ParseKeyPublish error = %v", err)
	}
	bad := fmt.Sprintf(`{"v":1,"type":"key.publish","request_id":%q,"round_id":%q,"connection_epoch":%q,"session_id":%q,"public_key_b64":%q}`, testReqID, testRoundID, testEpoch, testSessID, b64(31, 7))
	_, err := parse(t, bad)
	requireCode(t, err, CodeInvalidMessage)
}

func TestParseKeyConfirm(t *testing.T) {
	base := func(seq, nonce, cipher string) string {
		return fmt.Sprintf(`{"v":1,"type":"key.confirm","request_id":%q,"round_id":%q,"connection_epoch":%q,"session_id":%q,"seq":%q,"nonce_b64":%q,"ciphertext_b64":%q}`, testReqID, testRoundID, testEpoch, testSessID, seq, nonce, cipher)
	}
	if _, err := parse(t, base("0", b64(24, 1), b64(16, 2))); err != nil {
		t.Fatalf("valid key.confirm rejected: %v", err)
	}
	for _, seq := range []string{"00", "01", "-1", "18446744073709551616"} {
		_, err := parse(t, base(seq, b64(24, 1), b64(16, 2)))
		requireCode(t, err, CodeInvalidMessage)
	}
	for _, body := range []string{
		base("0", b64(23, 1), b64(16, 2)),
		base("0", "!!!!", b64(16, 2)),
		base("0", b64(24, 1), b64(15, 2)),
	} {
		_, err := parse(t, body)
		requireCode(t, err, CodeInvalidMessage)
	}
}

func TestParseKeyReady(t *testing.T) {
	base := func(status string) string {
		return fmt.Sprintf(`{"v":1,"type":"key.ready","request_id":%q,"round_id":%q,"connection_epoch":%q,"session_id":%q,"status":%q}`, testReqID, testRoundID, testEpoch, testSessID, status)
	}
	for _, status := range []string{"ready", "failed"} {
		if _, err := parse(t, base(status)); err != nil {
			t.Fatalf("status %q rejected: %v", status, err)
		}
	}
	_, err := parse(t, base("maybe"))
	requireCode(t, err, CodeInvalidMessage)
}

func TestParseChatPlaintext(t *testing.T) {
	base := func(text string) string {
		return fmt.Sprintf(`{"v":1,"type":"chat.send","request_id":%q,"round_id":%q,"connection_epoch":%q,"session_id":%q,"message_id":%q,"seq":"1","text":%s}`, testReqID, testRoundID, testEpoch, testSessID, testMsgID, text)
	}
	msg, err := parse(t, base(`"你好"`))
	if err != nil {
		t.Fatalf("ParseChatPlaintext error = %v", err)
	}
	chat := msg.(*ChatSend)
	if chat.Text == nil || *chat.Text != "你好" {
		t.Fatalf("text = %v", chat.Text)
	}
	if chat.NonceB64 != "" || chat.CiphertextB64 != "" {
		t.Fatal("plaintext chat contains cipher fields")
	}

	if _, err := parse(t, base(`"`+strings.Repeat("a", 4096)+`"`)); err != nil {
		t.Fatalf("4096-byte text rejected: %v", err)
	}
	_, err = parse(t, base(`"`+strings.Repeat("a", 4097)+`"`))
	requireCode(t, err, CodeInvalidMessage)

	if _, err := parse(t, base(`"`+strings.Repeat("你", 1365)+`"`)); err != nil {
		t.Fatalf("4095-byte Chinese text rejected: %v", err)
	}
	_, err = parse(t, base(`"`+strings.Repeat("你", 1366)+`"`))
	requireCode(t, err, CodeInvalidMessage)

	if _, err := parse(t, base(`"`+strings.Repeat("😀", 1024)+`"`)); err != nil {
		t.Fatalf("4096-byte emoji text rejected: %v", err)
	}
	_, err = parse(t, base(`"`+strings.Repeat("😀", 1025)+`"`))
	requireCode(t, err, CodeInvalidMessage)

	escaped := `"line1\nline2 \"quoted\" \\ slash"`
	if _, err := parse(t, base(escaped)); err != nil {
		t.Fatalf("escaped text rejected: %v", err)
	}
}

func TestParseChatEncrypted(t *testing.T) {
	base := func(extra string) string {
		return fmt.Sprintf(`{"v":1,"type":"chat.send","request_id":%q,"round_id":%q,"connection_epoch":%q,"session_id":%q,"message_id":%q,"seq":"3",%s}`, testReqID, testRoundID, testEpoch, testSessID, testMsgID, extra)
	}
	valid := base(fmt.Sprintf(`"nonce_b64":%q,"ciphertext_b64":%q`, b64(24, 1), b64(32, 2)))
	msg, err := parse(t, valid)
	if err != nil {
		t.Fatalf("ParseChatEncrypted error = %v", err)
	}
	chat := msg.(*ChatSend)
	if chat.Text != nil {
		t.Fatal("encrypted chat has text")
	}
	if chat.NonceB64 == "" || chat.CiphertextB64 == "" {
		t.Fatal("encrypted chat missing cipher fields")
	}

	for _, body := range []string{
		base(fmt.Sprintf(`"text":"hello","nonce_b64":%q,"ciphertext_b64":%q`, b64(24, 1), b64(32, 2))),
		base(`"text":null`),
		base(fmt.Sprintf(`"nonce_b64":%q`, b64(24, 1))),
		base(fmt.Sprintf(`"nonce_b64":%q,"ciphertext_b64":%q,"private_key":"x"`, b64(24, 1), b64(32, 2))),
		base(fmt.Sprintf(`"nonce_b64":%q,"ciphertext_b64":%q`, b64(23, 1), b64(32, 2))),
		base(fmt.Sprintf(`"nonce_b64":%q,"ciphertext_b64":%q`, b64(24, 1), b64(15, 2))),
		base(fmt.Sprintf(`"nonce_b64":%q,"ciphertext_b64":%q`, "!!!!", b64(32, 2))),
	} {
		_, err := parse(t, body)
		requireCode(t, err, CodeInvalidMessage)
	}
}

func TestParseChatReceipt(t *testing.T) {
	base := func(status string) string {
		return fmt.Sprintf(`{"v":1,"type":"chat.receipt","request_id":%q,"round_id":%q,"connection_epoch":%q,"session_id":%q,"message_id":%q,"status":%q}`, testReqID, testRoundID, testEpoch, testSessID, testMsgID, status)
	}
	for _, status := range []string{"processed", "crypto_failed"} {
		if _, err := parse(t, base(status)); err != nil {
			t.Fatalf("status %q rejected: %v", status, err)
		}
	}
	_, err := parse(t, base("done"))
	requireCode(t, err, CodeInvalidMessage)
}

func TestParseAdminAuth(t *testing.T) {
	if _, err := parse(t, fmt.Sprintf(`{"v":1,"type":"admin.auth","request_id":%q,"token":%q}`, testReqID, testToken())); err != nil {
		t.Fatalf("valid admin.auth rejected: %v", err)
	}
	_, err := parse(t, fmt.Sprintf(`{"v":1,"type":"admin.auth","request_id":%q,"token":"short"}`, testReqID))
	requireCode(t, err, CodeInvalidMessage)
}

func TestParseObserverSubscribe(t *testing.T) {
	base := func(session string) string {
		return fmt.Sprintf(`{"v":1,"type":"observer.subscribe","request_id":%q,"session_id":%s}`, testReqID, session)
	}
	msg, err := parse(t, base("null"))
	if err != nil {
		t.Fatalf("null subscription rejected: %v", err)
	}
	if msg.(*ObserverSubscribe).SessionID != nil {
		t.Fatal("null session id should be nil")
	}
	msg, err = parse(t, base(fmt.Sprintf("%q", testSessID)))
	if err != nil {
		t.Fatalf("session subscription rejected: %v", err)
	}
	if msg.(*ObserverSubscribe).SessionID == nil || *msg.(*ObserverSubscribe).SessionID != testSessID {
		t.Fatal("session id not parsed")
	}
	_, err = parse(t, base(`"bad"`))
	requireCode(t, err, CodeInvalidMessage)
}

func TestParseRoundReset(t *testing.T) {
	base := func(expected string) string {
		return fmt.Sprintf(`{"v":1,"type":"round.reset","request_id":%q,"expected_round_id":%s}`, testReqID, expected)
	}
	if _, err := parse(t, base(fmt.Sprintf("%q", testRoundID))); err != nil {
		t.Fatalf("valid round.reset rejected: %v", err)
	}
	for _, expected := range []string{`"bad"`, `null`, `123`} {
		_, err := parse(t, base(expected))
		requireCode(t, err, CodeInvalidMessage)
	}
	_, err := parse(t, fmt.Sprintf(`{"v":1,"type":"round.reset","request_id":%q}`, testReqID))
	requireCode(t, err, CodeInvalidMessage)
}

func TestParseClassControls(t *testing.T) {
	for _, typ := range []string{"class.end", "class.start"} {
		body := fmt.Sprintf(`{"v":1,"type":%q,"request_id":%q,"expected_round_id":%q}`, typ, testReqID, testRoundID)
		if _, err := parse(t, body); err != nil {
			t.Fatalf("%s rejected: %v", typ, err)
		}
	}
	if _, err := parse(t, fmt.Sprintf(`{"v":1,"type":"server.shutdown","request_id":%q}`, testReqID)); err != nil {
		t.Fatalf("server.shutdown rejected: %v", err)
	}
}

func TestParsePing(t *testing.T) {
	if _, err := parse(t, `{"v":1,"type":"ping"}`); err != nil {
		t.Fatalf("ping rejected: %v", err)
	}
	_, err := parse(t, fmt.Sprintf(`{"v":1,"type":"ping","request_id":%q}`, testReqID))
	requireCode(t, err, CodeInvalidMessage)
}

func TestParseLeave(t *testing.T) {
	body := fmt.Sprintf(`{"v":1,"type":"leave","request_id":%q,"round_id":%q,"connection_epoch":%q}`, testReqID, testRoundID, testEpoch)
	if _, err := parse(t, body); err != nil {
		t.Fatalf("leave rejected: %v", err)
	}
	bad := fmt.Sprintf(`{"v":1,"type":"leave","request_id":%q,"round_id":%q}`, testReqID, testRoundID)
	_, err := parse(t, bad)
	requireCode(t, err, CodeInvalidMessage)
}

func TestErrorCarriesContext(t *testing.T) {
	body := fmt.Sprintf(`{"v":1,"type":"chat.send","request_id":%q,"round_id":%q,"connection_epoch":%q,"session_id":%q,"message_id":%q,"seq":"00","text":"x"}`, testReqID, testRoundID, testEpoch, testSessID, testMsgID)
	_, err := parse(t, body)
	requireCode(t, err, CodeInvalidMessage)
	if err.RequestID != testReqID {
		t.Errorf("error request_id = %q", err.RequestID)
	}
	if err.SessionID != testSessID {
		t.Errorf("error session_id = %q", err.SessionID)
	}
}
