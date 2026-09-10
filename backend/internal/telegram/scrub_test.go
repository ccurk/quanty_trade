package telegram

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// 这里刻意**不用**真 token 的形状(数字:字母)——
// scrub 做的是字面量替换,与形状无关,而一个长得像凭据的测试夹具会把明文凭据闸门
// (agent-office 的 pre-commit)喂成狼来了。假警报比漏验更糟,所以夹具要一眼假。
// 值以 "fake" 开头是刻意的:scan_secrets.py 的占位符标记必须**锚在值的开头**才算数
// (埋在中间不算,免得「32 字符真密钥里恰好含 change_me」被放过)。
const fakeToken = "fake-token-not-a-real-credential"

// TestFetchUpdatesErrorHidesToken:网络失败时,吐出去的错误里不许出现 token。
//
// 复现的是 2026-09-09 那 12 小时的真实形状:DNS 挂了 → httpClient.Do 返回 *url.Error
// → Error() 里带着整条 https://api.telegram.org/bot<TOKEN>/getUpdates → run() 那句
// logger.Errorf 每 3 秒把它写进容器日志一次。这里用一个解析不了的主机名造出同一类失败。
func TestFetchUpdatesErrorHidesToken(t *testing.T) {
	s := &Service{
		token:       fakeToken,
		baseURL:     "https://invalid.host.that.does.not.exist.example/bot" + fakeToken,
		httpClient:  &http.Client{Timeout: 3 * time.Second},
		pollTimeout: 1,
	}
	_, _, err := s.fetchUpdates(context.Background(), 0)
	if err == nil {
		t.Fatal("这个域名不该解析得出来,期望拿到一个错误")
	}
	if strings.Contains(err.Error(), fakeToken) {
		t.Fatalf("错误信息里带着 token:%s", strings.ReplaceAll(err.Error(), fakeToken, "<LEAK>"))
	}
	if !strings.Contains(err.Error(), "<redacted>") {
		t.Fatalf("期望看到 <redacted> 占位,实得:%s", err.Error())
	}
}

// TestSendTextErrorHidesToken:发消息失败这条路径同样不许漏。
func TestSendTextErrorHidesToken(t *testing.T) {
	s := &Service{
		token:      fakeToken,
		baseURL:    "https://invalid.host.that.does.not.exist.example/bot" + fakeToken,
		httpClient: &http.Client{Timeout: 3 * time.Second},
	}
	err := s.sendText(42, "hello")
	if err == nil {
		t.Fatal("期望拿到一个错误")
	}
	if strings.Contains(err.Error(), fakeToken) {
		t.Fatalf("错误信息里带着 token:%s", strings.ReplaceAll(err.Error(), fakeToken, "<LEAK>"))
	}
}

// TestScrubKeepsUnrelatedErrors:不含 token 的错误必须原样返回,不许被包一层改了形状。
func TestScrubKeepsUnrelatedErrors(t *testing.T) {
	s := &Service{token: fakeToken}
	orig := http.ErrHandlerTimeout
	if got := s.scrub(orig); got != orig {
		t.Fatalf("无关错误被改写了:%v", got)
	}
	if got := s.scrub(nil); got != nil {
		t.Fatalf("nil 应该原样返回,实得 %v", got)
	}
	// token 为空(未配置)时不能 panic,也不该乱替换。
	empty := &Service{}
	if got := empty.scrub(orig); got != orig {
		t.Fatalf("token 为空时不该改写错误:%v", got)
	}
}
