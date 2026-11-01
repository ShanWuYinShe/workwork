package auth

import (
	"admin/internal/runtime"

	"github.com/docker/docker/client"
)

// Security 是限流/封禁/nginx 同步的唯一 owner。路径（blocked-ips.json、
// generated/nginx/blocked-ips.conf）与 docker client 都通过构造注入，
// 不再有包级 ipBlocker/authRateLimiter/blocklistSyncCli 全局。
type Security struct {
	blocker   *ipBlockStore
	limiter   *rateLimiter
	blocklist *blocklistSync
}

func NewSecurity(layout runtime.Layout) *Security {
	blocker := &ipBlockStore{path: layout.BlockedIPsPath()}
	sec := &Security{
		blocker:   blocker,
		limiter:   newRateLimiter(blocker),
		blocklist: newBlocklistSync(layout.BlockedIPsConfPath(), blocker),
	}
	blocker.onChange = sec.blocklist.schedule
	blocker.load()
	return sec
}

func (s *Security) StartRateLimiter() { s.limiter.start() }
func (s *Security) StopRateLimiter()  { s.limiter.stop() }
func (s *Security) FlushBlockedIPs()  { s.blocker.flush() }

func (s *Security) SetBlocklistClient(cli *client.Client) { s.blocklist.setClient(cli) }
func (s *Security) ScheduleBlocklistSync()                { s.blocklist.schedule() }
func (s *Security) WriteBlockedIPsConf() error            { return s.blocklist.write() }
