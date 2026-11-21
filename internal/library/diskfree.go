package library

// Space is the size and free room of the disk a folder lives on.
type Space struct {
	Free  uint64 // bytes available to this program
	Total uint64 // bytes on the disk
}

// DiskSpace reports the free and total space of the disk that holds path. It
// uses GetDiskFreeSpaceExW on Windows and statfs on Linux and the BSDs, both
// through the syscall package.
func DiskSpace(path string) (Space, error) { return diskSpace(path) }
