//go:build linux

package main

import (
	"fmt"
	"log"

	"github.com/TypicalAM/ivshmem/guest"
)

func main() {
	devs, err := guest.ListDevices()
	if err != nil {
		log.Fatalln("Failed to get the available IVSHMEM PCI devices:", err)
	}

	g, err := guest.New(devs[0])
	if err != nil {
		log.Fatalln("Failed to create a new guest", err)
	}

	if err := g.Map(); err != nil {
		log.Fatalln("Failed to map the shared memory:", err)
	}
	defer g.Unmap()

	fmt.Println("We are on:", g.System())
	fmt.Println("Detected PCI devices:", devs)
	fmt.Println("Selected PCI device:", g.Location())
	fmt.Println("Device path:", g.DevPath())
	fmt.Println("Shared mem size (in MB):", g.Size()/1024/1024)

	mem := g.SharedMem()
	buf := make([]byte, 12)
	copy(buf, *mem)

	fmt.Println("Message from host:", string(buf))
}
