package main

import (
	"strings"

	"github.com/docker/docker/api/types/filters"
)

const defaultComposeProject = "workwork"

func composeProjectName() string {
	if project := strings.TrimSpace(getEnv("COMPOSE_PROJECT_NAME")); project != "" {
		return project
	}
	return defaultComposeProject
}

func composeProjectFilters(extra ...filters.KeyValuePair) filters.Args {
	args := filters.NewArgs(filters.KeyValuePair{
		Key:   "label",
		Value: "com.docker.compose.project=" + composeProjectName(),
	})
	for _, pair := range extra {
		args.Add(pair.Key, pair.Value)
	}
	return args
}
