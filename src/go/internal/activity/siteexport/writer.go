package siteexport

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

const (
	// ContentType and CacheControl are set on every object (contract §1).
	ContentType  = "application/json; charset=utf-8"
	CacheControl = "public, max-age=300"
)

// GCSWriter writes the site files under prefix in a bucket.
type GCSWriter struct {
	Client *storage.Client
	Bucket string
	Prefix string // e.g. "site/james"
}

func (w *GCSWriter) object(name string) string {
	return path.Join(w.Prefix, name)
}

func (w *GCSWriter) Write(ctx context.Context, name string, data []byte) error {
	obj := w.Client.Bucket(w.Bucket).Object(w.object(name))
	wc := obj.NewWriter(ctx)
	wc.ContentType = ContentType
	wc.CacheControl = CacheControl
	if _, err := wc.Write(data); err != nil {
		_ = wc.Close()
		return err
	}
	return wc.Close()
}

func (w *GCSWriter) Delete(ctx context.Context, name string) error {
	err := w.Client.Bucket(w.Bucket).Object(w.object(name)).Delete(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil
	}
	return err
}

func (w *GCSWriter) List(ctx context.Context, prefix string) ([]string, error) {
	full := w.object(prefix)
	if strings.HasSuffix(prefix, "/") && !strings.HasSuffix(full, "/") {
		full += "/"
	}
	it := w.Client.Bucket(w.Bucket).Objects(ctx, &storage.Query{Prefix: full})
	var names []string
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return names, nil
		}
		if err != nil {
			return nil, err
		}
		names = append(names, strings.TrimPrefix(attrs.Name, strings.TrimSuffix(w.Prefix, "/")+"/"))
	}
}

// DirWriter writes the same files into a local directory (--dry-run --out).
type DirWriter struct {
	Root string
}

func (w *DirWriter) Write(_ context.Context, name string, data []byte) error {
	p := filepath.Join(w.Root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func (w *DirWriter) Delete(_ context.Context, name string) error {
	err := os.Remove(filepath.Join(w.Root, filepath.FromSlash(name)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (w *DirWriter) List(_ context.Context, prefix string) ([]string, error) {
	dir := filepath.Join(w.Root, filepath.FromSlash(prefix))
	var names []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(w.Root, p)
		if err != nil {
			return err
		}
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	return names, err
}
