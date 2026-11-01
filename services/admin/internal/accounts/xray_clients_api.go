package accounts

import (
	"admin/internal/identity"
	"admin/internal/platform"
	"admin/internal/requestctx"
	"admin/internal/xray"
	"context"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
)

func ApiAddXray(users *identity.Store, svc *xray.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, _ := requestctx.ActorFrom(r.Context())

		var body struct {
			UUID  string `json:"uuid"`
			Owner string `json:"owner"`
			Name  string `json:"name"`
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
		uuid := strings.ToLower(body.UUID)

		// 客户端归属：登录用户是 nginx 认证身份，客户端是它名下的代理身份，
		// 一个用户可以创建多个。admin 可以代任意存在的用户创建（owner 字段）；
		// 非 admin 只能为自己创建。
		owner := actor.Username
		if strings.TrimSpace(body.Owner) != "" {
			owner = strings.TrimSpace(body.Owner)
		}
		isAdmin := actor.Role == identity.RoleAdmin
		if !isAdmin && owner != actor.Username {
			platform.WriteError(w, "无权操作：只能为自己创建客户端", 403)
			return
		}
		if !identity.IsValidUsername(owner) || identity.IsReservedUsername(owner) {
			platform.WriteError(w, "owner 用户名不合法或为保留名", 400)
			return
		}

		// 与删除面板用户串行化：否则删除流程结束后在途的 add 仍会把 proxy
		// 凭证/客户端写回去，被删用户重新获得代理身份。
		userLifecycleMu.Lock()
		defer userLifecycleMu.Unlock()

		exists, err := users.Exists(owner)
		if err != nil {
			log.Printf("check user %q: %v", owner, err)
			platform.WriteInternalError(w, err)
			return
		}
		if !exists {
			platform.WriteError(w, "owner 用户不存在", 404)
			return
		}
		if _, err := users.EnsureProxyPassword(owner); err != nil {
			if errors.Is(err, identity.ErrUserNotFound) {
				platform.WriteError(w, "owner 用户不存在", 404)
				return
			}
			log.Printf("Failed to create proxy password for %s: %v", owner, err)
			platform.WriteInternalError(w, err)
			return
		}

		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), platform.RestartTimeout)
		defer cancel()
		created, err := svc.Add(ctx, xray.Client{ID: uuid, Owner: owner, Name: body.Name})
		if err != nil {
			switch {
			case errors.Is(err, xray.ErrDuplicateID):
				platform.WriteError(w, "该 UUID 的客户端已存在", 409)
			case errors.Is(err, xray.ErrDuplicateName):
				platform.WriteError(w, "客户端用户名已被占用", 409)
			case errors.Is(err, xray.ErrInvalidName):
				platform.WriteError(w, "客户端用户名不合法（小写字母/数字开头，仅含字母数字 . _ -，最长 64）", 400)
			case errors.Is(err, xray.ErrRestart):
				log.Printf("ERROR: add xray client %s: %v", uuid, err)
				platform.WriteError(w, "客户端创建失败：Xray 无法加载新配置，已回滚", http.StatusBadGateway)
			case errors.Is(err, xray.ErrInvalidClient):
				platform.WriteError(w, "客户端 UUID 不合法", 400)
			default:
				log.Printf("ERROR: add xray client %s: %v", uuid, err)
				platform.WriteInternalError(w, err)
			}
			return
		}
		log.Printf("[security] xray client add uuid=%s name=%q owner=%q by=%s", uuid, created.Name, owner, actor.Username)
		platform.WriteJSON(w, map[string]string{"status": "ok", "uuid": uuid, "owner": owner, "name": created.Name})
	}
}

func ApiDeleteXray(svc *xray.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, _ := requestctx.ActorFrom(r.Context())

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
		body.UUID = strings.ToLower(body.UUID)

		isAdmin := actor.Role == identity.RoleAdmin
		ownerFilter := ""
		if !isAdmin {
			ownerFilter = actor.Username
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), platform.RestartTimeout)
		defer cancel()
		deleted, err := svc.Delete(ctx, body.UUID, ownerFilter)
		if err != nil {
			switch {
			case errors.Is(err, xray.ErrForbidden):
				platform.WriteError(w, "无权操作：不是你的客户端", 403)
			case errors.Is(err, xray.ErrNotFound):
				platform.WriteError(w, "客户端不存在", 404)
			case errors.Is(err, xray.ErrRestart):
				log.Printf("ERROR: delete xray client %s: %v", body.UUID, err)
				platform.WriteError(w, "客户端删除失败：Xray 无法加载新配置，已回滚", http.StatusBadGateway)
			default:
				log.Printf("ERROR: delete xray client %s: %v", body.UUID, err)
				platform.WriteInternalError(w, err)
			}
			return
		}
		log.Printf("[security] xray client delete uuid=%q name=%q owner=%q by=%s", body.UUID, deleted.Name, deleted.Owner, actor.Username)
		platform.WriteJSON(w, map[string]string{"status": "ok"})
	}
}

// GET /api/admin/users/xray/config/{uuid} — generate config for a specific client
func ApiXrayClientConfig(users *identity.Store, svc *xray.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, _ := requestctx.ActorFrom(r.Context())
		uuid := strings.ToLower(r.PathValue("uuid"))
		if uuid == "" {
			platform.WriteError(w, "UUID 必填", 400)
			return
		}
		clients, err := svc.Clients()
		if err != nil {
			log.Printf("Failed to read clients: %v", err)
			platform.WriteInternalError(w, err)
			return
		}
		var client *xray.Client
		for i := range clients {
			if clients[i].ID == uuid {
				client = &clients[i]
				break
			}
		}
		if client == nil {
			platform.WriteError(w, "客户端不存在", 404)
			return
		}
		if actor.Role != identity.RoleAdmin && client.Owner != actor.Username {
			platform.WriteError(w, "无权限", 403)
			return
		}

		// 只读端点：用 ProxyAuthForUserRead 拿已有密码，绝不懒创建。
		proxyAuth := users.ProxyAuthForUserRead(client.Owner)
		genJSON := xray.GenerateClientConfig([]xray.Client{*client}, xray.ClientConfigFromEnv(proxyAuth))
		platform.WriteJSON(w, map[string]any{
			"config": genJSON, "uuid": uuid, "owner": client.Owner, "name": client.Name,
		})
	}
}

var validUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
