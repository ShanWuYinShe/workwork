package main

import (
	"context"
	"errors"
	"github.com/docker/docker/client"
	"log"
	"net/http"
	"regexp"
	"time"
)

// -- User management APIs --
func apiUsers(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := getUserFromContext(r)
		allClients, err := readXrayClientMaps()
		if err != nil {
			log.Printf("Failed to read clients: %v", err)
			writeInternalError(w, err)
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
				proxyAuth = proxyAuthForUserRead(username)
			}
		}
		genJSON := GenerateClientConfig(filtered, ClientConfig{
			RealityDomain:     getEnv("XRAY_REALITY_DOMAIN"),
			RealityServerName: getEnv("XRAY_REALITY_SERVER_NAME"),
			RealityPublicKey:  getEnv("XRAY_PUBLIC_KEY"),
			RealityShortID:    xrayClientShortID(),
			XHTTPDomain:       getEnv("XHTTP_DOMAIN"),
			XHTTPProxyAuth:    proxyAuth,
		})

		// Build adminUsers with role info
		doc, err := readUsersMeta()
		if err != nil {
			writeInternalError(w, err)
			return
		}
		type adminUserJSON struct {
			Username  string `json:"username"`
			Role      string `json:"role"`
			CreatedAt string `json:"created_at"`
		}
		var adminUsers []adminUserJSON
		if userInfo != nil && userInfo.Role == "admin" {
			for _, u := range readHtpasswdUsers() {
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

		writeJSON(w, map[string]any{
			"xrayClients": filtered, "generatedJSON": genJSON, "adminUsers": adminUsers,
		})
	}
}

var validUsername = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func isValidUsername(username string) bool {
	return username != "" && validUsername.MatchString(username) && len(username) <= 64
}

// isReservedUsername 内部伪身份名：X-Internal-Token / X-Fail2Ban-Token 请求在
// 审计与上下文中以此名字出现（middleware requireRole）。创建同名真实用户会
// 与其冲突——该用户永远拿不到 XHTTP proxy 密码（proxyAuthForUser 硬编码排除），
// 且审计日志无法区分内部请求与真人操作。
func isReservedUsername(username string) bool {
	return username == "internal" || username == "fail2ban"
}

func apiChangePassword(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := getUserFromContext(r)
		var body struct {
			Username    string `json:"username"`
			OldPassword string `json:"old_password"`
			Password    string `json:"password"`
		}
		if err := decodeStrictJSON(r.Body, &body); err != nil {
			writeError(w, "Invalid JSON", 400)
			return
		}
		if body.Username == "" || body.Password == "" {
			writeError(w, "Username and password required", 400)
			return
		}
		if !isValidUsername(body.Username) {
			writeError(w, "Invalid username (use alphanumeric, underscore, hyphen)", 400)
			return
		}
		// Non-admin can only change their own password
		if userInfo != nil && userInfo.Role != "admin" && body.Username != username {
			writeError(w, "Forbidden: can only change your own password", 403)
			return
		}
		// Protected admin account (AUTH_USER) can only change its own password:
		// 防止任意 admin 重置超级管理员密码并接管该账号(纵深防御,与
		// apiSetRole/apiDeleteAdmin 的 isProtectedAdmin 守卫对齐)。
		if body.Username == getEnv("AUTH_USER") && (userInfo == nil || username != body.Username) {
			writeError(w, "Cannot change the default admin account password", 403)
			return
		}
		// Verify old password for non-admin users（持锁校验，见
		// verifyHtpasswdPasswordLocked 注释——与后续重写共用 htpasswdMu）
		if userInfo == nil || userInfo.Role != "admin" {
			if body.OldPassword == "" {
				writeError(w, "Old password required", 400)
				return
			}
			if !verifyHtpasswdPasswordLocked(body.Username, body.OldPassword) {
				writeError(w, "Invalid old password", 401)
				return
			}
		}
		if err := htpasswdAddUser(body.Username, body.Password); err != nil {
			writeInternalError(w, err)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiAddAdmin(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Role     string `json:"role"`
		}
		if err := decodeStrictJSON(r.Body, &body); err != nil {
			writeError(w, "Invalid JSON", 400)
			return
		}
		if body.Username == "" || body.Password == "" {
			writeError(w, "Username and password required", 400)
			return
		}
		if !isValidUsername(body.Username) {
			writeError(w, "Invalid username (use alphanumeric, underscore, hyphen)", 400)
			return
		}
		if isReservedUsername(body.Username) {
			writeError(w, "This username is reserved", 400)
			return
		}
		if body.Role == "" {
			body.Role = "viewer"
		}
		if body.Role != "admin" && body.Role != "viewer" {
			writeError(w, "Role must be admin or viewer", 400)
			return
		}
		// Only admin can create admin users
		userInfo, _ := getUserFromContext(r)
		if body.Role == "admin" && userInfo != nil && userInfo.Role != "admin" {
			writeError(w, "Forbidden", 403)
			return
		}
		// 受保护账号(AUTH_USER)已存在时禁止经此端点覆盖密码：updateUsersMeta 的
		// 守卫只在"降级已有 admin"时触发，role 保持 admin 的重置密码路径会绕过
		// 它直达 htpasswdAddUser——与 apiChangePassword 对同一攻击的阻断对齐
		// (防止任意 admin 重置超管密码接管账号，也让 UI 误填超管用户名时明确报错)。
		if body.Username == getEnv("AUTH_USER") {
			if doc, err := readUsersMeta(); err == nil {
				if _, exists := doc.Users[body.Username]; exists {
					writeError(w, "Cannot modify the default admin account; use change-password instead", 403)
					return
				}
			}
		}

		// This handler doubles as the edit path for an existing username, so it
		// must enforce the same lockout guards as apiSetRole when demoting an
		// admin, and must preserve durable fields instead of wiping them.
		// last-admin 检查与写入在 updateUsersMeta 的单次持锁回调内完成(原子
		// read-check-write),消除两段式守卫的 TOCTOU。
		var previousInfo UserInfo
		var hadPrevious bool
		if err := updateUsersMeta(func(doc *usersMetaDoc) (bool, error) {
			prev, ok := doc.Users[body.Username]
			hadPrevious = ok
			if ok {
				previousInfo = prev
			}
			if ok && prev.Role == "admin" && body.Role != "admin" {
				if isProtectedAdmin(body.Username) {
					return false, errProtectedAdmin
				}
				adminCount := 0
				for _, info := range doc.Users {
					if info.Role == "admin" {
						adminCount++
					}
				}
				if adminCount <= 1 {
					return false, errLastAdmin
				}
			}
			newInfo := UserInfo{
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
			if errors.Is(err, errLastAdmin) {
				writeError(w, "Cannot demote the last admin user", 400)
				return
			}
			if errors.Is(err, errProtectedAdmin) {
				writeError(w, "Cannot demote the default admin account", 403)
				return
			}
			writeInternalError(w, err)
			return
		}
		if err := htpasswdAddUser(body.Username, body.Password); err != nil {
			var rollbackErr error
			if hadPrevious {
				rollbackErr = updateUserMeta(body.Username, previousInfo)
			} else {
				rollbackErr = deleteUserMeta(body.Username)
			}
			if rollbackErr != nil {
				log.Printf("WARNING: failed to roll back user meta for %s after htpasswd error: %v", body.Username, rollbackErr)
			}
			writeInternalError(w, err)
			return
		}
		_, actor := getUserFromContext(r)
		log.Printf("[security] user upsert user=%s role=%s created=%t by=%s",
			body.Username, body.Role, !hadPrevious, actor)
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiDeleteAdmin(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username string }
		if err := decodeStrictJSON(r.Body, &body); err != nil {
			writeError(w, "Invalid JSON", 400)
			return
		}
		if body.Username == "" {
			writeError(w, "Username required", 400)
			return
		}
		if isProtectedAdmin(body.Username) {
			writeError(w, "Cannot delete the default admin account", 403)
			return
		}
		_, currentUser := getUserFromContext(r)
		if body.Username == currentUser {
			writeError(w, "Cannot delete yourself", 400)
			return
		}

		// last-admin 检查与删除在 updateUsersMeta 的单次持锁回调内完成(原子
		// read-check-write),消除两段式守卫的 TOCTOU——并发删除两个管理员时
		// 只有一个能通过计数检查。
		var deleted bool
		if err := updateUsersMeta(func(doc *usersMetaDoc) (bool, error) {
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
					return false, errLastAdmin
				}
			}
			delete(doc.Users, body.Username)
			return true, nil
		}); err != nil {
			if errors.Is(err, errLastAdmin) {
				writeError(w, "Cannot delete the last admin user", 400)
				return
			}
			writeInternalError(w, err)
			return
		}
		if !deleted {
			writeError(w, "User not found", 404)
			return
		}

		if err := htpasswdRemoveUser(body.Username); err != nil {
			// users.json 已删、nginx 凭证未清：面板登录已不可用（requireRole
			// 会以 "User not provisioned" 拒绝），但 htpasswd 行残留。如实
			// 上报部分成功，提示重试自愈，而非笼统 500。
			log.Printf("ERROR: remove htpasswd entry for %s: %v", body.Username, err)
			writeError(w, "用户已删除，但清理登录凭证失败，请重试删除以完成清理", http.StatusInternalServerError)
			return
		}

		// 撤销代理凭证：.htpasswd-proxy 从 users.json 重建，被删用户自动剔除。
		// 此前只在启动/首次创建 proxy 密码时重建，被删用户的 bcrypt 行会
		// 无限期残留，nginx /proxy/query 仍接受其密码。
		if err := genProxyHtpasswd(); err != nil {
			// 不作为整体失败：面板登录已删除，残留行在下次重建时失效。
			log.Printf("ERROR: revoke proxy credentials for %s: %v", body.Username, err)
		}

		// 联动删除该用户的 Xray 客户端：UUID 不随用户删除失效，留存等同
		// 保留完整代理访问权。有删除才重新生成配置并重启 xray。
		removed, err := deleteXrayClientsByOwner(body.Username)
		if err != nil {
			log.Printf("ERROR: delete xray clients for %s: %v", body.Username, err)
			writeJSON(w, map[string]string{"status": "ok"})
			return
		}
		if removed > 0 {
			if err := genXrayConfig(); err != nil {
				log.Printf("ERROR: regenerate xray config after user delete: %v", err)
				writeJSON(w, map[string]string{"status": "ok"})
				return
			}
			restartCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), restartTimeout)
			defer cancel()
			if err := restartXray(restartCtx, cli); err != nil {
				log.Printf("ERROR: restart xray after user delete: %v", err)
			}
		}
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiSetRole(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Role     string `json:"role"`
		}
		if err := decodeStrictJSON(r.Body, &body); err != nil {
			writeError(w, "Invalid JSON", 400)
			return
		}
		if body.Username == "" {
			writeError(w, "Username and role required", 400)
			return
		}
		if body.Role != "admin" && body.Role != "viewer" {
			writeError(w, "Role must be admin or viewer", 400)
			return
		}

		// Protected admin account cannot be demoted
		if isProtectedAdmin(body.Username) && body.Role != "admin" {
			writeError(w, "Cannot demote the default admin account", 403)
			return
		}

		// last-admin 检查在 updateUsersMeta 的持锁回调内完成(单次锁获取内
		// read-check-write):并发降级两个管理员时,只有一个能通过计数检查,
		// 杜绝"先读后写"两段式守卫的 TOCTOU。
		var notFound bool
		if err := updateUsersMeta(func(doc *usersMetaDoc) (bool, error) {
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
					return false, errLastAdmin
				}
			}
			info.Role = body.Role
			doc.Users[body.Username] = info
			return true, nil
		}); err != nil {
			if errors.Is(err, errLastAdmin) {
				writeError(w, "Cannot demote the last admin user", 400)
				return
			}
			writeInternalError(w, err)
			return
		}
		if notFound {
			writeError(w, "User not found", 404)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiWhoAmI(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info, username := getUserFromContext(r)
		if info == nil {
			writeError(w, "Unauthorized", 401)
			return
		}
		writeJSON(w, map[string]any{
			"username":   username,
			"role":       info.Role,
			"created_at": info.CreatedAt,
		})
	}
}
