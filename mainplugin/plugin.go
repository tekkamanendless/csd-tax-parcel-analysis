package mainplugin

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"

	"github.com/go-app-blazar/blazar/blazarplugin"
	"github.com/maxence-charriere/go-app/v11/pkg/app"
)

// Config is the configuration for the plugin.
type Config struct {
	Location string
}

// plugin is the implementation of the plugin.
type plugin struct {
	config Config
}

var _ blazarplugin.Plugin = (*plugin)(nil)

// NewPlugin creates a new plugin.
func NewPlugin(config Config) blazarplugin.Plugin {
	return &plugin{
		config: config,
	}
}

// Register registers the plugin against the go-app handler and the HTTP mux.
func (p *plugin) Register(handler *app.Handler, mux *http.ServeMux) {
	location := p.config.Location
	if handler.Resources != nil {
		location = handler.Resources.Resolve(location)
	}
	slog.InfoContext(context.TODO(), "Registering plugin", "location", location)
	mux.Handle(location, http.StripPrefix(location, p.httpHandler()))

	handler.Styles = append(handler.Styles, p.cssFilenames(p.config.Location)...)
}

// cssFilenames returns the CSS filenames for the plugin.
func (p *plugin) cssFilenames(prefix string) []string {
	cssFiles := []string{
		"css/main.css",
	}
	slices.Sort(cssFiles)
	for i, filename := range cssFiles {
		cssFiles[i] = filepath.Join(prefix, filename)
	}
	return cssFiles
}

// httpHandler returns the HTTP handler for the plugin.
func (p *plugin) httpHandler() http.Handler {
	newFS, err := fs.Sub(embeddedFS, "embedded")
	if err != nil {
		return http.NotFoundHandler()
	}
	return blazarplugin.MimeTypeHandler(http.FileServerFS(newFS), blazarplugin.DefaultMimeTypeExtensions())
}
