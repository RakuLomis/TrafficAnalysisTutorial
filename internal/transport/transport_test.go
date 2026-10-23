package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/config"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/hub"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/protocol"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/web"
	"github.com/coder/websocket"
)

const testAdminToken = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func testConfig() config.Config {
	cfg := config.Default()
	cfg.StudentAddr = "127.0.0.1:0"
	cfg.TeacherAddr = "127.0.0.1:0"
	cfg.MaxWSMessageBytes = 1024
	cfg.JoinTimeout = 5 * time.Second
	cfg.HeartbeatInterval = time.Second
	cfg.DeadTimeout = 3 * time.Second
	return cfg
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testServerWithConfig(t *testing.T, mutate func(*config.Config)) *Server {
	t.Helper()
	cfg := testConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	h, err := hub.NewWithOptions(cfg, discardLogger(), hub.Options{
		AdminToken:    testAdminToken,
		ServerVersion: "test",
	})
	if err != nil {
		t.Fatalf("hub.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go h.Run(ctx)
	t.Cleanup(func() {
		cancel()
		<-h.Done()
	})
	return New(cfg, web.StudentHandler(), web.TeacherHandler(), Info{
		Version:    "test",
		RoundID:    h.RoundID(),
		StudentURL: "http://192.168.5.5:8080/",
		Hub:        h,
	}, discardLogger())
}

func testServer(t *testing.T) *Server {
	return testServerWithConfig(t, nil)
}

func dialWS(t *testing.T, serverURL, path string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(serverURL, "http")+path, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{serverURL}},
	})
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	return conn
}

func writeMessage(t *testing.T, conn *websocket.Conn, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readMessage[T any](t *testing.T, conn *websocket.Conn) T {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("message type = %v", typ)
	}
	var target T
	if err := json.Unmarshal(data, &target); err != nil {
		t.Fatalf("unmarshal %s: %v", data, err)
	}
	return target
}

func TestCheckOrigin(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		host   string
		wantOK bool
	}{
		{"same host", "http://192.168.5.5:8080", "192.168.5.5:8080", true},
		{"same host https", "https://192.168.5.5:8080", "192.168.5.5:8080", true},
		{"different host", "http://evil.example", "192.168.5.5:8080", false},
		{"different port", "http://192.168.5.5:9999", "192.168.5.5:8080", false},
		{"missing", "", "192.168.5.5:8080", false},
		{"opaque null", "null", "192.168.5.5:8080", false},
		{"not http", "file:///tmp/x", "192.168.5.5:8080", false},
		{"malformed", "://bad", "192.168.5.5:8080", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://"+tc.host+"/ws", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			err := CheckOrigin(req)
			if (err == nil) != tc.wantOK {
				t.Fatalf("CheckOrigin() error = %v, wantOK = %v", err, tc.wantOK)
			}
		})
	}
}

func TestPublicConfigEndpoint(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.studentHandler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/config")
	if err != nil {
		t.Fatalf("GET config: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	data, _ := io.ReadAll(resp.Body)
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("json: %v (%s)", err, data)
	}
	if body["protocol_version"].(float64) != 1 {
		t.Errorf("protocol_version = %v", body["protocol_version"])
	}
	limits := body["limits"].(map[string]any)
	if limits["max_body_bytes"].(float64) != 4096 {
		t.Errorf("max_body_bytes = %v", limits["max_body_bytes"])
	}
	round := body["round"].(map[string]any)
	if round["id"] != s.info.Hub.RoundID() {
		t.Errorf("round id = %v, want %s", round["id"], s.info.Hub.RoundID())
	}
	if _, ok := body["iface_candidates"]; ok {
		t.Error("public config leaked iface_candidates")
	}
}

func TestTeacherConfigIncludesAddressAndCandidates(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.teacherHandler())
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/config", nil)
	req.Host = "127.0.0.1"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET teacher config: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("json: %v (%s)", err, data)
	}
	if body["student_url"] != "http://192.168.5.5:8080/" {
		t.Errorf("student_url = %v", body["student_url"])
	}
}

func TestTeacherRejectsNonLoopbackHost(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.teacherHandler())
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/config", nil)
	req.Host = "evil.example:8081"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.studentHandler())
	defer server.Close()
	resp, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(data)) != "ok" {
		t.Fatalf("body = %q", data)
	}
}

func TestPingRoundTrip(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.studentHandler())
	defer server.Close()

	conn := dialWS(t, server.URL, "/ws")
	defer conn.CloseNow()
	writeMessage(t, conn, &protocol.Ping{V: 1, Type: "ping"})
	pong := readMessage[*protocol.Pong](t, conn)
	if pong.Type != "pong" || pong.ServerTimeUTC == "" {
		t.Fatalf("pong = %+v", pong)
	}
}

func TestJoinRoundTrip(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.studentHandler())
	defer server.Close()

	conn := dialWS(t, server.URL, "/ws")
	defer conn.CloseNow()
	join := &protocol.Join{
		V:         1,
		Type:      "join",
		RequestID: mustID(t),
		RoundID:   s.info.Hub.RoundID(),
		Nickname:  "小明",
	}
	writeMessage(t, conn, join)
	welcome := readMessage[*protocol.Welcome](t, conn)
	if welcome.RequestID != join.RequestID {
		t.Errorf("request_id = %q", welcome.RequestID)
	}
	if welcome.Nickname != "小明" {
		t.Errorf("nickname = %q", welcome.Nickname)
	}
	if !protocol.ValidID(welcome.ParticipantID) || !protocol.ValidToken(welcome.ResumeToken) {
		t.Errorf("identity = %+v", welcome)
	}
	if welcome.ConnectionEpoch == "" {
		t.Error("missing connection epoch")
	}
}

func TestInvalidMessageGetsStructuredError(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.studentHandler())
	defer server.Close()

	conn := dialWS(t, server.URL, "/ws")
	defer conn.CloseNow()
	writeMessage(t, conn, map[string]any{"v": 1, "type": "not-a-real-type"})
	errorMessage := readMessage[*protocol.ErrorMessage](t, conn)
	if errorMessage.Code != protocol.CodeInvalidMessage {
		t.Fatalf("code = %s", errorMessage.Code)
	}
}

func TestRejectsBadOrigin(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.studentHandler())
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://evil.example"}},
	})
	if err == nil {
		t.Fatal("dial succeeded, want rejection")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("response = %+v, want 403", resp)
	}
}

func TestRejectsMissingOrigin(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.studentHandler())
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", nil)
	if err == nil {
		t.Fatal("dial succeeded, want rejection")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("response = %+v, want 403", resp)
	}
}

func TestReadLimitClosesConnection(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.studentHandler())
	defer server.Close()

	conn := dialWS(t, server.URL, "/ws")
	defer conn.CloseNow()
	oversized := strings.Repeat("x", 2048)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(oversized)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("read succeeded, want connection close")
	}
}

func TestRejectsBinaryMessage(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.studentHandler())
	defer server.Close()

	conn := dialWS(t, server.URL, "/ws")
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageBinary, []byte{1, 2, 3}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("read succeeded, want connection close")
	}
}

func TestAdminAuth(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.teacherHandler())
	defer server.Close()

	conn := dialWS(t, server.URL, "/ws/admin")
	defer conn.CloseNow()
	requestID := mustID(t)
	writeMessage(t, conn, &protocol.AdminAuth{V: 1, Type: "admin.auth", RequestID: requestID, Token: testAdminToken})
	welcome := readMessage[*protocol.AdminWelcome](t, conn)
	if welcome.RequestID != requestID {
		t.Errorf("request_id = %q", welcome.RequestID)
	}
	if welcome.ServerVersion != "test" {
		t.Errorf("server version = %q", welcome.ServerVersion)
	}
	if welcome.Round.ID != s.info.Hub.RoundID() {
		t.Errorf("round = %+v", welcome.Round)
	}
}

func TestAdminAuthRejectsWrongToken(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.teacherHandler())
	defer server.Close()

	conn := dialWS(t, server.URL, "/ws/admin")
	defer conn.CloseNow()
	writeMessage(t, conn, &protocol.AdminAuth{V: 1, Type: "admin.auth", RequestID: mustID(t), Token: strings.Repeat("B", 43)})
	errorMessage := readMessage[*protocol.ErrorMessage](t, conn)
	if errorMessage.Code != protocol.CodeUnauthorized {
		t.Fatalf("code = %s", errorMessage.Code)
	}
}

func TestPublicConnectionCannotAuthenticateAsAdmin(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.studentHandler())
	defer server.Close()

	conn := dialWS(t, server.URL, "/ws")
	defer conn.CloseNow()
	writeMessage(t, conn, &protocol.AdminAuth{V: 1, Type: "admin.auth", RequestID: mustID(t), Token: testAdminToken})
	errorMessage := readMessage[*protocol.ErrorMessage](t, conn)
	if errorMessage.Code != protocol.CodeForbidden {
		t.Fatalf("code = %s", errorMessage.Code)
	}
}

func TestTeacherHasNoStudentWebSocket(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.teacherHandler())
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/ws", nil)
	req.Host = "127.0.0.1"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request /ws: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("/ws status = %d, want 404", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodGet, server.URL+"/ws/admin", nil)
	req.Host = "127.0.0.1"
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request /ws/admin: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("/ws/admin status = %d, want 403 without Origin", resp.StatusCode)
	}
}

func TestStudentHasNoAdminEndpoint(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.studentHandler())
	defer server.Close()
	for _, path := range []string{"/api/teacher-info", "/ws/admin"} {
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatalf("request %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestJoinTimeoutReleasesConnection(t *testing.T) {
	s := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.JoinTimeout = 100 * time.Millisecond
	})
	server := httptest.NewServer(s.studentHandler())
	defer server.Close()

	conn := dialWS(t, server.URL, "/ws")
	defer conn.CloseNow()
	errorMessage := readMessage[*protocol.ErrorMessage](t, conn)
	if errorMessage.Code != protocol.CodeInvalidMessage || !strings.Contains(errorMessage.Message, "join timeout") {
		t.Fatalf("error = %+v", errorMessage)
	}
}

func TestRawConnectionLimit(t *testing.T) {
	s := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.MaxRawConnections = 1
	})
	server := httptest.NewServer(s.studentHandler())
	defer server.Close()

	first := dialWS(t, server.URL, "/ws")
	defer first.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{server.URL}},
	})
	if err == nil {
		t.Fatal("second connection accepted, want capacity rejection")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("response = %+v, want 503", resp)
	}
}

func TestControlRateLimit(t *testing.T) {
	s := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.ControlRatePerSec = 1
		cfg.ControlBurst = 1
	})
	server := httptest.NewServer(s.studentHandler())
	defer server.Close()

	conn := dialWS(t, server.URL, "/ws")
	defer conn.CloseNow()
	writeMessage(t, conn, &protocol.Ping{V: 1, Type: "ping"})
	writeMessage(t, conn, &protocol.Ping{V: 1, Type: "ping"})

	var pong *protocol.Pong
	var rateLimited *protocol.ErrorMessage
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		typ, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if typ != websocket.MessageText {
			t.Fatalf("type = %v", typ)
		}
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &head); err != nil {
			t.Fatalf("json: %v", err)
		}
		switch head.Type {
		case "pong":
			var value protocol.Pong
			_ = json.Unmarshal(data, &value)
			pong = &value
		case "error":
			var value protocol.ErrorMessage
			_ = json.Unmarshal(data, &value)
			rateLimited = &value
		}
	}
	if pong == nil {
		t.Fatal("missing pong")
	}
	if rateLimited == nil || rateLimited.Code != protocol.CodeRateLimited {
		t.Fatalf("rate limit error = %+v", rateLimited)
	}
}

func TestConnectionQueueOverflow(t *testing.T) {
	cfg := testConfig()
	cfg.SendQueueCount = 1
	cfg.SendQueueBytes = 1024
	conn := &Connection{cfg: cfg, queue: make(chan []byte, 1)}
	if err := conn.Send(map[string]string{"text": strings.Repeat("a", 100)}); err != nil {
		t.Fatalf("first send: %v", err)
	}
	if err := conn.Send(map[string]string{"text": "second"}); !errors.Is(err, ErrSlowClient) {
		t.Fatalf("second send error = %v, want ErrSlowClient", err)
	}

	cfg.SendQueueCount = 10
	cfg.SendQueueBytes = 10
	conn = &Connection{cfg: cfg, queue: make(chan []byte, 10)}
	if err := conn.Send(map[string]string{"text": strings.Repeat("a", 100)}); !errors.Is(err, ErrSlowClient) {
		t.Fatalf("byte limit error = %v, want ErrSlowClient", err)
	}
	conn.closed.Store(true)
	if err := conn.Send(map[string]string{"text": "closed"}); !errors.Is(err, ErrConnectionClosed) {
		t.Fatalf("closed send error = %v", err)
	}
}

func TestStartBindsBothListeners(t *testing.T) {
	s := testServer(t)
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Close(ctx)
	})

	for name, addr := range map[string]net.Addr{"student": s.StudentAddr(), "teacher": s.TeacherAddr()} {
		if addr == nil {
			t.Fatalf("%s listener not bound", name)
		}
	}
	resp, err := http.Get("http://" + s.StudentAddr().String() + "/healthz")
	if err != nil {
		t.Fatalf("GET student healthz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("student status = %d", resp.StatusCode)
	}
}

func TestStartFailsWhenStudentPortBusy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	cfg := testConfig()
	cfg.StudentAddr = ln.Addr().String()
	s := New(cfg, web.StudentHandler(), web.TeacherHandler(), Info{Version: "test", RoundID: "00"}, discardLogger())
	if err := s.Start(); err == nil {
		t.Fatal("Start succeeded, want port conflict error")
	}
	if s.TeacherAddr() != nil {
		t.Fatal("teacher listener left bound after student failure")
	}
}

func mustID(t *testing.T) string {
	t.Helper()
	id, err := protocol.NewID()
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	return id
}
