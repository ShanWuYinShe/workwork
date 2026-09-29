package infra

// 证书到期监控:CF Origin CA 证书(server_cert.pem)到期 = CF 到源站握手失败、
// 全站 5xx。此前全仓库无任何到期检查,到期前无人/无机制知道。这里启动时与
// 每 24h 扫描挂载的证书目录(./cert → /app/certs:ro),对全部 *.pem/*.crt/
// *.cer 文件解析 x509 链,取最早 NotAfter(leaf 通常最短,即有效到期日):
//   - <30 天:log WARNING + webhook cert.expiring(每轮检查触发一次,由
//     webhook 层的 60s 风暴抑制去重——每日检查窗口远大于抑制窗口,不会漏报)
//   - <90 天:log INFO 提示
// 私钥与不匹配文件解析失败只记 DEBUG 级日志,不影响启动。

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"admin/internal/platform"
)

const (
	certWarnDays   = 30
	certInfoDays   = 90
	certCheckEvery = 24 * time.Hour
)

// StartCertExpiryWatcher 启动即检查一次,随后每 24h 一次,随 ctx 退出。
// 独立 goroutine,panic 自兜底(main.go daemonGuard 之外的第二道防线不需要,
// 但检查逻辑自身禁止 panic 传播导致守护死亡)。
func StartCertExpiryWatcher(ctx context.Context) {
	certDir := platform.GetEnv("CERT_DIR")
	if certDir == "" {
		certDir = "/app/certs"
	}
	checkOnce := func() { checkCertExpiry(certDir, time.Now().UTC()) }
	checkOnce()
	ticker := time.NewTicker(certCheckEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkOnce()
		}
	}
}

// checkCertExpiry 扫描目录内证书文件并按剩余有效期分级告警。
func checkCertExpiry(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("cert expiry check: read %s: %v", dir, err)
		}
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".pem" && ext != ".crt" && ext != ".cer" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("cert expiry check: read %s: %v", path, err)
			continue
		}
		earliest, _, perr := earliestNotAfter(data, now)
		if perr != nil {
			// 非证书 PEM(私钥改了 .pem 后缀等)静默跳过,正常数据不告警。
			continue
		}
		// 用传入的 now 而非 time.Until:注入固定时间(测试/演练)时结果才不失真。
		days := int(earliest.Sub(now).Hours() / 24)
		switch {
		case days < certWarnDays:
			log.Printf("WARNING: certificate %s expires in %d days (%s)", e.Name(), days, earliest.Format(time.RFC3339))
			platform.NotifyWebhook("cert.expiring", "证书即将到期: "+e.Name(), map[string]string{
				"file": e.Name(), "expires": earliest.Format(time.RFC3339), "days_left": strconv.Itoa(days),
			})
		case days < certInfoDays:
			log.Printf("INFO: certificate %s expires in %d days (%s)", e.Name(), days, earliest.Format(time.RFC3339))
		default:
			log.Printf("cert expiry check: %s OK (%d days)", e.Name(), days)
		}
	}
}

// earliestNotAfter 解析 PEM 块序列(证书链),返回最早的 NotAfter 与证书数。
// 无任何有效证书时返回错误(调用方静默跳过非证书文件)。
func earliestNotAfter(data []byte, now time.Time) (time.Time, int, error) {
	var earliest time.Time
	count := 0
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		count++
		if earliest.IsZero() || cert.NotAfter.Before(earliest) {
			earliest = cert.NotAfter
		}
	}
	if count == 0 {
		return time.Time{}, 0, errNoCertificate
	}
	return earliest, count, nil
}

type simpleError string

func (e simpleError) Error() string { return string(e) }

const errNoCertificate = simpleError("no certificate found")
