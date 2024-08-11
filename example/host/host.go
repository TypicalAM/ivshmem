//go:build linux

package main

import (
	"fmt"
	"log"

	"github.com/TypicalAM/ivshmem"
)

func main() {
	h, err := ivshmem.NewHost("/dev/shm/my-little-shared-memory")
	if err != nil {
		log.Fatalln("Failed to attach to shmem file:", err)
	}

	if err := h.Map(); err != nil {
		log.Fatalln("Failed to map memory from file:", err)
	}
	defer h.Unmap()

	fmt.Println("Shared mem size (in MB):", h.Size()/1024/1024)
	fmt.Println("Device path:", h.DevPath())

	mem := h.SharedMem()
	msg := []byte("Hello example!")
	copy(mem, msg)

	if err := h.Sync(); err != nil {
		log.Fatalln("Failed to flush the memory after writing")
	}
	fmt.Println("Write successful")
}
