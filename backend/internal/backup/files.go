package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"yuanfa-official/backend/internal/release"
)

func canonical(name string) (string, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	current := abs
	var suffix []string
	for {
		resolved, e := filepath.EvalSymlinks(current)
		if e == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		if !errors.Is(e, fs.ErrNotExist) {
			return "", e
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", e
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}
func overlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}
func safe(name string) bool {
	return fs.ValidPath(name) && name != "." && !strings.Contains(name, "\\") && !strings.HasPrefix(name, ".")
}
func regular(root, name string) (string, error) {
	if !safe(name) {
		return "", fail("unsafe recovery path %q", name)
	}
	full := root
	for _, part := range strings.Split(name, "/") {
		full = filepath.Join(full, part)
		info, err := os.Lstat(full)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fail("symlink forbidden: %s", name)
		}
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fail("not a regular file: %s", name)
	}
	return full, nil
}
func readRegular(root, name string) ([]byte, error) {
	full, err := regular(root, name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(full)
}
func emptyDirectory(directory string, lock bool) error {
	info, err := os.Lstat(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("target is not a directory")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !lock || entry.Name() != ".yuanfa-data.lock" {
			return errors.New("target directory must be empty")
		}
	}
	return nil
}
func copyFile(source, target, name string) error {
	full, err := regular(source, name)
	if err != nil {
		return err
	}
	in, err := os.Open(full)
	if err != nil {
		return err
	}
	defer in.Close()
	destination := filepath.Join(target, filepath.FromSlash(name))
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	return errors.Join(err, out.Close())
}
func digest(root, name string) (File, error) {
	full, err := regular(root, name)
	if err != nil {
		return File{}, err
	}
	f, err := os.Open(full)
	if err != nil {
		return File{}, err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	return File{name, hex.EncodeToString(h.Sum(nil)), size}, err
}
func inventory(root string) ([]File, error) {
	files := []File{}
	err := filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if full == root {
			if !d.IsDir() {
				return errors.New("missing payload directory")
			}
			return nil
		}
		name, e := filepath.Rel(root, full)
		if e != nil {
			return e
		}
		name = filepath.ToSlash(name)
		if d.IsDir() {
			return nil
		}
		f, e := digest(root, name)
		if e != nil {
			return e
		}
		files = append(files, f)
		return nil
	})
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return files, err
}
func compareInventory(root string, expected []File, restoring bool) error {
	actual, err := inventoryFiltered(root, restoring)
	if err != nil {
		return err
	}
	for i, f := range expected {
		if !safe(f.Path) || f.Bytes < 0 || len(f.SHA256) != 64 || (i > 0 && expected[i-1].Path >= f.Path) {
			return errors.New("invalid or duplicate backup inventory")
		}
	}
	if !reflect.DeepEqual(actual, expected) {
		return errors.New("backup file checksum or inventory mismatch")
	}
	return nil
}
func inventoryFiltered(root string, restoring bool) ([]File, error) {
	if !restoring {
		return inventory(root)
	}
	// The permanent lock and incomplete marker are not recovery payload files.
	files := []File{}
	err := filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if full == root || d.IsDir() {
			return nil
		}
		name, e := filepath.Rel(root, full)
		if e != nil {
			return e
		}
		name = filepath.ToSlash(name)
		if name == ".yuanfa-data.lock" || name == ".restore-incomplete" {
			return nil
		}
		f, e := digest(root, name)
		if e != nil {
			return e
		}
		files = append(files, f)
		return nil
	})
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return files, err
}
func treeFiles(root, name string) ([]string, error) {
	var files []string
	full := filepath.Join(root, filepath.FromSlash(name))
	info, err := os.Lstat(full)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fail("invalid retained directory: %s", name)
	}
	err = filepath.WalkDir(full, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		relative, e := filepath.Rel(root, file)
		if e != nil {
			return e
		}
		relative = filepath.ToSlash(relative)
		if _, e = regular(root, relative); e != nil {
			return e
		}
		files = append(files, relative)
		return nil
	})
	return files, err
}
func optionalFile(root, name string) (bool, error) {
	_, err := regular(root, name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
func writeJSON(name string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(raw, '\n'))
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
func strictJSON(raw []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	var rest any
	if err := d.Decode(&rest); err != io.EOF {
		return errors.New("trailing backup manifest data")
	}
	return nil
}
func syncTree(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, full)
			return nil
		}
		if !d.Type().IsRegular() {
			return errors.New("non-regular backup file")
		}
		f, e := os.Open(full)
		if e != nil {
			return e
		}
		e = f.Sync()
		return errors.Join(e, f.Close())
	})
	if err != nil {
		return err
	}
	slices.Reverse(dirs)
	for _, d := range dirs {
		if err = release.SyncDir(d); err != nil {
			return err
		}
	}
	return nil
}
func candidatePath(id string) bool { return id != "" && path.Base(id) == id && safe(id) }
