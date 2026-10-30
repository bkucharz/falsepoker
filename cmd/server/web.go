package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

func withWebAssets(api http.Handler, directory string) (http.Handler, error) {
	if directory == "" {
		return api, nil
	}
	assets := os.DirFS(directory)
	if _, err := fs.Stat(assets, "index.html"); err != nil {
		return nil, fmt.Errorf("web assets at %q: %w", directory, err)
	}
	files := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			api.ServeHTTP(w, r)
			return
		}

		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "." || name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(assets, name); err == nil {
			files.ServeHTTP(w, r)
			return
		} else if !errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "unable to read web asset", http.StatusInternalServerError)
			return
		}
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}

		indexRequest := r.Clone(r.Context())
		urlCopy := *r.URL
		urlCopy.Path = "/"
		urlCopy.RawPath = ""
		indexRequest.URL = &urlCopy
		files.ServeHTTP(w, indexRequest)
	}), nil
}
