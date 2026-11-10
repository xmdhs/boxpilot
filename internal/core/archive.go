package core

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

const maxArchiveEntries = 4096

type archiveGuard struct {
	seen  map[string]bool
	total int64
	count int
}

func (g *archiveGuard) entry(name string, size int64, dir bool) error {
	g.count++
	if g.count > maxArchiveEntries {
		return errors.New("archive entry limit exceeded")
	}
	if name == "" || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") {
		return errors.New("unsafe archive path")
	}
	clean := strings.TrimSuffix(name, "/")
	if path.Clean(clean) != clean || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return errors.New("archive path traversal")
	}
	if g.seen[clean] {
		return errors.New("duplicate archive entry")
	}
	g.seen[clean] = true
	// A file may not also be a parent directory, regardless of entry ordering.
	for p := path.Dir(clean); p != "."; p = path.Dir(p) {
		if g.seen[p] && !g.seen[p+"/"] {
			return errors.New("archive file/directory collision")
		}
	}
	if dir {
		g.seen[clean+"/"] = true
	} else {
		for prior := range g.seen {
			if strings.HasPrefix(prior, clean+"/") {
				return errors.New("archive file/directory collision")
			}
		}
	}
	if size < 0 || size > maxExtractSize || g.total > maxExtractSize-size {
		return errors.New("archive expanded size limit exceeded")
	}
	g.total += size
	return nil
}

type zeroPaddingWriter struct{}

func (zeroPaddingWriter) Write(b []byte) (int, error) {
	for _, c := range b {
		if c != 0 {
			return 0, errors.New("non-padding data after tar EOF")
		}
	}
	return len(b), nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}
func extractArchive(ctx context.Context, archivePath, asset, executable string) error {
	g := archiveGuard{seen: make(map[string]bool)}
	root := strings.TrimSuffix(strings.TrimSuffix(asset, ".tar.gz"), ".zip")
	exe := "sing-box"
	if strings.HasSuffix(asset, ".zip") {
		exe += ".exe"
	}
	expected := root + "/" + exe
	found := false
	consume := func(name string, size int64, dir bool, r io.Reader) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := g.entry(name, size, dir); err != nil {
			return err
		}
		if dir && size != 0 {
			return errors.New("archive directory contains data")
		}
		clean := strings.TrimSuffix(name, "/")
		if clean != root && !strings.HasPrefix(clean, root+"/") {
			return errors.New("unexpected archive root")
		}
		if name == expected && !dir {
			if found {
				return errors.New("duplicate executable")
			}
			found = true
			f, err := os.OpenFile(executable, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
			if err != nil {
				return err
			}
			n, err := io.Copy(f, io.LimitReader(contextReader{ctx, r}, size+1))
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			if n != size {
				return errors.New("archive entry size mismatch")
			}
			return nil
		}
		if !dir {
			n, err := io.Copy(io.Discard, io.LimitReader(contextReader{ctx, r}, size+1))
			if err != nil {
				return err
			}
			if n != size {
				return errors.New("archive entry size mismatch")
			}
		}
		return nil
	}
	if strings.HasSuffix(asset, ".tar.gz") {
		f, err := os.Open(archivePath)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(contextReader{ctx, f})
		if err != nil {
			return err
		}
		defer gz.Close()
		// Bound all decompressed bytes, including headers, extended metadata and padding.
		expanded := &io.LimitedReader{R: gz, N: maxExtractSize + 1}
		tr := tar.NewReader(contextReader{ctx, expanded})
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
				return fmt.Errorf("archive links/special files are forbidden: %s", h.Name)
			}
			if err = consume(h.Name, h.Size, h.Typeflag == tar.TypeDir, tr); err != nil {
				return err
			}
		}
		// Only zero tar padding may follow EOF. Reject hidden concatenated archives,
		// and read to gzip EOF so its checksum is always validated.
		if _, err := io.Copy(zeroPaddingWriter{}, contextReader{ctx, expanded}); err != nil {
			return err
		}
		if expanded.N == 0 {
			return errors.New("archive expanded size limit exceeded")
		}
	} else if strings.HasSuffix(asset, ".zip") {
		zr, err := zip.OpenReader(archivePath)
		if err != nil {
			return err
		}
		defer zr.Close()
		if len(zr.File) > maxArchiveEntries {
			return errors.New("archive entry limit exceeded")
		}
		for _, f := range zr.File {
			if f.Mode()&os.ModeSymlink != 0 || (!f.Mode().IsRegular() && !f.FileInfo().IsDir()) {
				return errors.New("archive links/special files are forbidden")
			}
			if f.UncompressedSize64 > uint64(maxExtractSize) {
				return errors.New("archive expanded size limit exceeded")
			}
			r, err := f.Open()
			if err != nil {
				return err
			}
			err = consume(f.Name, int64(f.UncompressedSize64), f.FileInfo().IsDir(), r)
			r.Close()
			if err != nil {
				return err
			}
		}
	} else {
		return errors.New("unsupported archive format")
	}
	if !found {
		return errors.New("expected sing-box executable not found")
	}
	return nil
}
