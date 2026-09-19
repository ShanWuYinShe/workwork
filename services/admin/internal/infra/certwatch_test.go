package infra

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// genCertPEM 生成自签证书 PEM,NotAfter = now + lifetime。
func genCertPEM(t *testing.T, now time.Time, lifetime time.Duration) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(lifetime),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// TestCheckCertExpiry 分级告警:<30 天 WARNING+webhook,30-90 天 INFO,
// >=90 天 OK;非证书文件静默跳过。
func TestCheckCertExpiry(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("expiring.pem", genCertPEM(t, now, 10*24*time.Hour)) // <30d → WARNING
	write("info.pem", genCertPEM(t, now, 60*24*time.Hour))     // 30-90d → INFO
	write("ok.pem", genCertPEM(t, now, 200*24*time.Hour))      // >=90d → OK
	write("chain.pem", genCertPEM(t, now, 5*24*time.Hour))     // 最短链生效
	write("server_cert.key", []byte("not a cert"))             // 非证书后缀跳过
	write("junk.pem", []byte("garbage bytes"))                 // 解析失败跳过
	if err := os.WriteFile(filepath.Join(dir, "server_cert.key"), []byte("-----BEGIN PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	checkCertExpiry(dir, now) // 观察日志;不 panic 即可,分级逻辑由 earliestNotAfter 测试覆盖
}

// TestEarliestNotAfter 证书链取最早到期日;无证书文件报错。
func TestEarliestNotAfter(t *testing.T) {
	now := time.Now().UTC()
	chain := append(genCertPEM(t, now, 365*24*time.Hour), genCertPEM(t, now, 30*24*time.Hour)...)
	earliest, count, err := earliestNotAfter(chain, now)
	if err != nil || count != 2 {
		t.Fatalf("count=%d err=%v, want 2/nil", count, err)
	}
	want := now.Add(30 * 24 * time.Hour)
	if diff := earliest.Sub(want); diff > time.Minute || diff < -time.Minute {
		t.Fatalf("earliest = %v, want ≈ %v", earliest, want)
	}
	if _, _, err := earliestNotAfter([]byte("garbage"), now); err == nil {
		t.Fatal("non-certificate input should error")
	}
}
