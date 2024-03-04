//go:build linux

package ivshmem

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Host represents the host machine, it maps the shared memory.
type Host struct {
	shmPath   string
	sharedMem []byte
	size      uint64
	mapped    bool
}

// NewHost creates a new host mapper.
func NewHost(shmPath string) (*Host, error) {
	if _, err := os.Stat(shmPath); err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}

	return &Host{shmPath: shmPath}, nil
}

// Map maps the shared memory into the program memory space.
func (h *Host) Map() error {
	file, err := os.OpenFile(h.shmPath, os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open device file: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}

	fileSize := info.Size()

	sharedMem, err := unix.Mmap(int(file.Fd()), 0, int(fileSize), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("mmap: %w", err)
	}

	h.mapped = true
	h.sharedMem = sharedMem
	h.size = uint64(fileSize)
	return nil
}

// Unmap unmaps the shared memory.
func (h Host) Unmap() error {
	if err := unix.Munmap(h.sharedMem); err != nil {
		return fmt.Errorf("munmap: %w", err)
	}

	return nil
}

// Size returns the size of the shared memory space.
func (h Host) Size() uint64 {
	return h.size
}

// DevPath returns the device path (in this case of the shared memory file).
func (h Host) DevPath() string {
	return h.shmPath
}

// SharedMem returns the already mapped shared memory, panics if Map() didn't succeed.
func (h Host) SharedMem() *[]byte {
	if !h.mapped {
		panic("tried to access non-mapped memory")
	}

	return &h.sharedMem
}

// Sync makes sure the changes made to the shared memory are synced.
func (h Host) Sync() error {
	return unix.Msync(h.sharedMem, unix.MS_SYNC)
}
