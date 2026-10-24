package system

import "syscall"

func diskUsage(path string) (total, free int64) {
	var s syscall.Statfs_t
	if syscall.Statfs(path, &s) != nil {
		return 0, 0
	}
	bs := int64(s.Bsize)
	return int64(s.Blocks) * bs / 1024, int64(s.Bavail) * bs / 1024
}
