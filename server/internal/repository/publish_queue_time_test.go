package repository

import (
	"testing"
	"time"
)

// TestParsePublishQueueTime 队列时间解析必须兼容各驱动/各写入路径的文本形态。
// 背景：MySQL 在 parseTime=True 下用 sql.NullString 读到的是 RFC3339
// （"2026-11-10T22:29:52+08:00"），只按 "2006-01-02 15:04:05" 解析会静默失败，
// 队尾被当成"空队列"，新入队批次于是从当前时间立刻发车（2026-11-10 线上实测）。
func TestParsePublishQueueTime(t *testing.T) {
	want := time.Date(2026, 11, 10, 22, 20, 16, 0, DatabaseTimeZone)

	cases := []string{
		"2026-11-10 22:20:16",                     // Go 写入路径
		"2026-11-10T22:20:16+08:00",               // MySQL parseTime=True 读出
		"2026-11-10T14:20:16Z",                    // 同刻的 UTC 写法
		"2026-11-10T22:20:16",                     // 前端 ISO 无时区
		"2026-11-10 22:20:16.123456789 +0800 CST", // time.Time.String()
		"2026-11-10 22:20:16.000",                 // DATETIME(3) 截断形态
		"  2026-11-10 22:20:16  ",                 // 首尾空白
	}
	for _, raw := range cases {
		got, ok := parsePublishQueueTime(raw)
		if !ok {
			t.Fatalf("应能解析 %q，实际失败", raw)
		}
		if !got.Truncate(time.Second).Equal(want) {
			t.Fatalf("解析结果不符 %q: got=%s want=%s", raw, got.Format(time.RFC3339), want.Format(time.RFC3339))
		}
	}

	for _, raw := range []string{"", "   ", "not-a-time", "NULL"} {
		if _, ok := parsePublishQueueTime(raw); ok {
			t.Fatalf("非法值 %q 不应解析成功", raw)
		}
	}
}
