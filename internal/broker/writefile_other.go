//go:build !linux

package broker

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Non-Linux fallback uses explicit symlink checks. Platforms that need strong
// race-resistant enforcement should use an OS-level sandbox or port the Linux
// openat/O_NOFOLLOW implementation to their native filesystem APIs.
func writeFileUnder(root, rel string, data []byte) error {
	rel = strings.ReplaceAll(rel, `\`, "/")
	clean := path.Clean(rel)
	if clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("invalid repository path")
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	parts := strings.Split(clean, "/")
	parent := rootReal
	for _, part := range parts[:len(parts)-1] {
		parent = filepath.Join(parent, filepath.FromSlash(part))
		st, statErr := os.Lstat(parent)
		if statErr == nil && st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink path component")
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			return statErr
		}
		if os.IsNotExist(statErr) {
			if err = os.Mkdir(parent, 0755); err != nil && !os.IsExist(err) {
				return err
			}
		}
	}
	target := filepath.Join(parent, filepath.FromSlash(parts[len(parts)-1]))
	if st, statErr := os.Lstat(target); statErr == nil && st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to replace symlink")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	mode := os.FileMode(0644)
	if st, statErr := os.Stat(target); statErr == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(parent, ".collab-write-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(mode); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, target)
}
