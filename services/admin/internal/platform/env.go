package platform

import (
	"bufio"
	"log"
	"os"
	"strings"
	"sync"
)

var (
	envOnce sync.Once
	envMap  map[string]string
)

func loadEnv() {
	envOnce.Do(func() {
		envMap = make(map[string]string)
		// Read .env file first (lower priority)
		f, err := os.Open("/app/.env")
		if err == nil {
			defer f.Close()
			sc := bufio.NewScanner(f)
			// 默认单行上限 64KB:超限后 Scanner 停止推进且不报错,后续 .env
			// 行被静默丢弃——表现为"部分配置项神秘缺失"。放大上限并把错误
			// 记入日志,silent 截断不再可能。
			sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				k, v, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				envMap[k] = unquoteEnvValue(v)
			}
			if err := sc.Err(); err != nil {
				log.Printf("WARNING: read /app/.env: %v (.env treated as partial)", err)
			}
		}
		// OS environment overrides .env values
		for _, e := range os.Environ() {
			if k, v, ok := strings.Cut(e, "="); ok {
				envMap[k] = v
			}
		}
	})
}

// GetEnv 查询合并后的环境视图。双源读取是有意为之，并非冗余：loadEnv 的
// sync.Once 只在进程首次调用时快照 os.Environ()，之后 t.Setenv（测试）或
// 运行期对进程环境的修改只能经 os.LookupEnv 看到——直接查 envMap 会让
// 依赖 Setenv 的测试全部失效（曾实测破坏 media_catalog_test）。.env 文件
// 值仍在 envMap 中兜底。
func GetEnv(key string) string {
	loadEnv()
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return envMap[key]
}

// unquoteEnvValue strips a single pair of matching surrounding quotes from a
// .env value. It requires at least two characters so a lone quote (e.g.
// `KEY="`) is returned verbatim instead of triggering a v[1:0] slice panic.
func unquoteEnvValue(v string) string {
	if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
		return v[1 : len(v)-1]
	}
	return v
}
