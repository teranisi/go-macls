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

// isFile reports whether p is itself a regular file (lstat, so a symlink --
// even one to a regular file -- doesn't count). Used to tell apart the
// three basic kinds of directory entry (see getDisplayTagInfo(), which
// checks this alongside isDir() and isSymlink()); isFileFollow() below is
// almost always the one actually wanted for anything about a file's own
// content, like a thumbnail.
func isFile(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.Mode().IsRegular()
}

// isFileFollow reports whether p is a regular file, or a symlink that
// resolves to one (stat, following symlinks) -- matching isDirFollow's own
// symlink handling above. -I wants a thumbnail for whatever content a
// symlinked image/document actually points to, the same as a plain file;
// isFile()'s Lstat-based check would see only the symlink itself (mode
// os.ModeSymlink, never "regular") and skip every symlinked entry outright.
func isFileFollow(p string) bool {
	info, err := os.Stat(p)
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
