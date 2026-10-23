package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/config"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/platform"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/transport"
	"github.com/RakuLomis/TrafficAnalysisTutorial/internal/web"
)

var (
	version   = "0.1.0-dev"
	commit    = ""
	buildTime = ""
)

func main() {
	platform.EnableUTF8Console()
	parsed, err := config.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "启动失败：%v\n\n", err)
		printUsage()
		waitForEnter()
		os.Exit(1)
	}
	if parsed.ShowVersion {
		fmt.Printf("traffic-classroom %s (protocol 1", version)
		if commit != "" {
			fmt.Printf(", commit %s", commit)
		}
		if buildTime != "" {
			fmt.Printf(", built %s", buildTime)
		}
		fmt.Println(")")
		return
	}
	if err := run(parsed.Config); err != nil {
		fmt.Fprintf(os.Stderr, "启动失败：%v\n", err)
		waitForEnter()
		os.Exit(1)
	}
}

func run(cfg config.Config) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	candidates, err := platform.Candidates()
	if err != nil {
		return err
	}
	selected, err := platform.Select(candidates, cfg.IfaceIP)
	if err != nil {
		return err
	}
	port := portOf(cfg.StudentAddr)
	studentURL, err := studentURLFor(selected.IP.String(), port)
	if err != nil {
		return err
	}
	roundID, err := randomID()
	if err != nil {
		return fmt.Errorf("生成轮次 ID 失败：%w", err)
	}

	server := transport.New(cfg, web.StudentHandler(), web.TeacherHandler(), transport.Info{
		Version:    version,
		RoundID:    roundID,
		StudentURL: studentURL,
		Candidates: candidates,
	}, logger)
	if err := server.Start(); err != nil {
		return fmt.Errorf("无法启动服务：%w\n提示：端口可能被占用。可用 --student-addr 0.0.0.0:8090 --teacher-addr 127.0.0.1:8091 显式改端口，并同步放行防火墙。", err)
	}
	teacherURL := "http://" + server.TeacherAddr().String() + "/"

	fmt.Printf("\n加密流量课堂 %s\n", version)
	fmt.Printf("学生入口：%s\n", studentURL)
	fmt.Printf("教师入口：%s（仅本机）\n", teacherURL)
	fmt.Printf("学生监听：%s\n", server.StudentAddr().String())
	fmt.Printf("教师监听：%s\n", server.TeacherAddr().String())
	fmt.Printf("使用网卡：%s\n", selected.String())
	fmt.Printf("轮次 ID：%s\n", roundID)
	fmt.Println("\n学生用手机连接同一局域网后访问学生入口。按 Ctrl+C 退出。")

	logger.Info("traffic classroom started",
		"version", version,
		"round_id", roundID,
		"student_url", studentURL,
		"student_listen", server.StudentAddr().String(),
		"teacher_url", teacherURL,
		"iface", selected.String(),
	)

	if !cfg.NoOpen {
		if err := platform.OpenBrowser(teacherURL); err != nil {
			logger.Warn("cannot open teacher page", "error", err)
			fmt.Printf("未能自动打开教师页，请手动访问：%s\n", teacherURL)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	<-ctx.Done()

	fmt.Println("\n正在关闭服务…")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Close(shutdownCtx)
}

func studentURLFor(ip string, port int) (string, error) {
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid student port: %d", port)
	}
	host := strings.TrimSpace(ip)
	if host == "" {
		return "", fmt.Errorf("empty interface address")
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("http://%s:%d/", host, port), nil
}

func portOf(addr string) int {
	_, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return 0
	}
	return port
}

func randomID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func waitForEnter() {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return
	}
	fmt.Fprint(os.Stderr, "\n按 Enter 键退出…")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "用法：traffic-classroom [选项]")
	fmt.Fprintln(os.Stderr, "  --student-addr 0.0.0.0:8080   学生监听地址")
	fmt.Fprintln(os.Stderr, "  --teacher-addr 127.0.0.1:8081 教师监听地址（仅 loopback）")
	fmt.Fprintln(os.Stderr, "  --iface-ip <IPv4>             二维码与展示使用的局域网地址")
	fmt.Fprintln(os.Stderr, "  --no-open                     不自动打开教师页")
	fmt.Fprintln(os.Stderr, "  --version                     打印版本")
}
