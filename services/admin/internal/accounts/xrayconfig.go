package accounts

import (
	"admin/internal/proxy"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"admin/internal/platform"
)

// genMu 串行化"读客户端快照→写配置文件"的完整生成过程。仅有
// xrayClientsMu 的锁内复制不够:两个并发生成者 A(旧快照)/B(新快照)的写盘
// 顺序不受锁保护,B 落盘后 A 仍可用过期客户端集覆盖,且调用方随后的
// RestartXray 会立即加载过期配置——刚拿到 add/delete 成功响应的用户实际
// 未生效,直到下次变更才自愈。
var genMu sync.Mutex

// GenXrayConfig generates xray.json from the template + persisted client data.
// Always regenerates — client data lives in /app/data/xray-clients.json.
// xrayClientsMu 只在读取快照时短暂持有,生成与写盘由 genMu 串行化,客户端
// 增删不会被慢速模板处理/写盘阻塞,同时保证后完成的生成者必然持有更新快照。
func GenXrayConfig() error {
	genMu.Lock()
	defer genMu.Unlock()

	tmpl, err := os.ReadFile(proxy.XrayConfigTemplatePath)
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

	xhttpJSON, err := encodeXrayClients(clientsCopy)
	if err != nil {
		return fmt.Errorf("encode xhttp clients: %w", err)
	}

	out := string(tmpl)
	out = strings.ReplaceAll(out, "__XRAY_CLIENTS_JSON__", xhttpJSON)

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
// 只保留 Xray 能识别的 id/email：管理面的 owner 归属信息不属于运行时配置，
// 不应写入（Xray 对未知字段静默忽略所以此前无害，但属于泄漏且未来收紧校验会炸）。
// flow 字段恒为空串——XHTTP 入站不用 flow（xtls-rprx-vision 是 Reality 专用，
// 已随 Reality 一并移除），保留键位仅为与历史配置 diff 稳定。
// 唯一的遗留读者 migrateXrayClientsFromConfig 在 Owner 缺失时已回退为 Owner=ID。
func encodeXrayClients(clients []xrayClientEntry) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, c := range clients {
		if err := enc.Encode(map[string]string{"id": c.ID, "email": c.Email, "flow": ""}); err != nil {
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
	// XHTTP_ADDRESS(可选)是客户端出站连接地址(优选入口)。只接受裸主机名或
	// IP:带 scheme/路径/空白的值会被原样写进生成配置的 vnext.address,把
	// 客户端打挂,且现象是"连不上"而非配置报错。
	if addr := strings.TrimSpace(platform.GetEnv("XHTTP_ADDRESS")); addr != "" {
		if strings.ContainsAny(addr, "/ 	") {
			return fmt.Errorf("XHTTP_ADDRESS must be a bare host or IP (no scheme/path/whitespace), got %q", addr)
		}
	}
	// XHTTP_ALPN 只接受 h2/h3:其他值生成的客户端配置无法与边缘协商,
	// 且要到客户端侧才暴露问题。
	switch strings.ToLower(strings.TrimSpace(platform.GetEnv("XHTTP_ALPN"))) {
	case "", "h2", "h3":
	default:
		return fmt.Errorf("XHTTP_ALPN must be h2 or h3, got %q", platform.GetEnv("XHTTP_ALPN"))
	}
	return nil
}
