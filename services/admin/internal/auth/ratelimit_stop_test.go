package auth

import "testing"

func TestRateLimiterStopBeforeStartIsNoop(t *testing.T) {
	rl := &rateLimiter{}
	rl.stop() // 未 start 时不得 close(nil) panic
}
