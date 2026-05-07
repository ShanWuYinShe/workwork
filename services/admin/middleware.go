package main

import (
	"context"
	"crypto/rand"
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

func saveUsersMeta(doc *usersMetaDoc) error {
	usersMetaMu.Lock()
	defer usersMetaMu.Unlock()

	os.MkdirAll("/app/data", 0755)
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(usersMetaPath, data, 0644)
}

func ensureUserMeta(username string) (*UserInfo, error) {
	doc, err := readUsersMeta()
	if err != nil {
		return nil, err
	}
	info, exists := doc.Users[username]
	if !exists {
		info = UserInfo{Role: "viewer", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
		doc.Users[username] = info
		if err := saveUsersMeta(doc); err != nil {
			return nil, err
		}
	}
	return &info, nil
}

func updateUserMeta(username string, info UserInfo) error {
	doc, err := readUsersMeta()
	if err != nil {
		return err
	}
	doc.Users[username] = info
	return saveUsersMeta(doc)
}

func deleteUserMeta(username string) error {
	doc, err := readUsersMeta()
	if err != nil {
		return err
	}
	delete(doc.Users, username)
	return saveUsersMeta(doc)
}

func getUserFromContext(r *http.Request) (*UserInfo, string) {
	info, _ := r.Context().Value(userInfoCtxKey).(*UserInfo)
	username, _ := r.Context().Value(usernameCtxKey).(string)
	return info, username
}

func getOrCreateCliproxyToken(username string) (string, error) {
	doc, err := readUsersMeta()
	if err != nil {
		return "", err
	}
	info := doc.Users[username]
	if info.CliproxyToken != "" {
		return info.CliproxyToken, nil
	}
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	info.CliproxyToken = hex.EncodeToString(b)
	info.Role = doc.Users[username].Role
	if info.CreatedAt == "" {
		info.CreatedAt = doc.Users[username].CreatedAt
	}
	doc.Users[username] = info
	if err := saveUsersMeta(doc); err != nil {
		return "", err
	}
	return info.CliproxyToken, nil
}

func requireRole(minRole string) func(http.HandlerFunc) http.HandlerFunc {
	roleLevel := map[string]int{"admin": 2, "viewer": 1}

	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			username := r.Header.Get("X-Forwarded-User")
			if username == "" {
				writeError(w, "Unauthorized", 401)
				return
			}

			info, err := ensureUserMeta(username)
			if err != nil {
				writeError(w, "Internal error", 500)
				return
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
