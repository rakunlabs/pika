package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/rytsh/mugo/fstore"
	_ "github.com/rytsh/mugo/fstore/registry"
	"github.com/rytsh/mugo/render"
	"gopkg.in/yaml.v3"
)

// proxyTemplateRenderer is a single mugo renderer shared by every
// proxy node that templates strings (http-request handler URL/headers/
// body, template-transform middleware). Mirrors the configuration-
// template renderer in internal/service/template.go: trust=false plus
// a deny-list of filesystem / process / network functions because the
// templates are operator-authored but execute on the data path.
//
// Locked-around because mugo's render is not goroutine-safe for the
// underlying template object. Hot paths render small strings so the
// contention is acceptable.
var (
	proxyTemplateMu       sync.Mutex
	proxyTemplateRenderer = render.NewRender(
		fstore.WithTrust(false),
		fstore.WithDisableFuncs("exec", "file", "os", "env", "expandenv", "getHostByName"),
	)
)

// renderTemplate renders the supplied template string against data.
// Returns the original string verbatim when tmpl contains no "{{"
// marker so we don't pay the renderer cost for the common static
// case (a URL with no placeholders).
func renderTemplate(tmpl string, data any) (string, error) {
	if tmpl == "" {
		return "", nil
	}
	if !bytes.Contains([]byte(tmpl), []byte("{{")) {
		return tmpl, nil
	}
	proxyTemplateMu.Lock()
	defer proxyTemplateMu.Unlock()
	out, err := proxyTemplateRenderer.ExecuteWithData(tmpl, data)
	if err != nil {
		return "", fmt.Errorf("template render: %w", err)
	}
	return string(out), nil
}

// decodePayload best-effort parses bytes into a Go value the
// template engine can iterate over. Mirrors chore's
// transfer.BytesToData behaviour: JSON first, YAML next, fall back
// to the raw string. nil input returns nil so the template sees no
// payload key.
func decodePayload(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	// JSON is checked first because it's strict (a JSON document is
	// also valid YAML, but the JSON decoder is faster and more
	// predictable for the common case).
	var jsonVal any
	if err := json.Unmarshal(b, &jsonVal); err == nil {
		return jsonVal
	}
	var yamlVal any
	if err := yaml.Unmarshal(b, &yamlVal); err == nil && yamlVal != nil {
		return yamlVal
	}
	return string(b)
}
