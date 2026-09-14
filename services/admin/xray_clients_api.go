package main

import (
	"context"
	"github.com/docker/docker/client"
	"log"
	"net/http"
	"regexp"
	"strings"
)

func apiAddXray(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := getUserFromContext(r)

		var body struct {
			UUID string `json:"uuid"`
		}
		if err := decodeStrictJSON(r.Body, &body); err != nil {
			writeError(w, "Invalid JSON", 400)
			return
		}
		if body.UUID == "" {
			writeError(w, "UUID required", 400)
			return
		}
		if !validUUID.MatchString(body.UUID) {
			writeError(w, "Invalid UUID format (use lowercase hex, e.g. 550e8400-e29b-41d4-a716-446655440000)", 400)
			return
		}
		// UUID 规范化:大写/小写视为同一客户端(Xray 的 UUID 不区分大小写),
		// 避免 "550e8400-…" 与 "550E8400-…" 作为两个条目写入配置导致 Xray
		// 加载重复 user 失败。
		body.UUID = strings.ToLower(body.UUID)
		// internal 伪用户(X-Internal-Token 调用)不建 proxy 密码:否则
		// users.json 出现 role 为空的 "internal" 条目,并在 .htpasswd-proxy
		// 留下有效凭证行(与 proxyAuthForUser 的排除逻辑对齐)。
		if username != "internal" {
			if _, err := getOrCreateProxyPassword(username); err != nil {
				log.Printf("Failed to create proxy password for %s: %v", username, err)
				writeInternalError(w, err)
				return
			}
		}

		// requireRole 保证 userInfo 非 nil(死分支不复存在)
		isAdmin := userInfo.Role == "admin"
		entry := xrayClientEntry{ID: body.UUID, Owner: username, Email: username}
		var err error
		if isAdmin {
			err = addXrayClient(entry)
		} else {
			var added bool
			added, err = addXrayClientIfOwnerUnique(entry)
			if !added && err == nil {
				writeError(w, "You can only create one client. Delete the existing one first.", 400)
				return
			}
		}
		if err != nil {
			if err == errDuplicateUUID {
				writeError(w, "Client with this UUID already exists", 409)
				return
			}
			log.Printf("Failed to save client: %v", err)
			writeInternalError(w, err)
			return
		}

		// genXrayConfig() reads clients under xrayClientsMu and releases it before
		// generating the config, so add/delete operations are not blocked.
		if err := genXrayConfig(); err != nil {
			log.Printf("Failed to regenerate xray config: %v", err)
			writeInternalError(w, err)
			return
		}
		// 重启不随客户端断开而取消：与 apiServiceAction 一致，避免 add 请求断连后
		// 只完成配置落盘、Xray 停在未加载状态。
		restartCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), restartTimeout)
		if err := restartXray(restartCtx, cli); err != nil {
			cancel()
			// 配置已落盘但 xray 未加载:必须明确报错,否则用户看到"成功"而代理
			// 实际不可用;配置保留在磁盘,用户重试会撞 409 无法自愈,因此文案
			// 明确说明"已保存但未生效"。
			log.Printf("ERROR: failed to restart xray after add: %v", err)
			writeError(w, "客户端已保存，但 Xray 重启失败，代理配置可能未生效", http.StatusBadGateway)
			return
		}
		cancel()
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

func apiDeleteXray(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := getUserFromContext(r)

		var body struct {
			UUID string `json:"uuid"`
		}
		if err := decodeStrictJSON(r.Body, &body); err != nil {
			writeError(w, "Invalid JSON", 400)
			return
		}
		if body.UUID == "" {
			writeError(w, "UUID required", 400)
			return
		}
		// 与 apiAddXray 的规范化一致:UUID 不区分大小写。
		body.UUID = strings.ToLower(body.UUID)

		// deleteXrayClientIfOwner 原子地检查所有权并删除，避免 TOCTOU 竞态
		// requireRole 保证 userInfo 非 nil(死分支不复存在)
		isAdmin := userInfo.Role == "admin"
		ownerFilter := ""
		if !isAdmin {
			ownerFilter = username
		}
		deleted, err := deleteXrayClientIfOwner(body.UUID, ownerFilter)
		if err != nil {
			if err == errForbidden {
				writeError(w, "Forbidden: not your client", 403)
				return
			}
			if err == errNotFound {
				writeError(w, "Client not found", 404)
				return
			}
			log.Printf("Failed to delete client: %v", err)
			writeInternalError(w, err)
			return
		}
		if !deleted {
			writeError(w, "Client not found", 404)
			return
		}

		// genXrayConfig() reads clients under xrayClientsMu and releases it before
		// generating the config, so add/delete operations are not blocked.
		if err := genXrayConfig(); err != nil {
			log.Printf("Failed to regenerate xray config: %v", err)
			writeInternalError(w, err)
			return
		}
		// 重启不随客户端断开而取消（与 apiAddXray 一致）。
		restartCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), restartTimeout)
		if err := restartXray(restartCtx, cli); err != nil {
			cancel()
			log.Printf("ERROR: failed to restart xray after delete: %v", err)
			writeError(w, "客户端已删除，但 Xray 重启失败，代理配置可能未生效", http.StatusBadGateway)
			return
		}
		cancel()
		writeJSON(w, map[string]string{"status": "ok"})
	}
}

// GET /api/admin/users/xray/config/{uuid} — generate config for a specific client
func apiXrayClientConfig() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userInfo, username := getUserFromContext(r)
		uuid := strings.ToLower(r.PathValue("uuid"))
		if uuid == "" {
			writeError(w, "UUID required", 400)
			return
		}

		allClients, err := readXrayClientMaps()
		if err != nil {
			log.Printf("Failed to read clients: %v", err)
			writeInternalError(w, err)
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
			writeError(w, "Client not found", 404)
			return
		}

		// Regular users can only view their own client
		if userInfo != nil && userInfo.Role != "admin" && client["owner"] != username {
			writeError(w, "Forbidden", 403)
			return
		}

		// 只读端点:用 proxyAuthForUserRead 拿已有密码,绝不在这里懒创建
		// (生成密码 + 重写 htpasswd 是写副作用,并发 GET 会写放大,且写失败
		// 会让只读请求 500)。用户无 proxy 密码时配置里留空,与 apiUsers 一致。
		proxyAuth := proxyAuthForUserRead(client["owner"])
		genJSON := GenerateClientConfig([]map[string]string{client}, ClientConfig{
			RealityDomain:     getEnv("XRAY_REALITY_DOMAIN"),
			RealityServerName: getEnv("XRAY_REALITY_SERVER_NAME"),
			RealityPublicKey:  getEnv("XRAY_PUBLIC_KEY"),
			RealityShortID:    xrayClientShortID(),
			XHTTPDomain:       getEnv("XHTTP_DOMAIN"),
			XHTTPProxyAuth:    proxyAuth,
		})
		writeJSON(w, map[string]any{
			"config": genJSON, "uuid": uuid, "owner": client["owner"],
		})
	}
}

var validUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
