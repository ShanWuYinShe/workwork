package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type ctxKey string

const userInfoCtxKey ctxKey = "userInfo"
const usernameCtxKey ctxKey = "username"

type UserInfo struct {
	Role          string `json:"role"`
	CreatedAt     string `json:"created_at"`
	CliproxyToken string `json:"cliproxy_token"`
	PoolAccess    bool   `json:"pool_access"`
	ProxyPassword string `json:"proxy_password"` // plaintext, for xhttp client config
}

type usersMetaDoc struct {
	Users map[string]UserInfo `json:"users"`
}

var (
	usersMetaPath = "/app/data/users.json"
	usersMetaMu   sync.Mutex
)

func readUsersMeta() (*usersMetaDoc, error) {
	usersMetaMu.Lock()
	defer usersMetaMu.Unlock()

	doc := &usersMetaDoc{Users: make(map[string]UserInfo)}
	data, err := os.ReadFile(usersMetaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return doc, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, doc); err != nil {
		return nil, err
	}
	if doc.Users == nil {
		doc.Users = make(map[string]UserInfo)
	}
	return doc, nil
}

func updateUsersMeta(fn func(doc *usersMetaDoc) (bool, error)) error {
	usersMetaMu.Lock()
	defer usersMetaMu.Unlock()

	doc := &usersMetaDoc{Users: make(map[string]UserInfo)}
	data, err := os.ReadFile(usersMetaPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	} else if err := json.Unmarshal(data, doc); err != nil {
		return err
	}
	if doc.Users == nil {
		doc.Users = make(map[string]UserInfo)
	}

	dirty, err := fn(doc)
	if err != nil || !dirty {
		return err
	}

	os.MkdirAll("/app/data", 0755)
	data, err = json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(usersMetaPath, data, 0600)
}

func ensureUserMeta(username string) (*UserInfo, error) {
	doc, err := readUsersMeta()
	if err != nil {
		return nil, err
	}
	if existing, exists := doc.Users[username]; exists {
		return &existing, nil
	}
	// User not in users.json — must be explicitly created by admin.
	// Do NOT auto-provision on first request: any request could forge X-Forwarded-User.
	return nil, nil
}

func isProtectedAdmin(username string) bool {
	return username == os.Getenv("AUTH_USER")
}

func updateUserMeta(username string, info UserInfo) error {
	return updateUsersMeta(func(doc *usersMetaDoc) (bool, error) {
		doc.Users[username] = info
		return true, nil
	})
}

func deleteUserMeta(username string) error {
	return updateUsersMeta(func(doc *usersMetaDoc) (bool, error) {
		delete(doc.Users, username)
		return true, nil
	})
}

func getUserFromContext(r *http.Request) (*UserInfo, string) {
	info, _ := r.Context().Value(userInfoCtxKey).(*UserInfo)
	username, _ := r.Context().Value(usernameCtxKey).(string)
	return info, username
}

func getOrCreateCliproxyToken(username string) (string, error) {
	var token string
	err := updateUsersMeta(func(doc *usersMetaDoc) (bool, error) {
		info := doc.Users[username]
		if info.CliproxyToken != "" {
			token = info.CliproxyToken
			return false, nil
		}
		b := make([]byte, 10)
		if _, err := rand.Read(b); err != nil {
			return false, err
		}
		info.CliproxyToken = hex.EncodeToString(b)
		if info.CreatedAt == "" {
			info.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		}
		doc.Users[username] = info
		token = info.CliproxyToken
		return true, nil
	})
	return token, err
}

func getOrCreateProxyPassword(username string) (string, error) {
	var password string
	created := false
	err := updateUsersMeta(func(doc *usersMetaDoc) (bool, error) {
		info := doc.Users[username]
		// Admin users don't manage xray clients
		if info.Role == "admin" {
			return false, nil
		}
		if info.ProxyPassword != "" {
			password = info.ProxyPassword
			return false, nil
		}
		b := make([]byte, 12)
		if _, err := rand.Read(b); err != nil {
			return false, err
		}
		info.ProxyPassword = hex.EncodeToString(b)
		if info.CreatedAt == "" {
			info.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		}
		doc.Users[username] = info
		password = info.ProxyPassword
		created = true
		return true, nil
	})
	if err != nil {
		return "", err
	}
	if created {
		genProxyHtpasswd()
	}
	return password, nil
}

// trustedProxyCIDRs returns the set of networks allowed to forge X-Forwarded-User.
// Configurable via TRUSTED_PROXY_CIDRS env (comma-separated CIDRs).
// Default: loopback + the docker bridge that fronts this service.
var trustedProxyCIDRs = sync.OnceValue(func() []*net.IPNet {
	defaults := "127.0.0.0/8,::1/128,172.16.0.0/12"
	raw := os.Getenv("TRUSTED_PROXY_CIDRS")
	if raw == "" {
		raw = defaults
	}
	var nets []*net.IPNet
	for _, c := range strings.Split(raw, ",") {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			log.Printf("WARNING: ignoring bad TRUSTED_PROXY_CIDR %q: %v", c, err)
			continue
		}
		nets = append(nets, n)
	}
	return nets
})

// isTrustedProxy reports whether the immediate caller of this service is in
// the trusted-proxy set. Without this guard, any container on the docker bridge
// could set X-Forwarded-User to impersonate any user.
func isTrustedProxy(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range trustedProxyCIDRs() {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func requireRole(minRole string) func(http.HandlerFunc) http.HandlerFunc {
	roleLevel := map[string]int{"admin": 2, "viewer": 1}
	internalToken := os.Getenv("INTERNAL_TOKEN")

	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var info *UserInfo
			var username string

			// Internal services (fail2ban) authenticate via shared secret.
			// This bypass is intentional and only requires the INTERNAL_TOKEN.
			if internalToken != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Internal-Token")), []byte(internalToken)) == 1 {
				info = &UserInfo{Role: "admin", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
				username = "internal"
			} else {
				// External requests must come through a trusted reverse proxy
				// (nginx on the docker bridge), and the proxy must have set
				// X-Forwarded-User after authenticating with basic auth.
				if !isTrustedProxy(r.RemoteAddr) {
					writeError(w, "Unauthorized", 401)
					return
				}
				username = r.Header.Get("X-Forwarded-User")
				if username == "" {
					writeError(w, "Unauthorized", 401)
					return
				}
				var err error
				info, err = ensureUserMeta(username)
				if err != nil {
					writeError(w, "Internal error", 500)
					return
				}
				if info == nil {
					// User authenticated by nginx but not provisioned in
					// users.json. Refuse to auto-create on first request —
					// admin must add them via /api/admin/users/admin/add.
					writeError(w, "User not provisioned", 403)
					return
				}
			}

			requiredLevel, ok := roleLevel[minRole]
			if !ok {
				writeError(w, "Internal error", 500)
				return
			}
			userLevel, ok := roleLevel[info.Role]
			if !ok {
				userLevel = 0
			}
			if userLevel < requiredLevel {
				writeError(w, "Forbidden", 403)
				return
			}

			ctx := context.WithValue(r.Context(), userInfoCtxKey, info)
			ctx = context.WithValue(ctx, usernameCtxKey, username)
			next(w, r.WithContext(ctx))
		}
	}
}
