package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	StudentAddr string
	TeacherAddr string
	IfaceIP     string
	NoOpen      bool

	PlannedStudents           int
	MaxParticipants           int
	MaxRawConnections         int
	MaxIdentities             int
	MaxTeachers               int
	MaxSessionsPerParticipant int
	MaxNicknameRunes          int

	InviteTimeout     time.Duration
	KeyTimeout        time.Duration
	HeartbeatInterval time.Duration
	DeadTimeout       time.Duration
	ResumeWindow      time.Duration
	AckTimeout        time.Duration
	WriteTimeout      time.Duration
	JoinTimeout       time.Duration

	MaxBodyBytes       int
	MaxCiphertextBytes int
	MaxWSMessageBytes  int

	SessionHistoryCount int
	SessionHistoryBytes int
	GlobalHistoryBytes  int
	ClosedSessionMax    int
	ClosedSessionTTL    time.Duration

	SendQueueCount          int
	SendQueueBytes          int
	ChatRatePerSec          int
	ChatBurst               int
	ControlRatePerSec       int
	ControlBurst            int
	ClientHistoryPerSession int
	ClientCacheBytes        int
	RenderInterval          time.Duration
}

type Parsed struct {
	Config      Config
	ShowVersion bool
}

func Default() Config {
	return Config{
		StudentAddr: "0.0.0.0:8080",
		TeacherAddr: "127.0.0.1:8081",
		NoOpen:      false,

		PlannedStudents:           50,
		MaxParticipants:           100,
		MaxRawConnections:         120,
		MaxIdentities:             200,
		MaxTeachers:               4,
		MaxSessionsPerParticipant: 3,
		MaxNicknameRunes:          16,

		InviteTimeout:     30 * time.Second,
		KeyTimeout:        15 * time.Second,
		HeartbeatInterval: 15 * time.Second,
		DeadTimeout:       45 * time.Second,
		ResumeWindow:      60 * time.Second,
		AckTimeout:        5 * time.Second,
		WriteTimeout:      5 * time.Second,
		JoinTimeout:       10 * time.Second,

		MaxBodyBytes:       4096,
		MaxCiphertextBytes: 32768,
		MaxWSMessageBytes:  65536,

		SessionHistoryCount: 200,
		SessionHistoryBytes: 1 << 20,
		GlobalHistoryBytes:  32 << 20,
		ClosedSessionMax:    200,
		ClosedSessionTTL:    10 * time.Minute,

		SendQueueCount:          64,
		SendQueueBytes:          256 << 10,
		ChatRatePerSec:          2,
		ChatBurst:               5,
		ControlRatePerSec:       10,
		ControlBurst:            20,
		ClientHistoryPerSession: 200,
		ClientCacheBytes:        3 << 20,
		RenderInterval:          100 * time.Millisecond,
	}
}

func Parse(args []string) (Parsed, error) {
	cfg := Default()
	fs := flag.NewFlagSet("traffic-classroom", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var showVersion bool
	fs.StringVar(&cfg.StudentAddr, "student-addr", cfg.StudentAddr, "student listener address")
	fs.StringVar(&cfg.TeacherAddr, "teacher-addr", cfg.TeacherAddr, "teacher listener address (loopback only)")
	fs.StringVar(&cfg.IfaceIP, "iface-ip", cfg.IfaceIP, "LAN IPv4 address shown in URL and QR code")
	fs.BoolVar(&cfg.NoOpen, "no-open", cfg.NoOpen, "do not open the teacher page automatically")
	fs.BoolVar(&showVersion, "version", false, "print version and exit")

	if err := fs.Parse(args); err != nil {
		return Parsed{}, err
	}
	if fs.NArg() > 0 {
		return Parsed{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if err := cfg.Validate(); err != nil {
		return Parsed{}, err
	}
	return Parsed{Config: cfg, ShowVersion: showVersion}, nil
}

func (c Config) Validate() error {
	if err := validateListenAddr("student_addr", c.StudentAddr, false); err != nil {
		return err
	}
	if err := validateListenAddr("teacher_addr", c.TeacherAddr, true); err != nil {
		return err
	}
	if c.IfaceIP != "" {
		ip := net.ParseIP(c.IfaceIP)
		if ip == nil || ip.To4() == nil {
			return fieldErr("iface_ip", "must be an IPv4 address, got %q", c.IfaceIP)
		}
		if ip.IsLoopback() || ip.IsUnspecified() {
			return fieldErr("iface_ip", "must be a LAN address, got %q", c.IfaceIP)
		}
	}

	if c.PlannedStudents < 1 || c.PlannedStudents > 100 {
		return fieldErr("planned_students", "must be between 1 and 100")
	}
	if c.MaxParticipants < 1 {
		return fieldErr("max_participants", "must be positive")
	}
	if c.MaxRawConnections < c.MaxParticipants {
		return fieldErr("max_raw_connections", "must be >= max_participants (%d)", c.MaxParticipants)
	}
	if c.MaxIdentities < c.MaxRawConnections {
		return fieldErr("max_identities", "must be >= max_raw_connections (%d)", c.MaxRawConnections)
	}
	if c.MaxTeachers < 1 {
		return fieldErr("max_teachers", "must be positive")
	}
	if c.MaxSessionsPerParticipant < 1 {
		return fieldErr("max_sessions_per_participant", "must be positive")
	}
	if c.MaxNicknameRunes < 1 {
		return fieldErr("max_nickname_runes", "must be positive")
	}
	if c.InviteTimeout <= 0 {
		return fieldErr("invite_timeout", "must be positive")
	}
	if c.KeyTimeout <= 0 {
		return fieldErr("key_timeout", "must be positive")
	}
	if c.HeartbeatInterval <= 0 {
		return fieldErr("heartbeat_interval", "must be positive")
	}
	if c.DeadTimeout <= c.HeartbeatInterval {
		return fieldErr("dead_timeout", "must be greater than heartbeat_interval")
	}
	if c.ResumeWindow <= 0 {
		return fieldErr("resume_window", "must be positive")
	}
	if c.AckTimeout <= 0 {
		return fieldErr("ack_timeout", "must be positive")
	}
	if c.WriteTimeout <= 0 {
		return fieldErr("write_timeout", "must be positive")
	}
	if c.JoinTimeout <= 0 {
		return fieldErr("join_timeout", "must be positive")
	}
	if c.MaxBodyBytes < 1 {
		return fieldErr("max_body_bytes", "must be positive")
	}
	if c.MaxCiphertextBytes < c.MaxBodyBytes {
		return fieldErr("max_ciphertext_bytes", "must be >= max_body_bytes (%d)", c.MaxBodyBytes)
	}
	if c.MaxWSMessageBytes < c.MaxCiphertextBytes {
		return fieldErr("max_ws_message_bytes", "must be >= max_ciphertext_bytes (%d)", c.MaxCiphertextBytes)
	}
	if c.SessionHistoryCount < 1 {
		return fieldErr("session_history_count", "must be positive")
	}
	if c.SessionHistoryBytes < 1 {
		return fieldErr("session_history_bytes", "must be positive")
	}
	if c.GlobalHistoryBytes < c.SessionHistoryBytes {
		return fieldErr("global_history_bytes", "must be >= session_history_bytes")
	}
	if c.ClosedSessionMax < 1 {
		return fieldErr("closed_session_max", "must be positive")
	}
	if c.ClosedSessionTTL <= 0 {
		return fieldErr("closed_session_ttl", "must be positive")
	}
	if c.SendQueueCount < 1 {
		return fieldErr("send_queue_count", "must be positive")
	}
	if c.SendQueueBytes < 1 {
		return fieldErr("send_queue_bytes", "must be positive")
	}
	if c.ChatRatePerSec < 1 {
		return fieldErr("chat_rate_per_sec", "must be positive")
	}
	if c.ChatBurst < 1 {
		return fieldErr("chat_burst", "must be positive")
	}
	if c.ControlRatePerSec < 1 {
		return fieldErr("control_rate_per_sec", "must be positive")
	}
	if c.ControlBurst < 1 {
		return fieldErr("control_burst", "must be positive")
	}
	if c.ClientHistoryPerSession < 1 {
		return fieldErr("client_history_per_session", "must be positive")
	}
	if c.ClientCacheBytes < 1 {
		return fieldErr("client_cache_bytes", "must be positive")
	}
	if c.RenderInterval <= 0 {
		return fieldErr("render_interval", "must be positive")
	}
	return nil
}

func validateListenAddr(field, addr string, requireLoopback bool) error {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return fieldErr(field, "invalid address %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fieldErr(field, "invalid port %q", portText)
	}
	if requireLoopback {
		if !isLoopbackHost(host) {
			return fieldErr(field, "must use a loopback host, got %q", host)
		}
		return nil
	}
	if host == "" || host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		return nil
	}
	return fieldErr(field, "invalid host %q", host)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func fieldErr(field, format string, args ...any) error {
	return fmt.Errorf("%s: %s", field, fmt.Sprintf(format, args...))
}

var ErrHelp = errors.New("help requested")
