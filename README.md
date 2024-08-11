# Inter-VM Shared Memory

Exchange info with very low latency using Inter-VM Shared Memory (IVSHMEM). This project aims to help interface communication between go programs between host machines and `qemu` virtual machine instances.

## Getting started

In order to use the ivshmem device, we need to enable it. If you are using `libvirt` (`virt-manager`) to manege your `qemu` instances, add the following line to your [xml config](https://libvirt.org/formatdomain.html#shared-memory-device):

```xml
<shmem name='my-little-shared-memory'>
  <model type='ivshmem-plain'/>
  <size unit='M'>64</size>
</shmem>
```

If you are using raw `qemu`, use the following cmd args:

```bash
-device ivshmem-plain,memdev=ivshmem,bus=pcie.0 \
-object memory-backend-file,id=ivshmem,share=on,mem-path=/dev/shm/my-little-shared-memory,size=64M
```

Adjust the `size` parameter as needed, in this example we choose 64MB. 

### Example host code

For now only linux hosts are supported, if you want to add windows host support - feel free to contribute.

```go
//go:build linux

package main

import (
	"github.com/TypicalAM/ivshmem"
	"fmt"
)

func main() {
	h, _ := ivshmem.NewHost("/dev/shm/my-little-shared-memory")
	h.Map()
	defer h.Unmap()

	fmt.Println("Shared mem size:", h.Size())
	fmt.Println("Device path: ", h.DevPath())

	mem := h.SharedMem()
	msg := []byte("Hello world!")
	copy(mem, msg)

	h.Sync()
	fmt.Println("Write successful")
}
```

### Example guest code (windows)

> [!IMPORTANT]
> Windows guests communicate with the `ivshmem` device using a special driver. It can be downloaded [here](https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/upstream-virtio/) from the fedora website

Example guest code:

```go
package main

import (
	"fmt"
	"github.com/TypicalAM/ivshmem"
)

func main() {
	devs, _ := ivshmem.ListDevices()
	g, _ := ivshmem.NewGuest(devs[0])
	g.Map()
	defer g.Unmap()

	fmt.Println("We are on:", g.System())
	fmt.Println("Detected IVSHMEM devices:", devs)
	fmt.Println("Selected IVSHMEM device:", g.Location())
	fmt.Println("Device path:", g.DevPath())
	fmt.Println("Shared mem size (in MB):", g.Size()/1024/1024)

	mem := g.SharedMem()
	buf := make([]byte, 12)
	copy(buf, mem)

	fmt.Println("Message from host:", string(buf))
}

```

**This results in the following output:**

On host:

```
Shared mem size (in MB): 64
Device path: /dev/shm/my-little-shared-memory
Write successful
```

On guest (linux):

```
We are on: Linux
Detected IVSHMEM devices: [PCI bus 8, device 1, function 0]
Selected IVSHMEM device: PCI bus 8, device 1, function 0
Device path: /sys/bus/pci/devices/0000:08:01.0/resource2
Shared mem size (in MB): 64
Message from host: Hello world!
```

On guest (windows):

```
We are on: Windows
Detected IVSHMEM devices: [PCI bus 4, device 1, function 0 PCI bus 9, device 1, function 0]
Selected IVSHMEM device: PCI bus 9, device 1, function 0
Device path: \\?\pci#ven_1af4&dev_1110&subsys_1100...
Shared mem size (in MB): 64
Message from host: Hello world!
```

> [!TIP]
> The emulated PCI bus values will usually be mismatched with the configuration options - they might have different bus numbers. This is normal and you should not rely on bus values from the `qemu` config - instead use the provided `ivshmem.ListDevices()`

### FAQ

- Why no CGO?
  - We would only need C to map the memory (only on windows). We might as well just use syscalls for that and not require a C compiler. Most of the functions we need are in the `golang.org/x/sys/windows` package, only needing to load one dll.

- Why use this when I can communicate using the network?
  - Why anything? Also, shared memory is very low latency. If you need speed (I've done transfers over easily 2.4 Gbps on my machine) this could be the solution. 
