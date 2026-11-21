//go:build linux || darwin || freebsd || dragonfly

package library

import "syscall"

func diskSpace(path string) (Space, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Space{}, err
	}
	// The field types differ between systems; converting through uint64 works
	// for all of them.
	bsize := uint64(st.Bsize)
	return Space{Free: uint64(st.Bavail) * bsize, Total: uint64(st.Blocks) * bsize}, nil
}
