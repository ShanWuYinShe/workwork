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
				envMap[k] = unquoteEnvValue(v)
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
