package handlers

import (
	"embed"
	"html/template"
	"io/fs"
	"log"
	"net/http"
)

//go:embed templates
var templateFS embed.FS

var tmpl *template.Template

func init() {
	tmpl = template.Must(template.New("").Funcs(template.FuncMap{
		"stateClass":    stateClass,
		"countRunning":  countRunning,
	}).ParseFS(templateFS, "templates/*.html"))
}

func countRunning(services []Service) int {
	count := 0
	for _, s := range services {
		if s.State == "running" {
			count++
		}
	}
	return count
}

func renderTemplate(w http.ResponseWriter, name string, data map[string]any) {
	data["CSRF"] = "placeholder"
	if err := tmpl.ExecuteTemplate(w, name+".html", data); err != nil {
		log.Printf("Template error (%s): %v", name, err)
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

func stateClass(state string) string {
	switch state {
	case "running":
		return "badge-green"
	case "exited", "dead":
		return "badge-red"
	case "paused":
		return "badge-yellow"
	case "restarting":
		return "badge-yellow"
	default:
		return "badge-gray"
	}
}

// Ensure templateFS is used (embed requirement)
var _ fs.FS = templateFS
