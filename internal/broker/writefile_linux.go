//go:build linux

package broker

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"golang.org/x/sys/unix"
)

// writeFileUnder uses directory file descriptors and O_NOFOLLOW on Linux. The
// opened parent remains bound to the verified directory even if an attacker
// concurrently changes a path component; the final rename never follows a link.
func writeFileUnder(root, rel string, data []byte) error {
	rel = strings.ReplaceAll(rel, `\`, "/")
	clean := path.Clean(rel)
	if clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("invalid repository path")
	}
	parts := strings.Split(clean, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid repository path")
		}
	}
	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open agent worktree: %w", err)
	}
	defer unix.Close(rootFD)

	dirFD, ownsDir := rootFD, false
	for _, part := range parts[:len(parts)-1] {
		next, openErr := unix.Openat(dirFD, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(openErr, unix.ENOENT) {
			if mkdirErr := unix.Mkdirat(dirFD, part, 0755); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				if ownsDir {
					_ = unix.Close(dirFD)
				}
				return mkdirErr
			}
			next, openErr = unix.Openat(dirFD, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		if openErr != nil {
			if ownsDir {
				_ = unix.Close(dirFD)
			}
			return fmt.Errorf("path parent is not a real directory: %w", openErr)
		}
		if ownsDir {
			_ = unix.Close(dirFD)
		}
		dirFD, ownsDir = next, true
	}
	if ownsDir {
		defer unix.Close(dirFD)
	}
	base := parts[len(parts)-1]
	mode := uint32(0644)
	var existing unix.Stat_t
	if err = unix.Fstatat(dirFD, base, &existing, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		if existing.Mode&unix.S_IFMT == unix.S_IFLNK {
			return fmt.Errorf("refusing to replace symlink")
		}
		if existing.Mode&unix.S_IFMT == unix.S_IFREG {
			mode = existing.Mode & 0777
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return err
	}

	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	temp := ".collab-write-" + hex.EncodeToString(random[:])
	fd, err := unix.Openat(dirFD, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, mode)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), temp)
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = unix.Unlinkat(dirFD, temp, 0)
		return writeErr
	}
	if err = unix.Renameat(dirFD, temp, dirFD, base); err != nil {
		_ = unix.Unlinkat(dirFD, temp, 0)
		return err
	}
	if err = unix.Fsync(dirFD); err != nil && !errors.Is(err, unix.EINVAL) {
		return err
	}
	return nil
}
