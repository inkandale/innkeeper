package api

import (
	"strings"

	"github.com/inkandale/innkeeper/internal/export"
)

// exporter encodes cards with the server's plugins.
func (s *Server) exporter() *export.Exporter {
	return &export.Exporter{Lib: s.Lib, Registry: s.Registry, Plugins: s.Plugins, Log: s.Log}
}

// exportType is an export's Content-Type. Only JSON and images other than
// SVG keep their type, so an export can't run as a page on this site.
func exportType(t string) string {
	if t == "application/json" || strings.HasPrefix(t, "image/") && !strings.HasPrefix(t, "image/svg") {
		return t
	}
	return "application/octet-stream"
}
