package transport

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/config"
)

func TestHeartbeatTimeoutClosesIdleConnection(t *testing.T) {
	s := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.HeartbeatInterval = 50 * time.Millisecond
		cfg.DeadTimeout = 100 * time.Millisecond
		cfg.JoinTimeout = 5 * time.Second
	})
	server := httptest.NewServer(s.studentHandler())
	defer server.Close()

	conn := dialWS(t, server.URL, "/ws")
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("read succeeded, want heartbeat timeout close")
	}
}
