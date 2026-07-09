// prodenv.go wires engine.Env to the live macOS filesystem. It is the only
// engine file that touches the OS: Stat/List read real files (with APFS birth
// time and atime pulled from the darwin stat struct), while removal is delegated
// to the trash/delete functions the caller supplies. Sizing uses the injected
// sizeKB (the same du-backed probe the scanner uses) so accounting is
// consistent across the whole tool.
package engine

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ProductionEnv builds an Env backed by the real filesystem. sizeKB measures a
// path's size in kilobytes (wire osprobe.New().SizeKB), and trash/delete perform
// the two removal actions (wire trash.ToTrash and a permanent remover). Now is
// time.Now.
func ProductionEnv(sizeKB func(string) int64, trash, delete func(string) error) Env {
	return Env{
		Now:    time.Now,
		Stat:   func(p string) (FileInfo, bool) { return statPath(p, sizeKB) },
		List:   func(dir string, recursive bool) ([]FileInfo, error) { return listDir(dir, recursive, sizeKB) },
		Trash:  trash,
		Delete: delete,
	}
}

// statPath stats a single path into a FileInfo, reading birth/access time from
// the darwin stat struct. The bool is false when the path cannot be stat'd.
func statPath(path string, sizeKB func(string) int64) (FileInfo, bool) {
	fi, err := os.Lstat(path)
	if err != nil {
		return FileInfo{}, false
	}
	return toFileInfo(path, fi, sizeKB), true
}

// listDir returns the depth-1 children of dir, or every descendant file when
// recursive is true (directories are omitted from a recursive listing so watch
// jobs only ever act on files). A read error on a subdirectory is skipped so one
// unreadable folder never aborts the sweep.
func listDir(dir string, recursive bool, sizeKB func(string) int64) ([]FileInfo, error) {
	if !recursive {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		out := make([]FileInfo, 0, len(entries))
		for _, e := range entries {
			fi, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, toFileInfo(filepath.Join(dir, e.Name()), fi, sizeKB))
		}
		return out, nil
	}

	var out []FileInfo
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries, keep walking
		}
		if p == dir || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, toFileInfo(p, info, sizeKB))
		return nil
	})
	return out, err
}

// toFileInfo converts an os.FileInfo into the engine's FileInfo, extracting the
// APFS birth time and access time from the underlying darwin syscall.Stat_t.
// Size is measured via the injected sizeKB (du semantics) for directories and
// large trees; regular-file size falls back to the stat size when du returns 0.
func toFileInfo(path string, fi os.FileInfo, sizeKB func(string) int64) FileInfo {
	out := FileInfo{
		Path:    path,
		ModTime: fi.ModTime(),
		IsDir:   fi.IsDir(),
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		out.Birth = time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec)
		out.Access = time.Unix(st.Atimespec.Sec, st.Atimespec.Nsec)
	}
	if sizeKB != nil {
		out.SizeKB = sizeKB(path)
	}
	if out.SizeKB == 0 && !fi.IsDir() {
		out.SizeKB = (fi.Size() + 1023) / 1024
	}
	return out
}
