package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

type Service struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Status string `json:"status"`
	Image  string `json:"image"`
	Uptime string `json:"uptime,omitempty"`
}

func listServices(ctx context.Context, cli *client.Client) ([]Service, error) {
	containers, err := cli.ContainerList(ctx, types.ContainerListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.KeyValuePair{
				Key:   "label",
				Value: "com.docker.compose.project",
			},
		),
	})
	if err != nil {
		return nil, err
	}

	project := ""
	services := make([]Service, 0, len(containers))
	for _, c := range containers {
		if project == "" {
			project = c.Labels["com.docker.compose.project"]
		}
		if c.Labels["com.docker.compose.project"] != project {
			continue
		}

		name := strings.TrimPrefix(c.Names[0], "/")
		svcLabel := c.Labels["com.docker.compose.service"]
		if svcLabel != "" {
			name = svcLabel
		}

		svc := Service{
			Name:   name,
			State:  c.State,
			Status: c.Status,
			Image:  c.Image,
		}
		services = append(services, svc)
	}

	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})
	return services, nil
}

func Dashboard(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		services, err := listServices(r.Context(), cli)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		data := map[string]any{
			"Page":     "dashboard",
			"Services": services,
			"Now":      time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		renderTemplate(w, "dashboard", data)
	}
}

func ServicesList(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		services, err := listServices(r.Context(), cli)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		data := map[string]any{
			"Page":     "services",
			"Services": services,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		renderTemplate(w, "services", data)
	}
}

func ServiceStart(cli *client.Client) http.HandlerFunc {
	return serviceAction(cli, func(ctx context.Context, cli *client.Client, id string) error {
		return cli.ContainerStart(ctx, id, types.ContainerStartOptions{})
	}, "start")
}

func ServiceStop(cli *client.Client) http.HandlerFunc {
	return serviceAction(cli, func(ctx context.Context, cli *client.Client, id string) error {
		return cli.ContainerStop(ctx, id, container.StopOptions{})
	}, "stop")
}

func ServiceRestart(cli *client.Client) http.HandlerFunc {
	return serviceAction(cli, func(ctx context.Context, cli *client.Client, id string) error {
		return cli.ContainerRestart(ctx, id, container.StopOptions{})
	}, "restart")
}

func serviceAction(cli *client.Client, fn func(context.Context, *client.Client, string) error, action string) http.HandlerFunc {
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

		if err := fn(r.Context(), cli, containers[0].ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if r.Header.Get("HX-Request") == "true" {
			services, _ := listServices(r.Context(), cli)
			for _, s := range services {
				if s.Name == name {
					data := map[string]any{"Service": s}
					renderTemplate(w, "service_row", data)
					return
				}
			}
		}

		http.Redirect(w, r, "/admin/services", http.StatusSeeOther)
	}
}

func ServicesAPI(cli *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		services, err := listServices(r.Context(), cli)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(services)
	}
}
