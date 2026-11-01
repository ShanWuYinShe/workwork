package transfer

import (
	"admin/internal/platform"
	"admin/internal/requestctx"
	"fmt"
	"log"
	"net/http"
	"os"
)

func ApiTransferList(s *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, _ := requestctx.ActorFrom(r.Context())

		s.mu.Lock()
		m, err := s.loadLocked()
		s.mu.Unlock()
		if err != nil {
			log.Printf("ERROR: transfer list: load manifest: %v", err)
			platform.WriteInternalError(w, err)
			return
		}

		// 权限过滤
		var files []transferFile
		if actor.Role == "admin" {
			files = m.Files
		} else {
			for _, f := range m.Files {
				if f.Uploader == actor.Username {
					files = append(files, f)
				}
			}
		}

		if files == nil {
			files = []transferFile{}
		}
		// 分页(可选):limit/offset 显式传入时校验并截取,默认全量。
		page, ok := platform.ParseListPage(w, r)
		if !ok {
			return
		}
		files = platform.Apply(files, page)

		platform.WriteJSON(w, map[string]any{
			"files":      files,
			"total_size": m.TotalSize,
			"max_total":  transferMaxTotal,
		})
	}
}

// DELETE /api/admin/transfer/files — viewer+ 删除文件
func ApiTransferDelete(s *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, _ := requestctx.ActorFrom(r.Context())

		// DELETE 不带 body(风格统一,claude/xray downloads 同为 query):目标经 ?code= 定位。
		code := r.URL.Query().Get("code")
		if code == "" {
			platform.WriteError(w, "分享码必填", 400)
			return
		}

		s.mu.Lock()
		defer s.mu.Unlock()

		m, err := s.loadLocked()
		if err != nil {
			log.Printf("ERROR: transfer delete: load manifest: %v", err)
			platform.WriteInternalError(w, err)
			return
		}
		found := false
		var kept []transferFile
		var removedNames []string
		for _, f := range m.Files {
			if f.Code == code {
				// 权限检查：admin 可删任意文件，普通用户只能删自己的
				if actor.Role != "admin" && f.Uploader != actor.Username {
					platform.WriteError(w, "无权操作：不是你上传的文件", 403)
					return
				}
				// Remove 失败(非 NotExist)时保留条目并向调用方如实报错:
				// 条目删除后文件成为孤儿(不可见、不可再删、不占配额),保留
				// 条目让用户可以重试删除。
				if err := os.Remove(s.filePath(f.Code, f.Filename)); err != nil && !os.IsNotExist(err) {
					log.Printf("ERROR: transfer delete remove %s: %v (entry kept)", f.Code, err)
					platform.WriteError(w, "文件删除失败，请稍后重试", 500)
					return
				}
				removedNames = append(removedNames, f.Filename)
				m.TotalSize -= f.Size
				found = true
			} else {
				kept = append(kept, f)
			}
		}

		if !found {
			platform.WriteError(w, "文件不存在", 404)
			return
		}

		m.Files = kept
		if m.TotalSize < 0 {
			m.TotalSize = 0
		}
		if err := s.saveLocked(m); err != nil {
			log.Printf("ERROR: save transfer manifest (delete): %v", err)
			platform.WriteInternalError(w, fmt.Errorf("persist transfer manifest: %w", err))
			return
		}
		// 审计带上命中条目的文件名(服务端可控值,无注入面):事后审计不必
		// 反查 manifest——那时条目已删。
		log.Printf("[security] transfer delete code=%q files=%q by=%s", code, removedNames, actor.Username)
		platform.WriteJSON(w, map[string]string{"status": "ok"})
	}
}
