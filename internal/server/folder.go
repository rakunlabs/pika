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
		// First-match-wins: order matters. The most specific patterns
		// must come before the catch-all SPA-shell rule. Regex is matched
		// against the file's basename (fi.Name()), not the full URL path.
		CacheRegex: []*folder.RegexCacheStore{
			// Vite content-hashed bundles (e.g. index-D2Akt2N_.css,
			// index-BrnSF0WY.js). The hash changes whenever the content
			// changes, so the URL changes too — safe to cache forever.
			// The "immutable" hint lets browsers skip revalidation round-
			// trips entirely on subsequent visits.
			{
				Regex:        `^index-[A-Za-z0-9_-]+\.(js|css)$`,
				CacheControl: "public, max-age=31536000, immutable",
			},
			// Favicons and other root-level static images/fonts. Filenames
			// are stable across builds and the assets rarely change, so
			// one week of caching is a reasonable trade-off; a hard refresh
			// recovers from any change in the meantime.
			{
				Regex:        `\.(ico|png|svg|webp|woff2?)$`,
				CacheControl: "public, max-age=604800",
			},
			// SPA shell — must always be revalidated so a new deploy's
			// hashed asset URLs are picked up immediately. Kept last
			// because first-match-wins; the more specific rules above
			// take priority for asset/image filenames.
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
