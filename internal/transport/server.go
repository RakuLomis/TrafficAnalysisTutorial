package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/config"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/platform"
	"github.com/coder/websocket"
)

type Info struct {
	Version    string
	RoundID    string
	StudentURL string
	Candidates []platform.Candidate
}

type Server struct {
	cfg           config.Config
	info          Info
	logger        *slog.Logger
	studentStatic http.Handler
	teacherStatic http.Handler

	studentServer *http.Server
	teacherServer *http.Server
	studentLn     net.Listener
	teacherLn     net.Listener
}

func New(cfg config.Config, studentStatic, teacherStatic http.Handler, info Info, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		cfg:           cfg,
		info:          info,
		logger:        logger,
		studentStatic: studentStatic,
		teacherStatic: teacherStatic,
	}
}

func (s *Server) Start() error {
	studentLn, err := net.Listen("tcp", s.cfg.StudentAddr)
	if err != nil {
		return fmt.Errorf("student listener %s: %w", s.cfg.StudentAddr, err)
	}
	teacherLn, err := net.Listen("tcp", s.cfg.TeacherAddr)
	if err != nil {
		_ = studentLn.Close()
		return fmt.Errorf("teacher listener %s: %w", s.cfg.TeacherAddr, err)
	}

	s.studentLn = studentLn
	s.teacherLn = teacherLn
	s.studentServer = &http.Server{
		Handler:           s.studentHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.teacherServer = &http.Server{
		Handler:           s.teacherHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		if err := s.studentServer.Serve(studentLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("student server stopped", "error", err)
		}
	}()
	go func() {
		if err := s.teacherServer.Serve(teacherLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("teacher server stopped", "error", err)
		}
	}()
	return nil
}

func (s *Server) Close(ctx context.Context) error {
	var errs []error
	if s.studentServer != nil {
		if err := s.studentServer.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("shutdown student server: %w", err))
			if s.studentLn != nil {
				_ = s.studentLn.Close()
			}
		}
	}
	if s.teacherServer != nil {
		if err := s.teacherServer.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("shutdown teacher server: %w", err))
			if s.teacherLn != nil {
				_ = s.teacherLn.Close()
			}
		}
	}
	return errors.Join(errs...)
}

func (s *Server) StudentAddr() net.Addr {
	if s.studentLn == nil {
		return nil
	}
	return s.studentLn.Addr()
}

func (s *Server) TeacherAddr() net.Addr {
	if s.teacherLn == nil {
		return nil
	}
	return s.teacherLn.Addr()
}

func (s *Server) studentHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		s.writeJSON(w, s.publicConfig())
	})
	mux.HandleFunc("/ws", s.handleStudentWS)
	mux.Handle("/", s.studentStatic)
	return mux
}

func (s *Server) teacherHandler() http.Handler {
	mux := http.NewServeMux()
	handler := func(w http.ResponseWriter, r *http.Request) {
		s.writeJSON(w, s.teacherInfo())
	}
	mux.HandleFunc("/api/config", handler)
	mux.HandleFunc("/api/teacher-info", handler)
	mux.Handle("/", s.teacherStatic)
	return requireLoopbackHost(mux)
}

func (s *Server) handleStudentWS(w http.ResponseWriter, r *http.Request) {
	if err := CheckOrigin(r); err != nil {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(int64(s.cfg.MaxWSMessageBytes))

	for {
		typ, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			_ = conn.Close(websocket.StatusUnsupportedData, "text messages only")
			return
		}
		if len(data) > s.cfg.MaxWSMessageBytes {
			_ = conn.Close(websocket.StatusMessageTooBig, "message too large")
			return
		}
		writeCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		err = conn.Write(writeCtx, websocket.MessageText, data)
		cancel()
		if err != nil {
			return
		}
	}
}

type publicRound struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type publicLimits struct {
	MaxNicknameRunes          int   `json:"max_nickname_runes"`
	MaxBodyBytes              int   `json:"max_body_bytes"`
	MaxCiphertextBytes        int   `json:"max_ciphertext_bytes"`
	MaxSessionsPerParticipant int   `json:"max_sessions_per_participant"`
	InviteTimeoutMS           int64 `json:"invite_timeout_ms"`
	KeyTimeoutMS              int64 `json:"key_timeout_ms"`
	AckTimeoutMS              int64 `json:"ack_timeout_ms"`
	HeartbeatMS               int64 `json:"heartbeat_ms"`
	RenderIntervalMS          int64 `json:"render_interval_ms"`
	ClientHistoryPerSession   int   `json:"client_history_per_session"`
	ClientCacheBytes          int   `json:"client_cache_bytes"`
}

type publicCapabilities struct {
	Plaintext  bool `json:"plaintext"`
	Encryption bool `json:"encryption"`
	Resume     bool `json:"resume"`
}

type publicConfig struct {
	V               int                `json:"v"`
	ProtocolVersion int                `json:"protocol_version"`
	ServerVersion   string             `json:"server_version"`
	Round           publicRound        `json:"round"`
	Limits          publicLimits       `json:"limits"`
	Capabilities    publicCapabilities `json:"capabilities"`
}

type ifaceCandidate struct {
	Name      string `json:"name"`
	IP        string `json:"ip"`
	Loopback  bool   `json:"loopback"`
	LinkLocal bool   `json:"link_local"`
	Virtual   bool   `json:"virtual"`
	Usable    bool   `json:"usable"`
}

type teacherInfo struct {
	publicConfig
	StudentURL      string           `json:"student_url"`
	StudentPort     int              `json:"student_port"`
	IfaceCandidates []ifaceCandidate `json:"iface_candidates"`
}

func (s *Server) publicConfig() publicConfig {
	return publicConfig{
		V:               1,
		ProtocolVersion: 1,
		ServerVersion:   s.info.Version,
		Round:           publicRound{ID: s.info.RoundID, Status: "RUNNING"},
		Limits: publicLimits{
			MaxNicknameRunes:          s.cfg.MaxNicknameRunes,
			MaxBodyBytes:              s.cfg.MaxBodyBytes,
			MaxCiphertextBytes:        s.cfg.MaxCiphertextBytes,
			MaxSessionsPerParticipant: s.cfg.MaxSessionsPerParticipant,
			InviteTimeoutMS:           s.cfg.InviteTimeout.Milliseconds(),
			KeyTimeoutMS:              s.cfg.KeyTimeout.Milliseconds(),
			AckTimeoutMS:              s.cfg.AckTimeout.Milliseconds(),
			HeartbeatMS:               s.cfg.HeartbeatInterval.Milliseconds(),
			RenderIntervalMS:          s.cfg.RenderInterval.Milliseconds(),
			ClientHistoryPerSession:   s.cfg.ClientHistoryPerSession,
			ClientCacheBytes:          s.cfg.ClientCacheBytes,
		},
		Capabilities: publicCapabilities{Plaintext: true, Encryption: true, Resume: true},
	}
}

func (s *Server) teacherInfo() teacherInfo {
	info := teacherInfo{
		publicConfig:    s.publicConfig(),
		StudentURL:      s.info.StudentURL,
		StudentPort:     studentPort(s.cfg.StudentAddr),
		IfaceCandidates: make([]ifaceCandidate, 0, len(s.info.Candidates)),
	}
	for _, c := range s.info.Candidates {
		info.IfaceCandidates = append(info.IfaceCandidates, ifaceCandidate{
			Name:      c.Name,
			IP:        c.IP.String(),
			Loopback:  c.Loopback,
			LinkLocal: c.LinkLocal,
			Virtual:   c.Virtual,
			Usable:    c.Usable(),
		})
	}
	return info
}

func (s *Server) writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(value)
}

func studentPort(addr string) int {
	_, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return 0
	}
	return port
}

func requireLoopbackHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHostPort(r.Host) {
			http.Error(w, "teacher endpoint is loopback only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHostPort(hostport string) bool {
	host := hostport
	if parsed, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
