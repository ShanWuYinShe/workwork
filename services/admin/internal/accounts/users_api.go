package accounts

import (
	"admin/internal/auth"
	"admin/internal/identity"
	"admin/internal/platform"
	"admin/internal/proxy"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/docker/docker/client"
)

// CascadeDeleteUploads 在删除用户时级联清理其名下资源,由 main 装配注入
// (transfer.DeleteUploadsByUploader)。依赖方向约束:transfer 依赖 accounts
// 之下的层,accounts 不能直接 import transfer,故用钩子注入而不是直接调用。
// 未装配(nil)时跳过级联——单元测试环境无需构造 transfer 状态。
var CascadeDeleteUploads func(uploader string) (int, error)

// -- User management APIs --

func ApiUsers(users *identity.Store) http.HandlerFunc {
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

		// 只读：GET 端点不创建 proxy 密码。generatedJSON 只服务"当前用户自己的
		// 客户端"：admin 视角含他人客户端时留空，避免把他人 UUID/明文凭证
		// 嵌进返回配置。
		allOwned := len(filtered) > 0
		for _, c := range filtered {
			if c["owner"] != username {
				allOwned = false
				break
			}
		}
		proxyAuth := ""
		genJSON := ""
		if allOwned {
			proxyAuth = users.ProxyAuthForUserRead(username)
			genJSON = GenerateClientConfig(filtered, ClientConfig{
				XHTTPDomain:    platform.GetEnv("XHTTP_DOMAIN"),
				XHTTPAddress:   platform.GetEnv("XHTTP_ADDRESS"),
				XHTTPALPN:      platform.GetEnv("XHTTP_ALPN"),
				XHTTPProxyAuth: proxyAuth,
			})
		}

		doc, htpasswdUsers, err := users.AdminView()
		if err != nil {
			log.Printf("read users/htpasswd: %v", err)
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
			for _, u := range htpasswdUsers {
				au := adminUserJSON{Username: u, Role: "viewer"}
				if info, ok := doc.Users[u]; ok {
					au.Role = info.Role
					au.CreatedAt = info.CreatedAt
				}
				adminUsers = append(adminUsers, au)
			}
		} else if curInfo, ok := doc.Users[username]; ok {
			adminUsers = []adminUserJSON{{Username: username, Role: curInfo.Role, CreatedAt: curInfo.CreatedAt}}
		} else {
			adminUsers = []adminUserJSON{{Username: username, Role: "viewer"}}
		}

		platform.WriteJSON(w, map[string]any{
			"xrayClients": filtered, "generatedJSON": genJSON, "adminUsers": adminUsers,
		})
	}
}

func ApiChangePassword(users *identity.Store) http.HandlerFunc {
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
		if len(body.Password) < identity.MinPasswordLen {
			platform.WriteError(w, fmt.Sprintf("密码至少 %d 个字符", identity.MinPasswordLen), 400)
			return
		}
		if !identity.IsValidUsername(body.Username) {
			platform.WriteError(w, "用户名不合法（仅字母/数字/下划线/连字符）", 400)
			return
		}
		// Non-admin can only change their own password.
		if userInfo != nil && userInfo.Role != "admin" && body.Username != username {
			platform.WriteError(w, "无权操作：只能修改自己的密码", 403)
			return
		}
		// 内置管理员只能改自己的密码，防止任意 admin 重置超管密码接管账号。
		if users.IsProtected(body.Username) && (userInfo == nil || username != body.Username) {
			platform.WriteError(w, "内置管理员账号密码请经修改密码功能变更", 403)
			return
		}
		requireOld := userInfo == nil || userInfo.Role != "admin"
		if requireOld && body.OldPassword == "" {
			platform.WriteError(w, "旧密码必填", 400)
			return
		}
		ok, err := users.ChangePassword(body.Username, body.OldPassword, body.Password, requireOld)
		switch {
		case err == nil:
		case errors.Is(err, identity.ErrUserNotFound):
			platform.WriteError(w, "用户不存在", 404)
			return
		case errors.Is(err, identity.ErrPasswordTooShort):
			platform.WriteError(w, fmt.Sprintf("密码至少 %d 个字符", identity.MinPasswordLen), 400)
			return
		default:
			platform.WriteInternalError(w, err)
			return
		}
		if !ok {
			platform.WriteError(w, "旧密码不正确", 401)
			return
		}
		log.Printf("[security] password change user=%s by=%s", body.Username, username)
		platform.WriteJSON(w, map[string]string{"status": "ok"})
	}
}

func ApiAddAdmin(users *identity.Store) http.HandlerFunc {
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
		if len(body.Password) < identity.MinPasswordLen {
			platform.WriteError(w, fmt.Sprintf("密码至少 %d 个字符", identity.MinPasswordLen), 400)
			return
		}
		if !identity.IsValidUsername(body.Username) {
			platform.WriteError(w, "用户名不合法（仅字母/数字/下划线/连字符）", 400)
			return
		}
		if identity.IsReservedUsername(body.Username) {
			platform.WriteError(w, "该用户名为保留名", 400)
			return
		}
		if body.Role != "" && body.Role != "admin" && body.Role != "viewer" {
			platform.WriteError(w, "角色必须是 admin 或 viewer", 400)
			return
		}
		userInfo, _ := auth.GetUserFromContext(r)
		if body.Role == "admin" && userInfo != nil && userInfo.Role != "admin" {
			platform.WriteError(w, "无权限", 403)
			return
		}

		result, err := users.UpsertUser(body.Username, body.Password, body.Role)
		if err != nil {
			switch {
			case errors.Is(err, identity.ErrLastAdmin):
				platform.WriteErrorCode(w, "不能降级最后一个管理员", 400, "LAST_ADMIN")
			case errors.Is(err, identity.ErrProtectedAdmin):
				platform.WriteError(w, "不能改动内置管理员账号的角色", 403)
			case errors.Is(err, identity.ErrProtectedAccount):
				platform.WriteError(w, "内置管理员账号不可修改，请使用修改密码功能", 403)
			case errors.Is(err, identity.ErrPasswordTooShort):
				platform.WriteError(w, fmt.Sprintf("密码至少 %d 个字符", identity.MinPasswordLen), 400)
			case errors.Is(err, identity.ErrInvalidUsername):
				platform.WriteError(w, "用户名不合法（仅字母/数字/下划线/连字符）", 400)
			case errors.Is(err, identity.ErrInvalidRole):
				platform.WriteError(w, "角色必须是 admin 或 viewer", 400)
			default:
				platform.WriteInternalError(w, err)
			}
			return
		}
		_, actor := auth.GetUserFromContext(r)
		log.Printf("[security] user upsert user=%s role=%s created=%t by=%s",
			body.Username, result.Role, result.Created, actor)
		platform.WriteJSON(w, map[string]string{"status": "ok"})
	}
}

func ApiDeleteAdmin(users *identity.Store, cli *client.Client) http.HandlerFunc {
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
		if users.IsProtected(body.Username) {
			platform.WriteError(w, "不能删除内置管理员账号", 403)
			return
		}
		_, currentUser := auth.GetUserFromContext(r)
		if body.Username == currentUser {
			platform.WriteError(w, "不能删除自己", 400)
			return
		}

		// 与 ApiAddXray 串行化：删除流程与在途 add 并发时，后者可以在
		// users.json/xray-clients.json 里把被删用户/客户端写回来。
		userLifecycleMu.Lock()
		defer userLifecycleMu.Unlock()

		deleted, err := users.DeleteUser(body.Username)
		if err != nil {
			switch {
			case errors.Is(err, identity.ErrLastAdmin):
				platform.WriteErrorCode(w, "不能删除最后一个管理员", 400, "LAST_ADMIN")
			case errors.Is(err, identity.ErrProtectedAccount):
				platform.WriteError(w, "不能删除内置管理员账号", 403)
			default:
				log.Printf("ERROR: delete user %s: %v", body.Username, err)
				platform.WriteError(w, "用户已删除，但清理登录/代理凭证失败，请重试删除以完成清理", http.StatusInternalServerError)
			}
			return
		}

		// 联动删除该用户的 Xray 客户端。重试路径（!deleted）即使没有可删的
		// 台账条目，也要重新生成并重启——上一次可能已删台账但没来得及让
		// xray 加载。
		const xrayCascadeFailMsg = "用户已删除，但联动更新 Xray 配置失败，其代理 UUID 在配置生效前仍然有效，请重启 Xray 或重试删除"
		removed, err := deleteXrayClientsByOwner(body.Username)
		if err != nil {
			log.Printf("ERROR: delete xray clients for %s: %v", body.Username, err)
			platform.WriteError(w, xrayCascadeFailMsg, http.StatusInternalServerError)
			return
		}
		if removed > 0 || !deleted {
			changed, err := GenXrayConfig()
			if err != nil {
				log.Printf("ERROR: regenerate xray config after user delete: %v", err)
				platform.WriteError(w, xrayCascadeFailMsg, http.StatusInternalServerError)
				return
			}
			if changed || !deleted {
				restartCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), platform.RestartTimeout)
				err := proxy.RestartXray(restartCtx, cli)
				cancel()
				if err != nil {
					log.Printf("ERROR: restart xray after user delete: %v", err)
					platform.WriteError(w, xrayCascadeFailMsg, http.StatusInternalServerError)
					return
				}
			}
		}
		// 级联清理其名下的 transfer 上传：用户已删，文件既无法自助管理，
		// 配额也无人释放。级联失败不作为整体失败（文件随过期清理回收），
		// 只记日志并告知。
		uploadsRemoved := 0
		if CascadeDeleteUploads != nil {
			if n, err := CascadeDeleteUploads(body.Username); err != nil {
				log.Printf("ERROR: cascade delete uploads for %s: %v", body.Username, err)
			} else {
				uploadsRemoved = n
			}
		}
		_, actor := auth.GetUserFromContext(r)
		log.Printf("[security] user delete user=%q deleted=%t xray_clients_removed=%d uploads_removed=%d by=%s", body.Username, deleted, removed, uploadsRemoved, actor)
		platform.WriteJSON(w, map[string]string{"status": "ok"})
	}
}

func ApiSetRole(users *identity.Store) http.HandlerFunc {
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

		err := users.SetRole(body.Username, body.Role)
		switch {
		case err == nil:
		case errors.Is(err, identity.ErrUserNotFound):
			platform.WriteError(w, "用户不存在", 404)
			return
		case errors.Is(err, identity.ErrProtectedAdmin):
			platform.WriteError(w, "不能改动内置管理员账号的角色", 403)
			return
		case errors.Is(err, identity.ErrLastAdmin):
			platform.WriteErrorCode(w, "不能降级最后一个管理员", 400, "LAST_ADMIN")
			return
		default:
			platform.WriteInternalError(w, err)
			return
		}
		_, actor := auth.GetUserFromContext(r)
		log.Printf("[security] role set user=%q role=%s by=%s", body.Username, body.Role, actor)
		platform.WriteJSON(w, map[string]string{"status": "ok"})
	}
}

func ApiWhoAmI() http.HandlerFunc {
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
