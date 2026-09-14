package infra

import (
	"admin/internal/accounts"
	"admin/internal/auth"
	"admin/internal/platform"
	"fmt"
	"github.com/docker/docker/client"
	"log"
	"net/http"
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
				proxyAuth, err = auth.ProxyAuthForUser(username)
				if err != nil {
					platform.WriteInternalError(w, err)
					return
				}
			}
			genJSON := accounts.GenerateClientConfig(myClients, accounts.ClientConfig{
				RealityDomain:     platform.GetEnv("XRAY_REALITY_DOMAIN"),
				RealityServerName: platform.GetEnv("XRAY_REALITY_SERVER_NAME"),
				RealityPublicKey:  platform.GetEnv("XRAY_PUBLIC_KEY"),
				RealityShortID:    accounts.XrayClientShortID(),
				XHTTPDomain:       platform.GetEnv("XHTTP_DOMAIN"),
				XHTTPProxyAuth:    proxyAuth,
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
		info, err := cli.Info(r.Context())
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
