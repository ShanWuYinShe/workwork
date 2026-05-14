package main

import (
	"bufio"
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
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				k, v, ok := strings.Cut(line, "=")
				if !ok {
					continue
				}
				if (strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`)) || (strings.HasPrefix(v, `'`) && strings.HasSuffix(v, `'`)) {
				v = v[1 : len(v)-1]
			}
				envMap[k] = v
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

func getEnv(key string) string {
	loadEnv()
	return envMap[key]
}
