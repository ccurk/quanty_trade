package main

import (
	"context"
	"errors"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"quanty_trade/internal/logger"
)

// shutdown.go 是台账 #117【优雅撤单是死代码】的接线点。
//
// 病历:后端【0 处信号处理】,而做市引擎的 mm.Stop()(撤光所有挂单)挂在
// context.Background() 派生的 ctx 上 —— 那个 ctx 永远不会 Done,所以 Stop()
// 生产里【从没执行过一次】。写了一整段"绝不把裸单留在交易所"的代码,
// 而进程每次都是被 SIGTERM 直接打断,挂单原样留在盘口上。
//
// 接上之后这条链才闭合:SIGTERM/SIGINT → ctx.Done → mm.Stop() → cancelAll
// → 撤单档读挂单(绕过熔断)→ 逐张撤 → 进程退出。
//
// 【为什么 HTTP 与撤单是并行收尾】两件事互不依赖:撤单是对交易所,关端口是对前端。
// 串起来做总时长要相加,而外面那把 SIGKILL 的刀只给一个宽限期(docker 默认 10s,
// docker-compose.prod.yml 未配 stop_grace_period)。并行的话总时长是两者取大:
// 撤单最坏 7s(见 marketmaker/engine.go 的两段预算),HTTP 收尾 2s。

// httpShutdownBudget 是等在途 HTTP 请求收尾的上限。
// 它只需覆盖一次正常 API 调用的量级;等更久没有意义 —— 真正需要时间的是撤单,
// 而那一段有它自己的预算。
const httpShutdownBudget = 2 * time.Second

// shutdownContext 返回一个"收到 SIGTERM/SIGINT 就 Done"的 ctx。
//
// 这一行就是 #117 的修复本身:在它出现之前,main 用的是 context.Background(),
// 全后端没有任何地方监听信号,于是所有挂在 ctx.Done 上的收尾动作都是死代码。
//
// SIGTERM 是 docker stop / 容器重启 / 宿主关机发的;SIGINT 是终端里 Ctrl-C。
// 两个都要接:前者是生产,后者是人在服务器上手动停服务时的常见姿势。
func shutdownContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

// serveUntilShutdown 跑 HTTP 服务,直到 ctx 被信号 Done(或服务自己挂了),
// 然后并行做两件收尾:关 HTTP、让做市引擎撤光挂单。
//
// shutdownMM 来自 app.StartBackgroundJobs,必须【同步】调用:它内部自带硬超时,
// 而进程要等它跑完才能退 —— 挂个 goroutine 就走,等于什么都没撤(#117 的老形态)。
func serveUntilShutdown(ctx context.Context, srv *http.Server, shutdownMM func()) error {
	srvErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			srvErr <- err
			return
		}
		srvErr <- nil
	}()

	var runErr error
	select {
	case <-ctx.Done():
		logger.Infof("[shutdown] 收到退出信号:开始收尾(撤光做市挂单 + 关闭 HTTP)")
	case runErr = <-srvErr:
		// 端口被占之类:服务根本没跑起来。照样要走收尾,否则已经挂出去的单没人撤。
		logger.Errorf("[shutdown] HTTP 服务提前退出,仍执行收尾: %v", runErr)
	}

	httpDone := make(chan struct{})
	go func() {
		defer close(httpDone)
		hctx, cancel := context.WithTimeout(context.Background(), httpShutdownBudget)
		defer cancel()
		if err := srv.Shutdown(hctx); err != nil {
			logger.Warnf("[shutdown] HTTP 未能在 %s 内干净关闭: %v", httpShutdownBudget, err)
		}
	}()

	shutdownMM()
	<-httpDone
	logger.Infof("[shutdown] 收尾完成,进程退出")
	return runErr
}
