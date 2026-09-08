package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 台账 #37 的回归测试:证明 server.log 真的会轮转、当前文件被压在阈值内、
// 历史备份被裁到 logMaxBackups 份 —— 也就是磁盘占用真有上界。
// 用 1MB 阈值代替生产的 50MB,走的是同一个 newRotatingWriter,机制一致。
func TestRotatingWriterRotatesAndPrunes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.log")
	w := newRotatingWriter(path, 1) // 1 MB

	line := []byte(strings.Repeat("x", 1024) + "\n")
	const totalKB = 5 * 1024 // ~5MB,足够越过 1MB 阈值好几次并触发裁剪
	for i := 0; i < totalKB; i++ {
		if _, err := w.Write(line); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if c, ok := w.(io.Closer); ok {
		if err := c.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}

	// 1) 轮转发生了:目录里不止当前这一个文件。
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("没有发生轮转:写了约 %dKB 之后目录里只有 %d 个文件 %v",
			totalKB, len(entries), names(entries))
	}

	// 2) 当前文件被压在阈值内 —— 这是"不撑爆磁盘"的核心断言。
	//    写了约 5MB,若不轮转 server.log 就该是 5MB。
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if fi.Size() > 2<<20 {
		t.Fatalf("当前 server.log 没被截断:%d 字节,超过 1MB 阈值太多", fi.Size())
	}

	// 3) 备份份数收敛到 1 + logMaxBackups。gzip 压缩是后台 goroutine 做的,
	//    Close 不等它,所以这里轮询一小会儿再断言。
	deadline := time.Now().Add(3 * time.Second)
	var n int
	for {
		entries, err = os.ReadDir(dir)
		if err != nil {
			t.Fatalf("readdir: %v", err)
		}
		n = len(entries)
		if n <= 1+logMaxBackups || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n > 1+logMaxBackups {
		t.Fatalf("备份没被裁剪:目录里有 %d 个文件,上限应为 %d;%v",
			n, 1+logMaxBackups, names(entries))
	}
	t.Logf("轮转后目录内容(%d 个文件): %v;当前 server.log = %d 字节",
		n, names(entries), fi.Size())
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
