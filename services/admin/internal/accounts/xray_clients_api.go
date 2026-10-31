package accounts

import (
	"admin/internal/auth"
	"admin/internal/platform"
	"admin/internal/proxy"
	"context"
	"errors"
	"github.com/docker/docker/client"
	"log"
	"net/http"
	"regexp"
	"strings"
)

// rollbackXrayClientAdd 撤销一次刚写入台账的客户端，并尽力把运行配置恢复
// 到不含它的状态。只在 GenXrayConfig/RestartXray 失败时使用。
func rollbackXrayClientAdd(id string) {
	if removed, err := removeXrayClient(id); err != nil {
		log.Printf("ERROR: rollback xray client %s: %v", id, err)
	} else if removed {
		if _, err := GenXrayConfig(); err != nil {
			log.Printf("ERROR: regenerate xray config after rollback of %s: %v", id, err)
		}
	}
}

// rollbackXrayClientDelete 把刚删掉的客户端写回台账，并尽力恢复运行配置。
func rollbackXrayClientDelete(entry xrayClientEntry) {
	if err := addXrayClient(entry); err != nil {
		log.Printf("ERROR: rollback deleted xray client %s: %v", entry.ID, err)
		return
	}
	if _, err := GenXrayConfig(); err != nil {
		log.Printf("ERROR: regenerate xray config after rollback of %s: %v", entry.ID, err)
	}
}

func ApiAddXray(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := auth.GetUserFromContext(r)

		var body struct {
			UUID  string `json:"uuid"`
			Owner string `json:"owner"`
		}
		if err := platform.DecodeStrictJSON(r.Body, &body); err != nil {
			platform.WriteError(w, "请求体不是合法 JSON", 400)
			return
		}
		if body.UUID == "" {
			platform.WriteError(w, "UUID 必填", 400)
			return
		}
		if !validUUID.MatchString(body.UUID) {
			platform.WriteError(w, "UUID 格式不合法（小写 hex，例 550e8400-e29b-41d4-a716-446655440000）", 400)
			return
		}
		// UUID 规范化:大写/小写视为同一客户端(Xray 的 UUID 不区分大小写),
		// 避免 "550e8400-…" 与 "550E8400-…" 作为两个条目写入配置导致 Xray
		// 加载重复 user 失败。
		uuid := strings.ToLower(body.UUID)

		// 客户端归属：登录用户是 nginx 认证身份，客户端是它名下的代理身份，
		// 一个用户可以创建多个。admin 可以代任意存在的用户创建（owner 字段）；
		// 非 admin 只能为自己创建。
		owner := username
		if strings.TrimSpace(body.Owner) != "" {
			owner = strings.TrimSpace(body.Owner)
		}
		isAdmin := userInfo != nil && userInfo.Role == "admin"
		if !isAdmin && owner != username {
			platform.WriteError(w, "无权操作：只能为自己创建客户端", 403)
			return
		}
		if !auth.IsValidUsername(owner) || auth.IsReservedUsername(owner) {
			platform.WriteError(w, "owner 用户名不合法或为保留名", 400)
			return
		}

		// 与删除面板用户串行化：否则删除流程结束后在途的 add 仍会把 proxy
		// 凭证/客户端写回去，被删用户重新获得代理身份。
		userLifecycleMu.Lock()
		defer userLifecycleMu.Unlock()

		exists, err := auth.UserMetaExists(owner)
		if err != nil {
			log.Printf("check user %q: %v", owner, err)
			platform.WriteInternalError(w, err)
			return
		}
		if !exists {
			platform.WriteError(w, "owner 用户不存在", 404)
			return
		}
		// 保留伪用户(internal/fail2ban)已在上面拒绝；这里为真实登录用户
		// 懒创建 nginx Basic Auth 的 proxy 密码。
		if _, err := auth.GetOrCreateProxyPassword(owner); err != nil {
			log.Printf("Failed to create proxy password for %s: %v", owner, err)
			platform.WriteInternalError(w, err)
			return
		}

		entry := xrayClientEntry{ID: uuid, Owner: owner, Email: uuid}
		if err := addXrayClient(entry); err != nil {
			if errors.Is(err, errDuplicateUUID) {
				platform.WriteError(w, "该 UUID 的客户端已存在", 409)
				return
			}
			log.Printf("Failed to save client: %v", err)
			platform.WriteInternalError(w, err)
			return
		}

		// GenXrayConfig() reads clients under xrayClientsMu and releases it before
		// generating the config, so add/delete operations are not blocked.
		changed, err := GenXrayConfig()
		if err != nil {
			rollbackXrayClientAdd(uuid)
			log.Printf("ERROR: generate xray config after add: %v", err)
			platform.WriteInternalError(w, err)
			return
		}
		if changed {
			// 重启不随客户端断开而取消：与 apiServiceAction 一致，避免 add 请求断连后
			// 只完成配置落盘、Xray 停在未加载状态。
			restartCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), platform.RestartTimeout)
			err := proxy.RestartXray(restartCtx, cli)
			cancel()
			if err != nil {
				// 配置已落盘但 xray 未加载：回滚台账并恢复配置，避免用户看到
				// "已保存"、代理实际不可用，且重试撞 409 无法自愈。
				rollbackXrayClientAdd(uuid)
				log.Printf("ERROR: failed to restart xray after add: %v", err)
				platform.WriteError(w, "客户端创建失败：Xray 无法加载新配置，已回滚", http.StatusBadGateway)
				return
			}
		}
		log.Printf("[security] xray client add uuid=%s owner=%s by=%s", uuid, owner, username)
		platform.WriteJSON(w, map[string]string{"status": "ok", "uuid": uuid, "owner": owner})
	}
}

func ApiDeleteXray(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := auth.GetUserFromContext(r)

		var body struct {
			UUID string `json:"uuid"`
		}
		if err := platform.DecodeStrictJSON(r.Body, &body); err != nil {
			platform.WriteError(w, "请求体不是合法 JSON", 400)
			return
		}
		if body.UUID == "" {
			platform.WriteError(w, "UUID 必填", 400)
			return
		}
		// 与 ApiAddXray 的规范化一致:UUID 不区分大小写。
		body.UUID = strings.ToLower(body.UUID)

		// deleteXrayClientIfOwner 原子地检查所有权并删除，避免 TOCTOU 竞态。
		// auth.RequireRole 保证 userInfo 非 nil(死分支不复存在)
		isAdmin := userInfo != nil && userInfo.Role == "admin"
		ownerFilter := ""
		if !isAdmin {
			ownerFilter = username
		}
		deletedEntry, deleted, err := deleteXrayClientIfOwner(body.UUID, ownerFilter)
		if err != nil {
			if err == errForbidden {
				platform.WriteError(w, "无权操作：不是你的客户端", 403)
				return
			}
			if err == errNotFound {
				platform.WriteError(w, "客户端不存在", 404)
				return
			}
			log.Printf("Failed to delete client: %v", err)
			platform.WriteInternalError(w, err)
			return
		}
		if !deleted {
			platform.WriteError(w, "客户端不存在", 404)
			return
		}

		changed, err := GenXrayConfig()
		if err != nil {
			rollbackXrayClientDelete(deletedEntry)
			log.Printf("ERROR: generate xray config after delete: %v", err)
			platform.WriteInternalError(w, err)
			return
		}
		if changed {
			// 重启不随客户端断开而取消（与 ApiAddXray 一致）。
			restartCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), platform.RestartTimeout)
			err := proxy.RestartXray(restartCtx, cli)
			cancel()
			if err != nil {
				// 配置已落盘但没有生效：写回台账并恢复配置，避免"UI 已看不到
				// 客户端、旧 UUID 却仍然可用"的无上限撤销窗口。
				rollbackXrayClientDelete(deletedEntry)
				log.Printf("ERROR: failed to restart xray after delete: %v", err)
				platform.WriteError(w, "客户端删除失败：Xray 无法加载新配置，已回滚", http.StatusBadGateway)
				return
			}
		}
		log.Printf("[security] xray client delete uuid=%q owner=%q by=%s", body.UUID, deletedEntry.Owner, username)
		platform.WriteJSON(w, map[string]string{"status": "ok"})
	}
}

// GET /api/admin/users/xray/config/{uuid} — generate config for a specific client
func ApiXrayClientConfig() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := auth.GetUserFromContext(r)
		uuid := strings.ToLower(r.PathValue("uuid"))
		if uuid == "" {
			platform.WriteError(w, "UUID 必填", 400)
			return
		}

		allClients, err := ReadXrayClientMaps()
		if err != nil {
			log.Printf("Failed to read clients: %v", err)
			platform.WriteInternalError(w, err)
			return
		}
		var client map[string]string
		for _, c := range allClients {
			if c["id"] == uuid {
				client = c
				break
			}
		}
		if client == nil {
			platform.WriteError(w, "客户端不存在", 404)
			return
		}

		// Regular users can only view their own client
		if userInfo != nil && userInfo.Role != "admin" && client["owner"] != username {
			platform.WriteError(w, "无权限", 403)
			return
		}

		// 只读端点:用 auth.ProxyAuthForUserRead 拿已有密码,绝不在这里懒创建
		// (生成密码 + 重写 htpasswd 是写副作用,并发 GET 会写放大,且写失败
		// 会让只读请求 500)。用户无 proxy 密码时配置里留空,与 auth.ApiUsers 一致。
		proxyAuth := auth.ProxyAuthForUserRead(client["owner"])
		genJSON := GenerateClientConfig([]map[string]string{client}, ClientConfig{
			XHTTPDomain:    platform.GetEnv("XHTTP_DOMAIN"),
			XHTTPAddress:   platform.GetEnv("XHTTP_ADDRESS"),
			XHTTPALPN:      platform.GetEnv("XHTTP_ALPN"),
			XHTTPProxyAuth: proxyAuth,
		})
		platform.WriteJSON(w, map[string]any{
			"config": genJSON, "uuid": uuid, "owner": client["owner"],
		})
	}
}

var validUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
