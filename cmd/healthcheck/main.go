// Package main 是 `healthcheck` 子命令：CLI 探活。
// 用法：healthcheck [live|ready] [addr]   默认 ready、本机 8080。
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	probe := "ready"
	addr := os.Getenv("SHOP_SERVE_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	if len(os.Args) >= 2 {
		probe = os.Args[1]
	}
	if len(os.Args) >= 3 {
		addr = os.Args[2]
	}
	url := fmt.Sprintf("http://%s/health/%s", addr, probe)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck %s error: %v\n", probe, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "healthcheck %s unhealthy (status %d): %s\n", probe, resp.StatusCode, string(body))
		os.Exit(1)
	}
	fmt.Printf("healthcheck %s ok (status %d)\n", probe, resp.StatusCode)
}
