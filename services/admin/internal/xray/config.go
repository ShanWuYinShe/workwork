package xray

import (
	"admin/internal/runtime"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Render 将台账渲染进 xray 模板并原子写入运行配置。
// 返回 changed 表示内容是否变化（调用方据此决定是否重启 xray）。
func Render(layout runtime.Layout, clients []Client) (bool, error) {
	tmpl, err := os.ReadFile(layout.XrayConfigTemplatePath())
	if err != nil {
		return false, err
	}
	if err := validateClients(clients); err != nil {
		return false, err
	}
	clientsJSON, err := encodeClients(clients)
	if err != nil {
		return false, fmt.Errorf("encode clients: %w", err)
	}
	out := strings.ReplaceAll(string(tmpl), "__XRAY_CLIENTS_JSON__", clientsJSON)
	if strings.Contains(out, "__XRAY_") {
		return false, fmt.Errorf("xray template still contains unreplaced __XRAY_ placeholders")
	}
	path := layout.XrayConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, err
	}
	existing, _ := os.ReadFile(path)
	if string(existing) == out {
		return false, runtime.EnsurePerm(path, 0640)
	}
	log.Printf("Regenerating xray config (%d clients)", len(clients))
	if err := runtime.AtomicWriteFile(path, []byte(out), 0640); err != nil {
		return false, err
	}
	return true, nil
}

// validateClients 是写盘前门禁：客户端用户名（Xray email）合法且大小写
// 不敏感唯一。Xray vless validator 对 email 做 lower-case 后去重，重复会让
// 整份配置无法加载。
func validateClients(clients []Client) error {
	seen := make(map[string]string, len(clients))
	for _, c := range clients {
		name := normalizeClientName(c.Name)
		if !ValidClientName(name) {
			return fmt.Errorf("xray client %s has invalid name/email %q", c.ID, c.Name)
		}
		if prevID, ok := seen[name]; ok {
			return fmt.Errorf("duplicate xray client name/email %q (clients %s and %s)", name, prevID, c.ID)
		}
		seen[name] = c.ID
	}
	return nil
}

// encodeClients 只写 Xray 认识的 id/email（email=客户端用户名）；owner 是
// 管理面归属，不属于运行时配置。flow 字段已彻底移除（XHTTP 不使用）。
func encodeClients(clients []Client) (string, error) {
	type encoded struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	arr := make([]encoded, 0, len(clients))
	for _, c := range clients {
		arr = append(arr, encoded{ID: c.ID, Email: normalizeClientName(c.Name)})
	}
	b, err := json.Marshal(arr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
