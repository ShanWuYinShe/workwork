package identity

import (
	"admin/internal/platform"
	"admin/internal/runtime"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// htpasswdGID 是 nginx worker 的运行组（compose nginx group_add/uid 101）。
// 文件 0640 owner=1001 group=101：nginx 可读，宿主其他用户不可读。
const htpasswdGID = 101

func bcryptHtpasswdLine(user, pass string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return user + ":" + string(hash), nil
}

func verifyHtpasswdLine(user, pass, line string) bool {
	prefix := user + ":"
	if !strings.HasPrefix(line, prefix) {
		return false
	}
	hash := strings.TrimPrefix(line, prefix)
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass)) == nil
}

// writeHtpasswdFile 原子写入 0640 并 chgrp 101。chgrp 失败不降级 0644：
// 0644 会让宿主任意用户拿到 bcrypt 哈希做离线爆破；保持 0640 并告警，
// 即使 nginx 读不到也表现为响亮的 401，而不是凭证静默外泄。
func writeHtpasswdFile(path string, content []byte) error {
	if err := runtime.AtomicWriteFile(path, content, 0640); err != nil {
		return err
	}
	if err := os.Chown(path, -1, htpasswdGID); err != nil {
		log.Printf("ERROR: chgrp %d %s failed (%v); keeping 0640 — add group_add [\"%d\"] to the admin service, otherwise nginx cannot read credentials", htpasswdGID, path, err, htpasswdGID)
		platform.NotifyWebhook("auth.permission", "htpasswd chgrp 失败，凭证保持 0640（nginx 可能无法读取）", map[string]string{
			"path": path, "gid": fmt.Sprint(htpasswdGID), "error": err.Error(),
		})
	}
	return nil
}

// ensureHtpasswdPerm 对已存在的 htpasswd 做幂等权限收敛（启动自愈）。
func ensureHtpasswdPerm(path string) {
	if err := os.Chown(path, -1, htpasswdGID); err != nil {
		log.Printf("ERROR: chgrp %d %s failed (%v); nginx may not be able to read credentials — add group_add [\"%d\"] to the admin service", htpasswdGID, path, err, htpasswdGID)
		return
	}
	if err := runtime.EnsurePerm(path, 0640); err != nil {
		log.Printf("WARNING: tighten perm %s: %v", path, err)
	}
}

func verifyHtpasswdFile(path, username, password string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	prefix := username + ":"
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) && verifyHtpasswdLine(username, password, line) {
			return true
		}
	}
	return false
}

func addHtpasswdLocked(path, username, password string) error {
	newLine, err := bcryptHtpasswdLine(username, password)
	if err != nil {
		return err
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	var lines []string
	replaced := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, username+":") {
			lines = append(lines, newLine)
			replaced = true
		} else {
			lines = append(lines, line)
		}
	}
	if !replaced {
		lines = append(lines, newLine)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return writeHtpasswdFile(path, []byte(strings.Join(lines, "\n")+"\n"))
}

func removeHtpasswdLocked(path, username string) error {
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return nil
		}
		return readErr
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, username+":") {
			lines = append(lines, line)
		}
	}
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return writeHtpasswdFile(path, []byte(content))
}

func readHtpasswdUsers(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var users []string
	for _, line := range strings.Split(string(data), "\n") {
		if idx := strings.Index(line, ":"); idx > 0 {
			users = append(users, line[:idx])
		}
	}
	return users, nil
}

// htpasswdHasValidEntry 判断内容里是否存在至少一行 "user:hash"（只挡截断/
// 清空，不校验算法细节——htpasswd 支持多种 hash 格式）。
func htpasswdHasValidEntry(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.Index(line, ":"); idx > 0 && idx < len(line)-1 {
			return true
		}
	}
	return false
}
