package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
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

func updateUsersMeta(fn func(doc *usersMetaDoc) error) error {
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

	if err := fn(doc); err != nil {
		return err
	}

	os.MkdirAll("/app/data", 0755)
	data, err = json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(usersMetaPath, data, 0644)
}

func ensureUserMeta(username string) (*UserInfo, error) {
	var info *UserInfo
	err := updateUsersMeta(func(doc *usersMetaDoc) error {
		existing, exists := doc.Users[username]
		if !exists {
			existing = UserInfo{Role: "viewer", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
			doc.Users[username] = existing
		}
		info = &existing
		return nil
	})
	return info, err
}

func isProtectedAdmin(username string) bool {
	return username == os.Getenv("AUTH_USER")
}

func updateUserMeta(username string, info UserInfo) error {
	return updateUsersMeta(func(doc *usersMetaDoc) error {
		doc.Users[username] = info
		return nil
	})
}

func deleteUserMeta(username string) error {
	return updateUsersMeta(func(doc *usersMetaDoc) error {
		delete(doc.Users, username)
		return nil
	})
}

func getUserFromContext(r *http.Request) (*UserInfo, string) {
	info, _ := r.Context().Value(userInfoCtxKey).(*UserInfo)
	username, _ := r.Context().Value(usernameCtxKey).(string)
	return info, username
}

func getOrCreateCliproxyToken(username string) (string, error) {
	var token string
	err := updateUsersMeta(func(doc *usersMetaDoc) error {
		info := doc.Users[username]
		if info.CliproxyToken != "" {
			token = info.CliproxyToken
			return nil
		}
		b := make([]byte, 10)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		info.CliproxyToken = hex.EncodeToString(b)
		if info.CreatedAt == "" {
			info.CreatedAt = doc.Users[username].CreatedAt
		}
		doc.Users[username] = info
		token = info.CliproxyToken
		return nil
	})
	return token, err
}

func getOrCreateProxyPassword(username string) (string, error) {
	var password string
	err := updateUsersMeta(func(doc *usersMetaDoc) error {
		info := doc.Users[username]
		if info.ProxyPassword != "" {
			password = info.ProxyPassword
			return nil
		}
		b := make([]byte, 12)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		info.ProxyPassword = hex.EncodeToString(b)
		if info.CreatedAt == "" {
			info.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		}
		doc.Users[username] = info
		password = info.ProxyPassword
		return nil
	})
	if err != nil {
		return "", err
	}
	// Regenerate proxy htpasswd so nginx picks up the new user
	genProxyHtpasswd()
	return password, nil
}

func requireRole(minRole string) func(http.HandlerFunc) http.HandlerFunc {
	roleLevel := map[string]int{"admin": 2, "viewer": 1}
	internalToken := os.Getenv("INTERNAL_TOKEN")

	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var info *UserInfo
			var username string

			// Internal services (fail2ban) authenticate via shared secret
			if internalToken != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Internal-Token")), []byte(internalToken)) == 1 {
				info = &UserInfo{Role: "admin", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
				username = "internal"
			} else {
				// External requests: must come through nginx (X-Forwarded-User set by basic auth)
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
