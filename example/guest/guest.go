package main

import (
	"fmt"
	"log"

	"github.com/TypicalAM/ivshmem"
)

func main() {
	devs, err := ivshmem.ListDevices()
	if err != nil {
		log.Fatalln("Cannot list devices:", err)
	}

	g, err := ivshmem.NewGuest(devs[1])
	if err != nil {
		log.Fatalln("Cannot create guest:", err)
	}

	err = g.Map()
	if err != nil {
		log.Fatalln("Cannot map memory:", err)
	}
	defer g.Unmap()

	fmt.Println("We are on:", g.System())
	fmt.Println("Detected IVSHMEM devices:", devs)
	fmt.Println("Selected IVSHMEM device:", g.Location())
	fmt.Println("Device path:", g.DevPath())
	fmt.Println("Shared mem size (in MB):", g.Size()/1024/1024)

	mem := g.SharedMem()
	buf := make([]byte, 12)
	copy(buf, *mem)

	fmt.Println("Message from host:", string(buf))
}
