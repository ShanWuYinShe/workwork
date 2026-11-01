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

// validateClients 是写盘前门禁：email 非空且大小写不敏感唯一。
// Xray vless validator 对 email 做 lower-case 后去重，重复会让整份配置
// 无法加载。
func validateClients(clients []Client) error {
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

// encodeClients 只写 Xray 认识的 id/email；owner 是管理面归属，不属于
// 运行时配置。flow 字段已彻底移除（XHTTP 不使用）。
func encodeClients(clients []Client) (string, error) {
	type encoded struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	arr := make([]encoded, 0, len(clients))
	for _, c := range clients {
		arr = append(arr, encoded{ID: c.ID, Email: c.Email})
	}
	b, err := json.Marshal(arr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
