package server

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/handler/folder"
)

//go:embed dist/*
var uiFS embed.FS

func folderHandler(m *ada.Mux) error {
	f, err := folder.New(&folder.Config{
		Browse:         false,
		SPA:            true,
		Index:          true,
		StripIndexName: true,
		PrefixPath:     m.Prefix(),
		CacheRegex: []*folder.RegexCacheStore{
			{
				Regex:        `index\.html$`,
				CacheControl: "no-cache",
			},
		},
	})
	if err != nil {
		return err
	}

	uiDist, err := fs.Sub(uiFS, "dist")
	if err != nil {
		return err
	}

	f.SetFs(http.FS(uiDist))

	m.Handle("/*", f)

	return nil
}
