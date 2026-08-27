package database

import (
	"testing"
	"time"
)

func TestParseTimeout(t *testing.T) {
	def := 120 * time.Second
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", def},            // 未配置→默认
		{"  ", def},          // 空白→默认
		{"60s", 60 * time.Second},
		{"2m", 2 * time.Minute},
		{"abc", def},         // 非法→默认(不能回到无超时挂起态)
		{"-5s", def},         // 非正→默认
		{"0", def},           // 零值→默认
	}
	for _, c := range cases {
		if got := parseTimeout(c.in, def); got != c.want {
			t.Errorf("parseTimeout(%q)=%s want %s", c.in, got, c.want)
		}
	}
}
