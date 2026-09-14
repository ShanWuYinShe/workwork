package accounts

import (
	"admin/internal/proxy"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"admin/internal/platform"
)

// GenXrayConfig generates xray.json from the template + persisted client data.
// Always regenerates — client data lives in /app/data/xray-clients.json.
// Reads clients under xrayClientsMu, copies, then releases the lock before
// generating the config. This avoids blocking concurrent client add/delete
// operations during the (potentially slow) template processing + file write.
// The brief inconsistency window is benign: the next add/delete call triggers
// another GenXrayConfig that will include the latest client set.
func GenXrayConfig() error {
	tmpl, err := os.ReadFile(proxy.XrayConfigTemplatePath)
	if err != nil {
		return err
	}
	shortIDsJSON, err := xrayShortIDsJSON()
	if err != nil {
		return err
	}

	xrayClientsMu.Lock()
	clients, err := readXrayClientsNoLock()
	if err != nil {
		xrayClientsMu.Unlock()
		return fmt.Errorf("read xray clients: %w", err)
	}
	// 复制切片以在锁外安全使用
	clientsCopy := make([]xrayClientEntry, len(clients))
	copy(clientsCopy, clients)
	xrayClientsMu.Unlock()

	xhttpJSON, err := encodeXrayClients(clientsCopy, "")
	if err != nil {
		return fmt.Errorf("encode xhttp clients: %w", err)
	}
	realityJSON, err := encodeXrayClients(clientsCopy, "xtls-rprx-vision")
	if err != nil {
		return fmt.Errorf("encode reality clients: %w", err)
	}

	out := string(tmpl)
	out = strings.ReplaceAll(out, "__XRAY_CLIENTS_JSON__", xhttpJSON)
	out = strings.ReplaceAll(out, "__XRAY_REALITY_CLIENTS_JSON__", realityJSON)
	out = strings.ReplaceAll(out, "__XRAY_SHORT_IDS_JSON__", shortIDsJSON)
	// 三个占位符都在 JSON 字符串字面量内（config.json.tmpl dest/serverNames/
	// privateKey），值先经 json.Marshal 再去外层引号替换——裸替换在值含
	// `"` 或 `\` 时产生非法 JSON / 提前闭合注入任意键。与客户端字段的
	// json.Encoder 路径对齐同一转义标准。
	replaceJSONEnv := func(placeholder, key, def string) {
		v := strings.TrimSpace(platform.GetEnv(key))
		if v == "" {
			v = def
		}
		quoted, err := json.Marshal(v)
		if err != nil {
			quoted = []byte(`"` + def + `"`)
		}
		out = strings.ReplaceAll(out, placeholder, string(quoted[1:len(quoted)-1]))
	}
	replaceJSONEnv("__XRAY_REALITY_DEST__", "XRAY_REALITY_DEST", "addons.mozilla.org")
	replaceJSONEnv("__XRAY_REALITY_SERVER_NAME__", "XRAY_REALITY_SERVER_NAME", "")
	replaceJSONEnv("__XRAY_PRIVATE_KEY__", "XRAY_PRIVATE_KEY", "")

	// Always write — content comparison happens inside WriteFile (atomic overwrite).
	// The file is shared with xray container via bind mount.
	if err := os.MkdirAll("/app/data/generated/xray", 0755); err != nil {
		return err
	}
	existing, _ := os.ReadFile(proxy.XrayConfigPath)
	if string(existing) == out {
		return platform.EnsurePerm(proxy.XrayConfigPath, 0640)
	}
	log.Printf("Regenerating xray config (%d clients, dns=%v)", len(clientsCopy), strings.Contains(out, `"dns"`))
	return platform.AtomicWriteFile(proxy.XrayConfigPath, []byte(out), 0640)
}

// encodeXrayClients 将持久化的 client 条目编码为 Xray inbound `clients` JSON 数组。
// 只保留 Xray 能识别的 id/email/flow：管理面的 owner 归属信息不属于运行时配置，
// 不应写入（Xray 对未知字段静默忽略所以此前无害，但属于泄漏且未来收紧校验会炸）。
// 唯一的遗留读者 migrateXrayClientsFromConfig 在 Owner 缺失时已回退为 Owner=ID。
func encodeXrayClients(clients []xrayClientEntry, flow string) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, c := range clients {
		if err := enc.Encode(map[string]string{"id": c.ID, "email": c.Email, "flow": flow}); err != nil {
			return "", fmt.Errorf("encode client %s: %w", c.ID, err)
		}
		// enc.Encode 追加换行；去掉尾换行再加逗号，拼成 JSON 数组元素。
		// 编码失败时 buffer 不会增长，Truncate(Len()-1) 在空 buffer 上会 panic，
		// 因此只在 Encode 成功后才 Truncate。
		buf.Truncate(buf.Len() - 1)
		buf.WriteByte(',')
	}
	if buf.Len() == 0 {
		return "[]", nil
	}
	return "[" + strings.TrimSuffix(buf.String(), ",") + "]", nil
}

func ValidateProxyConfigEnv() error {
	required := []string{
		"XRAY_REALITY_DOMAIN",
		"XRAY_REALITY_SERVER_NAME",
		"XRAY_PRIVATE_KEY",
		"XRAY_PUBLIC_KEY",
		"XRAY_SHORT_IDS",
		"XHTTP_DOMAIN",
	}
	var missing []string
	for _, key := range required {
		if strings.TrimSpace(platform.GetEnv(key)) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	if _, err := xrayShortIDs(); err != nil {
		return err
	}
	return nil
}

func xrayShortIDsJSON() (string, error) {
	ids, err := xrayShortIDs()
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(ids)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func XrayClientShortID() string {
	ids, err := xrayShortIDs()
	if err != nil || len(ids) == 0 {
		return strings.TrimSpace(platform.GetEnv("XRAY_SHORT_IDS"))
	}
	return ids[0]
}

func xrayShortIDs() ([]string, error) {
	raw := strings.TrimSpace(platform.GetEnv("XRAY_SHORT_IDS"))
	if raw == "" {
		return nil, fmt.Errorf("XRAY_SHORT_IDS is required")
	}
	var ids []string
	for _, part := range strings.Split(raw, ",") {
		id := strings.TrimSpace(part)
		if id == "" {
			continue
		}
		if len(id)%2 != 0 || len(id) > 16 {
			return nil, fmt.Errorf("invalid XRAY_SHORT_IDS value %q: use even-length hex strings up to 16 characters", id)
		}
		if _, err := hex.DecodeString(id); err != nil {
			return nil, fmt.Errorf("invalid XRAY_SHORT_IDS value %q: %w", id, err)
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("XRAY_SHORT_IDS is required")
	}
	return ids, nil
}
