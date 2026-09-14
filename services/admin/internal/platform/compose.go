package platform

import (
	"strings"

	"github.com/docker/docker/api/types/filters"
)

const defaultComposeProject = "workwork"

func ComposeProjectName() string {
	if project := strings.TrimSpace(GetEnv("COMPOSE_PROJECT_NAME")); project != "" {
		return project
	}
	return defaultComposeProject
}

func ComposeProjectFilters(extra ...filters.KeyValuePair) filters.Args {
	args := filters.NewArgs(filters.KeyValuePair{
		Key:   "label",
		Value: "com.docker.compose.project=" + ComposeProjectName(),
	})
	for _, pair := range extra {
		args.Add(pair.Key, pair.Value)
	}
	return args
}
