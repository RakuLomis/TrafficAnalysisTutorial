package transport

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/config"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/web"
	"github.com/coder/websocket"
)

func testConfig() config.Config {
	cfg := config.Default()
	cfg.StudentAddr = "127.0.0.1:0"
	cfg.TeacherAddr = "127.0.0.1:0"
	cfg.MaxWSMessageBytes = 1024
	return cfg
}

func testServer(t *testing.T) *Server {
	t.Helper()
	cfg := testConfig()
	return New(cfg, web.StudentHandler(), web.TeacherHandler(), Info{
		Version:    "test",
		RoundID:    "00112233445566778899aabbccddeeff",
		StudentURL: "http://192.168.5.5:8080/",
	}, nil)
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
	if _, ok := body["iface_candidates"]; ok {
		t.Error("public config leaked iface_candidates")
	}
	if _, ok := body["resume_token"]; ok {
		t.Error("public config leaked resume_token")
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

func TestEchoRoundTrip(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.studentHandler())
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{server.URL}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	if resp.Header.Get("Sec-WebSocket-Extensions") != "" {
		t.Fatalf("compression negotiated: %q", resp.Header.Get("Sec-WebSocket-Extensions"))
	}

	payload := `{"v":1,"type":"echo","text":"ping"}`
	if err := conn.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
		t.Fatalf("write: %v", err)
	}
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("type = %v", typ)
	}
	if string(data) != payload {
		t.Fatalf("echo = %q, want %q", data, payload)
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{server.URL}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	oversized := strings.Repeat("x", 2048)
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{server.URL}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	if err := conn.Write(ctx, websocket.MessageBinary, []byte{1, 2, 3}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("read succeeded, want connection close")
	}
}

func TestTeacherHasNoStudentWebSocket(t *testing.T) {
	s := testServer(t)
	server := httptest.NewServer(s.teacherHandler())
	defer server.Close()

	for _, path := range []string{"/ws", "/ws/admin"} {
		req, _ := http.NewRequest(http.MethodGet, server.URL+path, nil)
		req.Host = "127.0.0.1"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", path, resp.StatusCode)
		}
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
	s := New(cfg, web.StudentHandler(), web.TeacherHandler(), Info{Version: "test", RoundID: "00"}, nil)
	if err := s.Start(); err == nil {
		t.Fatal("Start succeeded, want port conflict error")
	}
	if s.TeacherAddr() != nil {
		t.Fatal("teacher listener left bound after student failure")
	}
}
