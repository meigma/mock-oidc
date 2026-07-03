package httpapi

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// staticURLPrefix is the request-path prefix the static tree is mounted under.
// The composition root wires a raw chi wildcard (mux.Handle("/static/*", …))
// because a static asset tree is multi-segment (/static/css/app.css) and cannot
// ride a single-segment Huma path param (Decision D-5 / TDD §9 mechanism 3).
const staticURLPrefix = "/static/"

// NewStaticHandler serves the static asset tree rooted at dir with two edge
// concerns the raw file server does not give us for free: a traversal guard
// (any request that escapes dir — via "..", an absolute path, or a symlink-style
// join that resolves above the root — is a flat 404 "not found", never a read of
// an out-of-tree file) and a best-effort Content-Type from
// [mime.TypeByExtension]
// (a non-contractual convenience for the browser surface). It is transport-tier
// (it reads files), which is why it lives in the adapter package and is handed to
// the generic router as a plain [http.Handler].
//
// dir must be non-empty; the composition root only mounts the /static/* route
// when a static-assets path is configured, so an unconfigured deployment serves
// no static tree at all (its /static/… requests fall through to the 404
// fallback).
func NewStaticHandler(dir string) http.Handler {
	root, err := filepath.Abs(dir)
	if err != nil {
		root = dir
	}
	// Canonicalize the root once so the per-request symlink re-check compares
	// like against like (on macOS, for example, /var is itself a symlink to
	// /private/var, so an un-evaluated root would never prefix an evaluated
	// full path). A failure here leaves root lexical; a non-existent root
	// serves nothing anyway (os.Stat below 404s before the re-check runs).
	if resolved, evalErr := filepath.EvalSymlinks(root); evalErr == nil {
		root = resolved
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, staticURLPrefix)

		full, ok := resolveStaticPath(root, rel)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// The lexical guard above cannot see through a symlink planted INSIDE
		// the root that points out of it (both os.Stat and os.Open follow
		// links). Resolve the real path and re-verify containment so the
		// doc guarantee — no read of an out-of-tree file — actually holds.
		if !pathContained(root, full) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		if ct := mime.TypeByExtension(filepath.Ext(full)); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		// ServeContent, not ServeFile: ServeFile 301-redirects any URL path
		// ending in "/index.html" to "./", which lands on the directory path
		// this handler deliberately 404s — making index.html the one
		// unreachable file in the tree. ServeContent keeps the conditional/
		// range handling without ServeFile's URL-path opinions.
		file, err := os.Open(full)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		defer func() { _ = file.Close() }()
		http.ServeContent(w, r, filepath.Base(full), info.ModTime(), file)
	})
}

// pathContained reports whether full, once its symlinks are resolved, is root
// itself or a descendant of it. It is the non-lexical companion to
// resolveStaticPath: an EvalSymlinks error (broken/looping link) or a resolution
// that climbs out of root both read as "not contained".
func pathContained(root, full string) bool {
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return false
	}
	return resolved == root || strings.HasPrefix(resolved, root+string(os.PathSeparator))
}

// resolveStaticPath maps a request-relative asset path to an absolute filesystem
// path CONTAINED within root, or reports ok=false when the path is empty or
// escapes the root. It rejects an absolute rel outright, then joins-and-cleans
// (filepath.Join collapses any "..") and verifies the result is root itself or a
// descendant — so "../../etc/passwd" resolves above root and is refused.
func resolveStaticPath(root, rel string) (string, bool) {
	if rel == "" || strings.HasPrefix(rel, "/") {
		return "", false
	}
	rel = filepath.FromSlash(rel)
	if filepath.IsAbs(rel) {
		return "", false
	}
	full := filepath.Join(root, rel)
	if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
		return "", false
	}
	return full, true
}
