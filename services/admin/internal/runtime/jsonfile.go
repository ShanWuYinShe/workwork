package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// ErrCorrupt 表示 JSON 文件解析失败且已隔离为 .corrupt。
var ErrCorrupt = errors.New("corrupt json state file")

// Notifier 是状态损坏告警的接缝；生产用 platform.NotifyWebhook 适配，
// 测试可传 nil 或记录器。
type Notifier interface {
	Notify(event, summary string, details map[string]string)
}

// NotifyFunc 让普通函数满足 Notifier。
type NotifyFunc func(event, summary string, details map[string]string)

func (f NotifyFunc) Notify(event, summary string, details map[string]string) {
	f(event, summary, details)
}

// JSONFile[T] 是一个无锁的 JSON 文件读写器。锁由持有该状态文件的领域
// store 自己管理（users.json 等跨文件事务需要领域级锁，不能由文件层
// 再嵌套一把锁）。
type JSONFile[T any] struct {
	path     string
	perm     os.FileMode
	notifier Notifier
}

func NewJSONFile[T any](path string, perm os.FileMode, notifier Notifier) *JSONFile[T] {
	return &JSONFile[T]{path: path, perm: perm, notifier: notifier}
}

func (f *JSONFile[T]) Path() string { return f.path }

// Load 读取文件：不存在返回零值 + nil；读失败返回 error；解析失败隔离
// 为 <path>.corrupt、外发告警并返回 ErrCorrupt 包装错误。
func (f *JSONFile[T]) Load() (T, error) {
	var v T
	data, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return v, nil
		}
		return v, fmt.Errorf("read %s: %w", f.path, err)
	}
	if err := json.Unmarshal(data, &v); err != nil {
		q := f.path + ".corrupt"
		if qerr := os.Rename(f.path, q); qerr != nil {
			log.Printf("ERROR: quarantine %s: %v", q, qerr)
		}
		if f.notifier != nil {
			f.notifier.Notify("data.corrupt", "状态文件损坏已隔离: "+f.path, map[string]string{
				"path": f.path, "quarantined_as": q,
			})
		}
		return v, fmt.Errorf("%w: %s: %v", ErrCorrupt, f.path, err)
	}
	return v, nil
}

// Save 原子写入（自动创建父目录）。
func (f *JSONFile[T]) Save(v T) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", f.path, err)
	}
	if err := AtomicWriteFile(f.path, data, f.perm); err != nil {
		return fmt.Errorf("write %s: %w", f.path, err)
	}
	return nil
}

// Clock 是可注入时间的接缝（限流、封禁过期等）。
type Clock interface {
	Now() time.Time
}

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }
