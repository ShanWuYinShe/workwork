package main

import "testing"

func TestConvertDockerLogTimestamp(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "UTC 转中国时区",
			in:   "2026-08-08T15:03:00Z INFO hello",
			want: "2026-08-08T23:03:00.000+08:00 INFO hello",
		},
		{
			name: "带纳秒精度",
			in:   "2026-08-08T01:02:03.123456789Z ERR boom",
			want: "2026-08-08T09:02:03.123+08:00 ERR boom",
		},
		{
			name: "无时间戳原样返回",
			in:   "plain log line without timestamp",
			want: "plain log line without timestamp",
		},
		{
			name: "空行原样返回",
			in:   "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := convertDockerLogTimestamp(tc.in)
			if got != tc.want {
				t.Fatalf("convertDockerLogTimestamp(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
