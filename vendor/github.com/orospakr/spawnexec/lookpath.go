package spawnexec

import (
	"os"
	"path/filepath"
	"strings"
)

// LookPath searches for an executable named file in the directories named
// by the PATH environment variable. If file contains a slash, it is tried
// directly and the PATH is not consulted. Otherwise, on success, the result
// is an absolute path.
//
// In older versions of Go, LookPath could return a path relative to the current
// directory. As of Go 1.19, LookPath will instead return that path along with
// an error satisfying errors.Is(err, ErrDot). See the package documentation for
// more details.
func LookPath(file string) (string, error) {
	// If file contains a slash, try it directly.
	if strings.Contains(file, "/") {
		err := findExecutable(file)
		if err == nil {
			return file, nil
		}
		return "", &Error{Name: file, Err: err}
	}

	path := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			// Unix shell semantics: path element "" means "."
			dir = "."
		}
		path := filepath.Join(dir, file)
		if err := findExecutable(path); err == nil {
			if !filepath.IsAbs(path) {
				if execErr := isExecutable(path); execErr {
					return path, &Error{Name: file, Err: ErrDot}
				}
			}
			return path, nil
		}
	}
	return "", &Error{Name: file, Err: ErrNotFound}
}

// findExecutable checks if the file at path exists and is executable.
func findExecutable(file string) error {
	fi, err := os.Stat(file)
	if err != nil {
		return err
	}
	m := fi.Mode()
	if m.IsDir() {
		return os.ErrPermission
	}
	if m&0111 != 0 {
		return nil
	}
	return os.ErrPermission
}
