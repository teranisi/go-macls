package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// isDir reports whether p is itself a directory (lstat, so a symlink to
// one doesn't count).
func isDir(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.IsDir()
}

// isDirFollow reports whether p is a directory, or a symlink that resolves
// to one (stat, following symlinks).
func isDirFollow(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func isSymlink(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func isFile(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.Mode().IsRegular()
}

func lexists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func unixAccessX(p string) error {
	return unix.Access(p, unix.X_OK)
}

// statMtime returns info's modification time as a Unix timestamp,
// matching Python's int(os.lstat(p).st_mtime).
func statMtime(info os.FileInfo) int64 {
	return info.ModTime().Unix()
}
