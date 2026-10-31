package accounts

import (
	"admin/internal/proxy"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
// 返回 changed 表示运行配置内容是否真的变了——调用方据此决定要不要重启
// xray（配置相同不重启，避免无意义的代理闪断）。
// xrayClientsMu 只在读取快照时短暂持有,生成与写盘由 genMu 串行化,客户端
// 增删不会被慢速模板处理/写盘阻塞,同时保证后完成的生成者必然持有更新快照。
func GenXrayConfig() (changed bool, err error) {
	genMu.Lock()
	defer genMu.Unlock()

	tmpl, err := os.ReadFile(proxy.XrayConfigTemplatePath)
	if err != nil {
		return false, err
	}

	xrayClientsMu.Lock()
	clients, err := readXrayClientsNoLock()
	if err != nil {
		xrayClientsMu.Unlock()
		return false, fmt.Errorf("read xray clients: %w", err)
	}
	// 复制切片以在锁外安全使用
	clientsCopy := make([]xrayClientEntry, len(clients))
	copy(clientsCopy, clients)
	xrayClientsMu.Unlock()

	// 门禁：email 必须非空且（大小写不敏感）唯一。Xray 的 VLESS validator
	// 会拒绝重复 email 并导致整份配置无法加载；必须在写盘前拦住，避免把
	// 坏配置落盘后 xray 起不来、而台账已经写入的死局。
	if err := validateXrayClients(clientsCopy); err != nil {
		return false, err
	}

	xhttpJSON, err := encodeXrayClients(clientsCopy)
	if err != nil {
		return false, fmt.Errorf("encode xhttp clients: %w", err)
	}

	out := string(tmpl)
	out = strings.ReplaceAll(out, "__XRAY_CLIENTS_JSON__", xhttpJSON)

	// The file is shared with xray container via bind mount.
	if err := os.MkdirAll(filepath.Dir(proxy.XrayConfigPath), 0755); err != nil {
		return false, err
	}
	existing, _ := os.ReadFile(proxy.XrayConfigPath)
	if string(existing) == out {
		return false, platform.EnsurePerm(proxy.XrayConfigPath, 0640)
	}
	log.Printf("Regenerating xray config (%d clients, dns=%v)", len(clientsCopy), strings.Contains(out, `"dns"`))
	if err := platform.AtomicWriteFile(proxy.XrayConfigPath, []byte(out), 0640); err != nil {
		return false, err
	}
	return true, nil
}

// validateXrayClients 是 Xray 配置的加载前门禁：email 非空且大小写不敏感唯一。
// 与 Xray vless validator 的语义一致（它对 email 做 lower-case 后去重）。
func validateXrayClients(clients []xrayClientEntry) error {
	seen := make(map[string]string, len(clients))
	for _, c := range clients {
		email := strings.TrimSpace(c.Email)
		if email == "" {
			return fmt.Errorf("xray client %s has empty email", c.ID)
		}
		key := strings.ToLower(email)
		if prevID, ok := seen[key]; ok {
			return fmt.Errorf("duplicate xray client email %q (clients %s and %s)", email, prevID, c.ID)
		}
		seen[key] = c.ID
	}
	return nil
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
