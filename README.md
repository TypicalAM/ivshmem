# Inter-VM Shared Memory

Exchange info with very low latency using Inter-VM Shared Memory (IVSHMEM). This project aims to help interface communication between go programs between host machines and `qemu` virtual machine instances. **For now only windows guests are supported*.

## Prerequisites

You need to have the following:

### Enabled shared memory in qemu/libvirt

In `libvirt` (`virt-manager`) you have to add the following line to your [xml configuration](https://libvirt.org/formatdomain.html#shared-memory-device):

```xml
<shmem name='my-little-shared-memory'>
  <model type='ivshmem-plain'/>
  <size unit='M'>16</size>
</shmem>
```

On `qemu`, add the following cmd args:

```bash
-device ivshmem-plain,memdev=ivshmem,bus=pcie.0 \
-object memory-backend-file,id=ivshmem,share=on,mem-path=/dev/shm/my-little-shared-memory,size=16M
```

The `bus` parameter can be adjusted to your needs. You are going to need it when using the library.

### Guest driver

The shared memory is accessed on the windows guest using an IVSHMEM driver. The driver can be downloaded [here](https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/upstream-virtio/).

## Code examples

### Host code

```go
//go:build linux

package main

import (
	"log"

	"github.com/TypicalAM/ivshmem"
	"golang.org/x/sys/unix"
)

func main() {
	mapper, err := ivshmem.NewHost("/dev/shm/my-little-shared-memory")
	if err != nil {
		log.Fatal(err)
	}
	if err = mapper.Map(); err != nil {
		log.Fatal(err)
	}
	defer mapper.Unmap()
	log.Println("Shared mem size", mapper.Size)

	mem := mapper.SharedMem
	msg := []byte("Hello world!")
	copy(mem, msg)

	// Make sure the data is synced
	if err := unix.Msync(mem, unix.MS_SYNC); err != nil {
		log.Println("Msync error", err)
	} else {
		log.Println("Message sent to guest")
	}
}
```

### Guest code

```go
//go:build windows

package main

import (
	"log"

	"github.com/TypicalAM/ivshmem"
)

func main() {
	mapper, err := ivshmem.NewGuest(0) // 0 is the device number
	if err != nil {
		log.Fatal(err)
	}
	if err = mapper.Map(); err != nil {
		log.Fatal(err)
	}
	defer mapper.Unmap()
	log.Println("Shared mem size", mapper.Size)
	log.Println("Device path ", mapper.DevicePath)

	mem := mapper.SharedMem
	buf := make([]byte, len([]byte("Hello world!")))
	copy(buf, mem)

	log.Println("Message from host:", string(buf))
}
```

### Outputs

On host:

```
2023/09/08 17:11:37 Shared mem size 16777216
2023/09/08 17:11:37 Message sent to guest
```

On guest:

```
2023/09/08 17:09:04 Shared mem size 16777216
2023/09/08 17:09:04 Device path  \\?\pci#ven_1af4&dev_1110&subsys_11001af4&rev_01#6&784e783&0&08100012#{df576976-569d-4672-95a0-f57e4ea0b210}
2023/09/08 17:09:04 Message from host: Hello world!
```

### FAQ

- Why no CGO?
  - We would only need C to map the memory, we can use syscalls for that which are assembly that do not grow the stack.
  - Most of the functions we need are in the `golang.org/x/sys/windows` package, only need to load one dll.

- Why use this library when I can communicate using the network?
  - Shared memory is very low latency. If you need speed (I've done transfers over easily 2.4 Gbps on my machine) this could be the solution. 
