//go:build linux

package ivshmem

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// HostMapper maps a file into memory.
type HostMapper struct {
	ShmPath   string
	SharedMem []byte
	Size      uint64
}

// NewHost creates a new host mapper.
func NewHost(shmPath string) (*HostMapper, error) {
	if _, err := os.Stat(shmPath); err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}

	return &HostMapper{ShmPath: shmPath}, nil
}

// Map maps the shared memory.
func (m *HostMapper) Map() error {
	file, err := os.OpenFile(m.ShmPath, os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
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

	m.SharedMem = sharedMem
	m.Size = uint64(fileSize)

	return nil
}

// Unmap unmaps the shared memory.
func (m *HostMapper) Unmap() error {
	if err := unix.Munmap(m.SharedMem); err != nil {
		return fmt.Errorf("munmap: %w", err)
	}

	return nil
}
