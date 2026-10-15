package infra

import (
	"admin/internal/accounts"
	"admin/internal/auth"
	"admin/internal/platform"
	"context"
	"fmt"
	"github.com/docker/docker/client"
	"log"
	"net/http"
	"time"
)

func ApiDashboard(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := auth.GetUserFromContext(r)

		// For non-admin users, return personalized dashboard
		if userInfo != nil && userInfo.Role != "admin" {
			allClients, err := accounts.ReadXrayClientMaps()
			if err != nil {
				log.Printf("Failed to read clients for dashboard: %v", err)
				platform.WriteInternalError(w, err)
				return
			}
			var myClients []map[string]string
			for _, c := range allClients {
				if c["owner"] == username {
					myClients = append(myClients, c)
				}
			}
			proxyAuth := ""
			if len(myClients) > 0 {
				// 只读 GET 不得有写副作用:ProxyAuthForUser 会在首次调用时为该用户
				// 生成 proxy 密码并重写 htpasswd(users_api.go / xray_clients_api.go
				// 两个读端点同此约定改用 Read 变体)。密码尚未生成时返回空串——
				// 客户端首次添加 Xray 客户端时再懒创建,与那两个读端点一致。
				proxyAuth = auth.ProxyAuthForUserRead(username)
			}
			genJSON := accounts.GenerateClientConfig(myClients, accounts.ClientConfig{
				XHTTPDomain:    platform.GetEnv("XHTTP_DOMAIN"),
				XHTTPAddress:   platform.GetEnv("XHTTP_ADDRESS"),
				XHTTPALPN:      platform.GetEnv("XHTTP_ALPN"),
				XHTTPProxyAuth: proxyAuth,
			})
			platform.WriteJSON(w, map[string]any{
				"role":          userInfo.Role,
				"username":      username,
				"myClients":     myClients,
				"generatedJSON": genJSON,
			})
			return
		}

		// Admin: full system dashboard
		// 读路径 deadline:daemon 挂起时 dashboard 10s 快速失败降级,
		// 不随客户端连接挂住(r.Context() 客户端不断开就不取消)。
		infoCtx, infoCancel := context.WithTimeout(r.Context(), 10*time.Second)
		info, err := cli.Info(infoCtx)
		infoCancel()
		if err != nil {
			// 降级为全 0 stats,但留日志:Docker 故障不该表现为"无声的零值"。
			log.Printf("dashboard: docker info: %v", err)
		}
		stats := map[string]string{
			"services": "0", "running": "0", "dockerVersion": "-",
			"cpu": "0", "memory": "-", "os": "-", "arch": "-",
		}
		if err == nil {
			stats = map[string]string{
				"services":      fmt.Sprintf("%d", info.Containers),
				"running":       fmt.Sprintf("%d", info.ContainersRunning),
				"dockerVersion": info.ServerVersion,
				"cpu":           fmt.Sprintf("%d", info.NCPU),
				"memory":        fmt.Sprintf("%.1f GB", float64(info.MemTotal)/1024/1024/1024),
				"os":            info.OSType,
				"arch":          info.Architecture,
			}
		}
		svcs := listServicesJSON(r.Context(), cli)
		platform.WriteJSON(w, map[string]any{"stats": stats, "services": svcs, "role": "admin"})
	}
}
