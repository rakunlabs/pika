package service

import (
	"fmt"
	"sync"

	"github.com/rytsh/mugo/fstore"
	_ "github.com/rytsh/mugo/fstore/registry"
	"github.com/rytsh/mugo/render"
)

var (
	configTemplateRendererMu sync.Mutex
	configTemplateRenderer   = render.NewRender(
		// render.NewRender defaults to trust=true. Configuration templates are
		// user-authored and may run on the data path, so explicitly turn trust off
		// and remove functions that can read/write the server filesystem, execute
		// commands, or expose process environment/network details.
		fstore.WithTrust(false),
		fstore.WithDisableFuncs("exec", "file", "os", "env", "expandenv", "getHostByName"),
	)
)

func renderConfigTemplate(data []byte, path, variant, format string, meta *FileMeta) ([]byte, error) {
	if meta == nil || !meta.GoTemplate {
		return data, nil
	}

	configTemplateRendererMu.Lock()
	defer configTemplateRendererMu.Unlock()
	out, err := configTemplateRenderer.ExecuteWithData(string(data), map[string]any{
		"Path":    path,
		"Variant": variant,
		"Format":  format,
	})
	if err != nil {
		return nil, fmt.Errorf("go template: %w", err)
	}
	return out, nil
}
