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
	"time"
)

// -- User management APIs --
func ApiUsers(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := auth.GetUserFromContext(r)
		allClients, err := ReadXrayClientMaps()
		if err != nil {
			log.Printf("Failed to read clients: %v", err)
			platform.WriteInternalError(w, err)
			return
		}

		// Filter by owner: admin sees all, viewer sees only own
		var filtered []map[string]string
		if userInfo != nil && userInfo.Role == "admin" {
			filtered = allClients
		} else {
			for _, c := range allClients {
				if c["owner"] == username {
					filtered = append(filtered, c)
				}
			}
		}

		// 只读：GET 端点不创建 proxy 密码（避免写副作用）。
		// proxyAuth 仅当列表里全部客户端都属于当前用户时才填充——admin 视角
		// 的 filtered 含其他用户的客户端，取"第一个 owner"会把他人明文凭证
		// 嵌进返回的 generatedJSON，admin 转发配置即泄露他人凭证。
		proxyAuth := ""
		if len(filtered) > 0 {
			allOwned := true
			for _, c := range filtered {
				if c["owner"] != username {
					allOwned = false
					break
				}
			}
			if allOwned {
				proxyAuth = auth.ProxyAuthForUserRead(username)
			}
		}
		genJSON := GenerateClientConfig(filtered, ClientConfig{
			RealityDomain:     platform.GetEnv("XRAY_REALITY_DOMAIN"),
			RealityServerName: platform.GetEnv("XRAY_REALITY_SERVER_NAME"),
			RealityPublicKey:  platform.GetEnv("XRAY_PUBLIC_KEY"),
			RealityShortID:    XrayClientShortID(),
			XHTTPDomain:       platform.GetEnv("XHTTP_DOMAIN"),
			XHTTPProxyAuth:    proxyAuth,
		})

		// Build adminUsers with role info
		doc, err := auth.ReadUsersMeta()
		if err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		type adminUserJSON struct {
			Username  string `json:"username"`
			Role      string `json:"role"`
			CreatedAt string `json:"created_at"`
		}
		var adminUsers []adminUserJSON
		if userInfo != nil && userInfo.Role == "admin" {
			for _, u := range auth.ReadHtpasswdUsers() {
				au := adminUserJSON{Username: u, Role: "viewer"}
				if info, ok := doc.Users[u]; ok {
					au.Role = info.Role
					au.CreatedAt = info.CreatedAt
				}
				adminUsers = append(adminUsers, au)
			}
		} else {
			// Regular users only see themselves
			if curInfo, ok := doc.Users[username]; ok {
				adminUsers = []adminUserJSON{{Username: username, Role: curInfo.Role, CreatedAt: curInfo.CreatedAt}}
			} else {
				adminUsers = []adminUserJSON{{Username: username, Role: "viewer"}}
			}
		}

		platform.WriteJSON(w, map[string]any{
			"xrayClients": filtered, "generatedJSON": genJSON, "adminUsers": adminUsers,
		})
	}
}

func ApiChangePassword(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := auth.GetUserFromContext(r)
		var body struct {
			Username    string `json:"username"`
			OldPassword string `json:"old_password"`
			Password    string `json:"password"`
		}
		if err := platform.DecodeStrictJSON(r.Body, &body); err != nil {
			platform.WriteError(w, "Invalid JSON", 400)
			return
		}
		if body.Username == "" || body.Password == "" {
			platform.WriteError(w, "Username and password required", 400)
			return
		}
		if !auth.IsValidUsername(body.Username) {
			platform.WriteError(w, "Invalid username (use alphanumeric, underscore, hyphen)", 400)
			return
		}
		// Non-admin can only change their own password
		if userInfo != nil && userInfo.Role != "admin" && body.Username != username {
			platform.WriteError(w, "Forbidden: can only change your own password", 403)
			return
		}
		// Protected admin account (AUTH_USER) can only change its own password:
		// 防止任意 admin 重置超级管理员密码并接管该账号(纵深防御,与
		// ApiSetRole/ApiDeleteAdmin 的 auth.IsProtectedAdmin 守卫对齐)。
		if body.Username == platform.GetEnv("AUTH_USER") && (userInfo == nil || username != body.Username) {
			platform.WriteError(w, "Cannot change the default admin account password", 403)
			return
		}
		// Verify old password for non-admin users（持锁校验，见
		// auth.VerifyHtpasswdPasswordLocked 注释——与后续重写共用 htpasswdMu）
		if userInfo == nil || userInfo.Role != "admin" {
			if body.OldPassword == "" {
				platform.WriteError(w, "Old password required", 400)
				return
			}
			if !auth.VerifyHtpasswdPasswordLocked(body.Username, body.OldPassword) {
				platform.WriteError(w, "Invalid old password", 401)
				return
			}
		}
		if err := auth.HtpasswdAddUser(body.Username, body.Password); err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		platform.WriteJSON(w, map[string]string{"status": "ok"})
	}
}

func ApiAddAdmin(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := platform.DecodeStrictJSON(r.Body, &body); err != nil {
			platform.WriteError(w, "Invalid JSON", 400)
			return
		}
		if body.Username == "" || body.Password == "" {
			platform.WriteError(w, "Username and password required", 400)
			return
		}
		if !auth.IsValidUsername(body.Username) {
			platform.WriteError(w, "Invalid username (use alphanumeric, underscore, hyphen)", 400)
			return
		}
		if auth.IsReservedUsername(body.Username) {
			platform.WriteError(w, "This username is reserved", 400)
			return
		}
		if body.Role == "" {
			body.Role = "viewer"
		}
		if body.Role != "admin" && body.Role != "viewer" {
			platform.WriteError(w, "Role must be admin or viewer", 400)
			return
		}
		// Only admin can create admin users
		userInfo, _ := auth.GetUserFromContext(r)
		if body.Role == "admin" && userInfo != nil && userInfo.Role != "admin" {
			platform.WriteError(w, "Forbidden", 403)
			return
		}
		// 受保护账号(AUTH_USER)已存在时禁止经此端点覆盖密码：auth.UpdateUsersMeta 的
		// 守卫只在"降级已有 admin"时触发，role 保持 admin 的重置密码路径会绕过
		// 它直达 auth.HtpasswdAddUser——与 ApiChangePassword 对同一攻击的阻断对齐
		// (防止任意 admin 重置超管密码接管账号，也让 UI 误填超管用户名时明确报错)。
		if body.Username == platform.GetEnv("AUTH_USER") {
			if doc, err := auth.ReadUsersMeta(); err == nil {
				if _, exists := doc.Users[body.Username]; exists {
					platform.WriteError(w, "Cannot modify the default admin account; use change-password instead", 403)
					return
				}
			}
		}

		// This handler doubles as the edit path for an existing username, so it
		// must enforce the same lockout guards as ApiSetRole when demoting an
		// admin, and must preserve durable fields instead of wiping them.
		// last-admin 检查与写入在 auth.UpdateUsersMeta 的单次持锁回调内完成(原子
		// read-check-write),消除两段式守卫的 TOCTOU。
		var previousInfo auth.UserInfo
		var hadPrevious bool
		if err := auth.UpdateUsersMeta(func(doc *auth.UsersMetaDoc) (bool, error) {
			prev, ok := doc.Users[body.Username]
			hadPrevious = ok
			if ok {
				previousInfo = prev
			}
			if ok && prev.Role == "admin" && body.Role != "admin" {
				if auth.IsProtectedAdmin(body.Username) {
					return false, auth.ErrProtectedAdmin
				}
				adminCount := 0
				for _, info := range doc.Users {
					if info.Role == "admin" {
						adminCount++
					}
				}
				if adminCount <= 1 {
					return false, auth.ErrLastAdmin
				}
			}
			newInfo := auth.UserInfo{
				Role:      body.Role,
				CreatedAt: time.Now().UTC().Format(time.RFC3339),
			}
			if ok {
				// Keep the existing XHTTP proxy password and creation time so we
				// don't break client configs or reset account metadata on edit.
				newInfo.ProxyPassword = prev.ProxyPassword
				if prev.CreatedAt != "" {
					newInfo.CreatedAt = prev.CreatedAt
				}
			}
			doc.Users[body.Username] = newInfo
			return true, nil
		}); err != nil {
			if errors.Is(err, auth.ErrLastAdmin) {
				platform.WriteError(w, "Cannot demote the last admin user", 400)
				return
			}
			if errors.Is(err, auth.ErrProtectedAdmin) {
				platform.WriteError(w, "Cannot demote the default admin account", 403)
				return
			}
			platform.WriteInternalError(w, err)
			return
		}
		if err := auth.HtpasswdAddUser(body.Username, body.Password); err != nil {
			var rollbackErr error
			if hadPrevious {
				rollbackErr = auth.UpdateUserMeta(body.Username, previousInfo)
			} else {
				rollbackErr = auth.DeleteUserMeta(body.Username)
			}
			if rollbackErr != nil {
				log.Printf("WARNING: failed to roll back user meta for %s after htpasswd error: %v", body.Username, rollbackErr)
			}
			platform.WriteInternalError(w, err)
			return
		}
		_, actor := auth.GetUserFromContext(r)
		log.Printf("[security] user upsert user=%s role=%s created=%t by=%s",
			body.Username, body.Role, !hadPrevious, actor)
		platform.WriteJSON(w, map[string]string{"status": "ok"})
	}
}

func ApiDeleteAdmin(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username string }
		if err := platform.DecodeStrictJSON(r.Body, &body); err != nil {
			platform.WriteError(w, "Invalid JSON", 400)
			return
		}
		if body.Username == "" {
			platform.WriteError(w, "Username required", 400)
			return
		}
		if auth.IsProtectedAdmin(body.Username) {
			platform.WriteError(w, "Cannot delete the default admin account", 403)
			return
		}
		_, currentUser := auth.GetUserFromContext(r)
		if body.Username == currentUser {
			platform.WriteError(w, "Cannot delete yourself", 400)
			return
		}

		// last-admin 检查与删除在 auth.UpdateUsersMeta 的单次持锁回调内完成(原子
		// read-check-write),消除两段式守卫的 TOCTOU——并发删除两个管理员时
		// 只有一个能通过计数检查。
		var deleted bool
		if err := auth.UpdateUsersMeta(func(doc *auth.UsersMetaDoc) (bool, error) {
			targetInfo, ok := doc.Users[body.Username]
			if !ok {
				return false, nil
			}
			deleted = true
			if targetInfo.Role == "admin" {
				adminCount := 0
				for _, info := range doc.Users {
					if info.Role == "admin" {
						adminCount++
					}
				}
				if adminCount <= 1 {
					return false, auth.ErrLastAdmin
				}
			}
			delete(doc.Users, body.Username)
			return true, nil
		}); err != nil {
			if errors.Is(err, auth.ErrLastAdmin) {
				platform.WriteError(w, "Cannot delete the last admin user", 400)
				return
			}
			platform.WriteInternalError(w, err)
			return
		}
		if !deleted {
			platform.WriteError(w, "User not found", 404)
			return
		}

		if err := auth.HtpasswdRemoveUser(body.Username); err != nil {
			// users.json 已删、nginx 凭证未清：面板登录已不可用（auth.RequireRole
			// 会以 "User not provisioned" 拒绝），但 htpasswd 行残留。如实
			// 上报部分成功，提示重试自愈，而非笼统 500。
			log.Printf("ERROR: remove htpasswd entry for %s: %v", body.Username, err)
			platform.WriteError(w, "用户已删除，但清理登录凭证失败，请重试删除以完成清理", http.StatusInternalServerError)
			return
		}

		// 撤销代理凭证：.htpasswd-proxy 从 users.json 重建，被删用户自动剔除。
		// 此前只在启动/首次创建 proxy 密码时重建，被删用户的 bcrypt 行会
		// 无限期残留，nginx /proxy/query 仍接受其密码。
		if err := auth.EnsureProxyHtpasswd(); err != nil {
			// 不作为整体失败：面板登录已删除，残留行在下次重建时失效。
			log.Printf("ERROR: revoke proxy credentials for %s: %v", body.Username, err)
		}

		// 联动删除该用户的 Xray 客户端：UUID 不随用户删除失效，留存等同
		// 保留完整代理访问权。有删除才重新生成配置并重启 xray。
		removed, err := deleteXrayClientsByOwner(body.Username)
		if err != nil {
			log.Printf("ERROR: delete xray clients for %s: %v", body.Username, err)
			platform.WriteJSON(w, map[string]string{"status": "ok"})
			return
		}
		if removed > 0 {
			if err := GenXrayConfig(); err != nil {
				log.Printf("ERROR: regenerate xray config after user delete: %v", err)
				platform.WriteJSON(w, map[string]string{"status": "ok"})
				return
			}
			restartCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), platform.RestartTimeout)
			defer cancel()
			if err := proxy.RestartXray(restartCtx, cli); err != nil {
				log.Printf("ERROR: restart xray after user delete: %v", err)
			}
		}
		platform.WriteJSON(w, map[string]string{"status": "ok"})
	}
}

func ApiSetRole(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Role     string `json:"role"`
		}
		if err := platform.DecodeStrictJSON(r.Body, &body); err != nil {
			platform.WriteError(w, "Invalid JSON", 400)
			return
		}
		if body.Username == "" {
			platform.WriteError(w, "Username and role required", 400)
			return
		}
		if body.Role != "admin" && body.Role != "viewer" {
			platform.WriteError(w, "Role must be admin or viewer", 400)
			return
		}

		// Protected admin account cannot be demoted
		if auth.IsProtectedAdmin(body.Username) && body.Role != "admin" {
			platform.WriteError(w, "Cannot demote the default admin account", 403)
			return
		}

		// last-admin 检查在 auth.UpdateUsersMeta 的持锁回调内完成(单次锁获取内
		// read-check-write):并发降级两个管理员时,只有一个能通过计数检查,
		// 杜绝"先读后写"两段式守卫的 TOCTOU。
		var notFound bool
		if err := auth.UpdateUsersMeta(func(doc *auth.UsersMetaDoc) (bool, error) {
			info, exists := doc.Users[body.Username]
			if !exists {
				notFound = true
				return false, nil
			}
			if body.Role != "admin" && info.Role == "admin" {
				adminCount := 0
				for username, other := range doc.Users {
					if other.Role == "admin" && username != body.Username {
						adminCount++
					}
				}
				if adminCount == 0 {
					return false, auth.ErrLastAdmin
				}
			}
			info.Role = body.Role
			doc.Users[body.Username] = info
			return true, nil
		}); err != nil {
			if errors.Is(err, auth.ErrLastAdmin) {
				platform.WriteError(w, "Cannot demote the last admin user", 400)
				return
			}
			platform.WriteInternalError(w, err)
			return
		}
		if notFound {
			platform.WriteError(w, "User not found", 404)
			return
		}
		platform.WriteJSON(w, map[string]string{"status": "ok"})
	}
}

func ApiWhoAmI(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info, username := auth.GetUserFromContext(r)
		if info == nil {
			platform.WriteError(w, "Unauthorized", 401)
			return
		}
		platform.WriteJSON(w, map[string]any{
			"username":   username,
			"role":       info.Role,
			"created_at": info.CreatedAt,
		})
	}
}
