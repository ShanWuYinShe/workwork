package accounts

import (
	"admin/internal/auth"
	"admin/internal/platform"
	"admin/internal/proxy"
	"context"
	"errors"
	"fmt"
	"github.com/docker/docker/client"
	"log"
	"net/http"
	"time"
)

// CascadeDeleteUploads 在删除用户时级联清理其名下资源,由 main 装配注入
// (transfer.DeleteUploadsByUploader)。依赖方向约束:transfer 依赖 accounts
// 之下的层,accounts 不能直接 import transfer,故用钩子注入而不是直接调用。
// 未装配(nil)时跳过级联——单元测试环境无需构造 transfer 状态。
var CascadeDeleteUploads func(uploader string) (int, error)

// minPasswordLen 是密码最小长度。此前无任何强度约束,viewer 可把密码设成
// "1"——面板 basic auth 是唯一的登录门,空强度密码直接击穿它。
const minPasswordLen = 8

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
			XHTTPDomain:    platform.GetEnv("XHTTP_DOMAIN"),
			XHTTPAddress:   platform.GetEnv("XHTTP_ADDRESS"),
			XHTTPProxyAuth: proxyAuth,
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
			htpasswdUsers, err := auth.ReadHtpasswdUsers()
			if err != nil {
				log.Printf("read htpasswd: %v", err)
				platform.WriteInternalError(w, err)
				return
			}
			for _, u := range htpasswdUsers {
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
			platform.WriteError(w, "请求体不是合法 JSON", 400)
			return
		}
		if body.Username == "" || body.Password == "" {
			platform.WriteError(w, "用户名与密码必填", 400)
			return
		}
		if len(body.Password) < minPasswordLen {
			platform.WriteError(w, fmt.Sprintf("密码至少 %d 个字符", minPasswordLen), 400)
			return
		}
		if !auth.IsValidUsername(body.Username) {
			platform.WriteError(w, "用户名不合法（仅字母/数字/下划线/连字符）", 400)
			return
		}
		// Non-admin can only change their own password
		if userInfo != nil && userInfo.Role != "admin" && body.Username != username {
			platform.WriteError(w, "无权操作：只能修改自己的密码", 403)
			return
		}
		// 目标用户必须存在于 users.json:此前 admin 可为不存在的用户名写入
		// htpasswd——能过 nginx basic auth、被 Go 以"用户未初始化"403,留下
		// htpasswd/users.json 双真相源漂移的幽灵凭证。
		if doc, err := auth.ReadUsersMeta(); err != nil {
			platform.WriteInternalError(w, err)
			return
		} else if _, ok := doc.Users[body.Username]; !ok {
			platform.WriteError(w, "用户不存在", 404)
			return
		}
		// Protected admin account (AUTH_USER) can only change its own password:
		// 防止任意 admin 重置超级管理员密码并接管该账号(纵深防御,与
		// ApiSetRole/ApiDeleteAdmin 的 auth.IsProtectedAdmin 守卫对齐)。
		if body.Username == platform.GetEnv("AUTH_USER") && (userInfo == nil || username != body.Username) {
			platform.WriteError(w, "内置管理员账号密码请经修改密码功能变更", 403)
			return
		}
		// Verify old password for non-admin users（校验+重写在单次持锁内完成,
		// 消除两段式之间并发改密互相覆盖的窗口）
		if userInfo == nil || userInfo.Role != "admin" {
			if body.OldPassword == "" {
				platform.WriteError(w, "旧密码必填", 400)
				return
			}
			ok, err := auth.HtpasswdChangePassword(body.Username, body.OldPassword, body.Password)
			if err != nil {
				platform.WriteInternalError(w, err)
				return
			}
			if !ok {
				platform.WriteError(w, "旧密码不正确", 401)
				return
			}
			log.Printf("[security] password change user=%s by=%s", body.Username, username)
			platform.WriteJSON(w, map[string]string{"status": "ok"})
			return
		}
		if err := auth.HtpasswdAddUser(body.Username, body.Password); err != nil {
			platform.WriteInternalError(w, err)
			return
		}
		log.Printf("[security] password change user=%s by=%s", body.Username, username)
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
			platform.WriteError(w, "请求体不是合法 JSON", 400)
			return
		}
		if body.Username == "" || body.Password == "" {
			platform.WriteError(w, "用户名与密码必填", 400)
			return
		}
		if len(body.Password) < minPasswordLen {
			platform.WriteError(w, fmt.Sprintf("密码至少 %d 个字符", minPasswordLen), 400)
			return
		}
		if !auth.IsValidUsername(body.Username) {
			platform.WriteError(w, "用户名不合法（仅字母/数字/下划线/连字符）", 400)
			return
		}
		if auth.IsReservedUsername(body.Username) {
			platform.WriteError(w, "该用户名为保留名", 400)
			return
		}
		if body.Role == "" {
			body.Role = "viewer"
		}
		if body.Role != "admin" && body.Role != "viewer" {
			platform.WriteError(w, "角色必须是 admin 或 viewer", 400)
			return
		}
		// Only admin can create admin users
		userInfo, _ := auth.GetUserFromContext(r)
		if body.Role == "admin" && userInfo != nil && userInfo.Role != "admin" {
			platform.WriteError(w, "无权限", 403)
			return
		}
		// 受保护账号(AUTH_USER)已存在时禁止经此端点覆盖密码：auth.UpdateUsersMeta 的
		// 守卫只在"降级已有 admin"时触发，role 保持 admin 的重置密码路径会绕过
		// 它直达 auth.HtpasswdAddUser——与 ApiChangePassword 对同一攻击的阻断对齐
		// (防止任意 admin 重置超管密码接管账号，也让 UI 误填超管用户名时明确报错)。
		if body.Username == platform.GetEnv("AUTH_USER") {
			doc, err := auth.ReadUsersMeta()
			if err != nil {
				// fail-closed:读不到现状时拒绝写入,防止绕过内置管理员保护。
				log.Printf("read users meta for protected-admin guard: %v", err)
				platform.WriteInternalError(w, err)
				return
			}
			if _, exists := doc.Users[body.Username]; exists {
				platform.WriteError(w, "内置管理员账号不可修改，请使用修改密码功能", 403)
				return
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
				platform.WriteErrorCode(w, "不能降级最后一个管理员", 400, "LAST_ADMIN")
				return
			}
			if errors.Is(err, auth.ErrProtectedAdmin) {
				platform.WriteError(w, "不能改动内置管理员账号的角色", 403)
				return
			}
			platform.WriteInternalError(w, err)
			return
		}
		if err := auth.HtpasswdAddUser(body.Username, body.Password); err != nil {
			// 回滚禁止用旧快照整条覆盖(UpdateUserMeta):bcrypt 耗时窗口内并发
			// SetRole/改密会被静默回滚(丢失更新,可把被降级的 admin 恢复成
			// admin)。锁内重读,仅当条目仍是本次写入的角色时回退该角色。
			rollbackErr := auth.UpdateUsersMeta(func(doc *auth.UsersMetaDoc) (bool, error) {
				cur, ok := doc.Users[body.Username]
				if !ok {
					if !hadPrevious {
						return false, nil // 已被并发删除,无需回滚
					}
					doc.Users[body.Username] = previousInfo
					return true, nil
				}
				if cur.Role != body.Role {
					return false, nil // 已被并发修改,保留现状不覆盖
				}
				if hadPrevious {
					cur.Role = previousInfo.Role
					doc.Users[body.Username] = cur
					return true, nil
				}
				delete(doc.Users, body.Username)
				return true, nil
			})
			if rollbackErr != nil {
				log.Printf("WARNING: failed to roll back user meta for %q after htpasswd error: %v", body.Username, rollbackErr)
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
			platform.WriteError(w, "请求体不是合法 JSON", 400)
			return
		}
		if body.Username == "" {
			platform.WriteError(w, "用户名必填", 400)
			return
		}
		if auth.IsProtectedAdmin(body.Username) {
			platform.WriteError(w, "不能删除内置管理员账号", 403)
			return
		}
		_, currentUser := auth.GetUserFromContext(r)
		if body.Username == currentUser {
			platform.WriteError(w, "不能删除自己", 400)
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
				platform.WriteErrorCode(w, "不能删除最后一个管理员", 400, "LAST_ADMIN")
				return
			}
			platform.WriteInternalError(w, err)
			return
		}
		if !deleted {
			platform.WriteError(w, "用户不存在", 404)
			return
		}

		if err := auth.HtpasswdRemoveUser(body.Username); err != nil {
			// users.json 已删、nginx 凭证未清：面板登录已不可用（auth.RequireRole
			// 会以 "用户未初始化" 拒绝），但 htpasswd 行残留。如实
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
		// 级联失败不再假报 ok：用户已删但 UUID 在旧 config 里仍然有效,
		// 漂移窗口无上限——如实 500 让调用方知道需要跟进(与上方 htpasswd
		// 部分失败的处理模式一致)。
		const xrayCascadeFailMsg = "用户已删除，但联动更新 Xray 配置失败，其代理 UUID 在配置生效前仍然有效，请重启 Xray 或检查客户端列表"
		removed, err := deleteXrayClientsByOwner(body.Username)
		if err != nil {
			log.Printf("ERROR: delete xray clients for %s: %v", body.Username, err)
			platform.WriteError(w, xrayCascadeFailMsg, http.StatusInternalServerError)
			return
		}
		if removed > 0 {
			if err := GenXrayConfig(); err != nil {
				log.Printf("ERROR: regenerate xray config after user delete: %v", err)
				platform.WriteError(w, xrayCascadeFailMsg, http.StatusInternalServerError)
				return
			}
			restartCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), platform.RestartTimeout)
			defer cancel()
			if err := proxy.RestartXray(restartCtx, cli); err != nil {
				log.Printf("ERROR: restart xray after user delete: %v", err)
				platform.WriteError(w, xrayCascadeFailMsg, http.StatusInternalServerError)
				return
			}
		}
		// 级联清理其名下的 transfer 上传:用户已删,文件既无法自助管理,
		// 配额也无人释放。级联失败不作为整体失败(用户已删,文件 7 天后
		// 随过期清理回收),只记日志并告知。
		uploadsRemoved := 0
		if CascadeDeleteUploads != nil {
			if n, err := CascadeDeleteUploads(body.Username); err != nil {
				log.Printf("ERROR: cascade delete uploads for %s: %v", body.Username, err)
			} else {
				uploadsRemoved = n
			}
		}
		_, actor := auth.GetUserFromContext(r)
		log.Printf("[security] user delete user=%q xray_clients_removed=%d uploads_removed=%d by=%s", body.Username, removed, uploadsRemoved, actor)
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
			platform.WriteError(w, "请求体不是合法 JSON", 400)
			return
		}
		if body.Username == "" {
			platform.WriteError(w, "用户名与角色必填", 400)
			return
		}
		if body.Role != "admin" && body.Role != "viewer" {
			platform.WriteError(w, "角色必须是 admin 或 viewer", 400)
			return
		}

		// Protected admin account cannot be demoted
		if auth.IsProtectedAdmin(body.Username) && body.Role != "admin" {
			platform.WriteError(w, "不能改动内置管理员账号的角色", 403)
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
				platform.WriteErrorCode(w, "不能降级最后一个管理员", 400, "LAST_ADMIN")
				return
			}
			platform.WriteInternalError(w, err)
			return
		}
		if notFound {
			platform.WriteError(w, "用户不存在", 404)
			return
		}
		_, actor := auth.GetUserFromContext(r)
		log.Printf("[security] role set user=%q role=%s by=%s", body.Username, body.Role, actor)
		platform.WriteJSON(w, map[string]string{"status": "ok"})
	}
}

func ApiWhoAmI(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info, username := auth.GetUserFromContext(r)
		if info == nil {
			platform.WriteError(w, "未认证", 401)
			return
		}
		platform.WriteJSON(w, map[string]any{
			"username":   username,
			"role":       info.Role,
			"created_at": info.CreatedAt,
		})
	}
}
