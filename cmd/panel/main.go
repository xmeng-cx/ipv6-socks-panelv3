package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ipv6-socks-panel/internal/panel"
)

func main() {
	cfg, err := panel.LoadConfig()
	if err != nil {
		log.Fatalf("配置错误: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	manager := panel.NewManager(cfg, panel.NewNetworkManager(), panel.NewXrayProcess(cfg))
	log.Printf("正在探测 IPv6 网络并恢复线路...")
	if err := manager.Start(ctx); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
	info := manager.NetworkInfo()
	log.Printf("已启动：网卡=%s 前缀=%s 线路=%d Web=http://%s", info.Interface, info.Prefix, len(manager.List()), cfg.WebListen)

	server := &http.Server{Addr: cfg.WebListen, Handler: panel.NewHTTPHandler(manager), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Printf("Web 服务异常: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	if err := manager.Shutdown(shutdownCtx); err != nil {
		log.Printf("清理受管 IPv6 时出现错误: %v", err)
	}
	log.Printf("已安全停止")
}
