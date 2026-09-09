package main

import (
	"context"
	"net/http"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// shutdown_test.go 锁死台账 #117 的外层:后端必须真的能被信号叫醒,
// 并且【等撤单跑完】再退出。改动前这两条都不成立 ——
// 全后端 0 处信号处理,mm.Stop() 挂在永不 Done 的 context.Background 上。

// TestShutdownContextWakesOnSIGTERM:SIGTERM 必须能把总闸 ctx 关掉。
// 这就是 #117 的红→绿点:换回 context.Background() 这个测试立刻挂。
func TestShutdownContextWakesOnSIGTERM(t *testing.T) {
	ctx, stop := shutdownContext()
	defer stop()

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("发不出 SIGTERM: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("收到 SIGTERM 两秒后总闸仍没 Done —— 后端没有接信号,所有收尾动作(尤其是撤单)都是死代码")
	}
}

// TestShutdownContextWakesOnSIGINT:人在服务器上 Ctrl-C 停服务同样要走优雅关闭。
func TestShutdownContextWakesOnSIGINT(t *testing.T) {
	ctx, stop := shutdownContext()
	defer stop()

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("发不出 SIGINT: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("收到 SIGINT 两秒后总闸仍没 Done")
	}
}

// TestServeUntilShutdownWaitsForCancelSweep:撤单必须是【同步】等完的。
//
// 老形态是 `go func(){ <-ctx.Done(); mm.Stop() }()` —— 挂个 goroutine 就走,
// main 一 return 进程就没了,撤单跑到一半被腰斩,等于没撤。
func TestServeUntilShutdownWaitsForCancelSweep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := &http.Server{Addr: "127.0.0.1:0"} // 端口 0 = 随机可用端口

	var done atomic.Bool
	shutdownMM := func() {
		time.Sleep(200 * time.Millisecond) // 假装在撤单
		done.Store(true)
	}

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		_ = serveUntilShutdown(ctx, srv, shutdownMM)
	}()

	time.Sleep(50 * time.Millisecond)
	if done.Load() {
		t.Fatal("还没收到信号就撤单了")
	}
	cancel()

	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("serveUntilShutdown 没有返回")
	}
	if !done.Load() {
		t.Fatal("serveUntilShutdown 在撤单跑完之前就返回了 —— 进程会带着没撤干净的挂单退出(这正是 #117)")
	}
}

// TestShutdownBudgetFitsGrace:HTTP 收尾预算必须远小于容器宽限期(docker 默认 10s),
// 且它与撤单是并行的,所以总时长取两者较大值,不相加。
func TestShutdownBudgetFitsGrace(t *testing.T) {
	if httpShutdownBudget >= 5*time.Second {
		t.Fatalf("HTTP 收尾预算 %s 过大:它和撤单并行,但仍要整个装进 10s 宽限期", httpShutdownBudget)
	}
}
