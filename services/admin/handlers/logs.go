package handlers

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

func ServiceLogsPage(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		data := map[string]any{
			"Page":        "logs",
			"ServiceName": name,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		renderTemplate(w, "logs", data)
	}
}

func ServiceLogs(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		containers, err := cli.ContainerList(r.Context(), types.ContainerListOptions{
			All: true,
			Filters: filters.NewArgs(
				filters.KeyValuePair{
					Key:   "label",
					Value: fmt.Sprintf("com.docker.compose.service=%s", name),
				},
			),
		})
		if err != nil || len(containers) == 0 {
			http.Error(w, "Service not found", http.StatusNotFound)
			return
		}

		tail := r.URL.Query().Get("tail")
		if tail == "" {
			tail = "100"
		}

		follow := r.URL.Query().Get("follow") == "true"

		out, err := cli.ContainerLogs(r.Context(), containers[0].ID, types.ContainerLogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Follow:     follow,
			Tail:       tail,
			Timestamps: true,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer out.Close()

		if !follow {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			streamLogs(out, w)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			line := stripHeader(scanner.Bytes())
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		}
	}
}

func streamLogs(src io.Reader, w io.Writer) {
	buf := make([]byte, 8192)
	first := true
	for {
		n, err := src.Read(buf)
		if n > 0 {
			start := 0
			if first {
				if n > 8 {
					start = 8
				}
				first = false
			}
			data := buf[start:n]
			clean := stripANSI(string(data))
			w.Write([]byte(clean))
		}
		if err != nil {
			break
		}
	}
}

func stripHeader(b []byte) string {
	if len(b) > 8 {
		b = b[8:]
	}
	return stripANSI(string(b))
}

func stripANSI(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
}
