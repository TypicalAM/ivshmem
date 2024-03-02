//go:build linux

package guest

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	PCI_PATH       = "/sys/bus/pci/devices"
	IVSHMEM_VENDOR = "0x1af4" // Red Hat, Inc.
	IVSHMEM_DEVICE = "0x1110" // Inter-VM shared memory
)

// ListDevices lists the available ivshmem devices by their locations. The devices are identified by their vendor and device ids.
func ListDevices() ([]PCILocation, error) {
	devices, err := listIvshmemPCIRaw()
	if err != nil {
		return nil, fmt.Errorf("get raw devices: %w", err)
	}

	result := make([]PCILocation, 0)
	for _, dev := range devices {
		loc, err := convertLocation(dev)
		if err != nil {
			fmt.Println(err)
			continue
		}

		result = append(result, *loc)
	}

	// Sort by bus -> device -> function
	sort.Slice(result, func(a, b int) bool {
		if result[a].bus < result[b].bus {
			return true
		} else if result[a].bus > result[b].bus {
			return false
		}

		if result[a].device < result[b].device {
			return true
		} else if result[a].device > result[b].device {
			return false
		}

		if result[a].function < result[b].function {
			return true
		} else if result[a].function > result[b].function {
			return false
		}

		return true
	})

	return result, nil
}

// convertLocation converts the PCI folder name to a PCILocation (for example "0000:08:00.0").
func convertLocation(locationDescription string) (*PCILocation, error) {
	parts := strings.Split(locationDescription, ":")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid location description: %s", locationDescription)
	}

	bus, err := strconv.ParseUint(parts[1], 16, 8)
	if err != nil {
		return nil, fmt.Errorf("parse bus: %w", err)
	}

	devFunc := strings.Split(parts[2], ".")
	if len(devFunc) != 2 {
		return nil, fmt.Errorf("invalid device/function description: %s", devFunc)
	}

	device, err := strconv.ParseUint(devFunc[0], 16, 8)
	if err != nil {
		return nil, fmt.Errorf("parse device: %w", err)
	}

	function, err := strconv.ParseUint(devFunc[1], 16, 8)
	if err != nil {
		return nil, fmt.Errorf("parse function: %w", err)
	}

	return &PCILocation{
		bus:      uint8(bus),
		device:   uint8(device),
		function: uint8(function),
	}, nil
}

// Guest allows to map a shared memory region.
type Guest struct {
	loc       PCILocation
	devPath   string
	mapped    bool
	sharedMem []byte
	size      uint64
}

// New returns a new Guest based on the PCI location.
func New(location PCILocation) (*Guest, error) {
	devices, err := listIvshmemPCIRaw()
	if err != nil {
		return nil, fmt.Errorf("get raw devices: %w", err)
	}

	var found bool
	var idx = -1
	for i, dev := range devices {
		loc, err := convertLocation(dev)
		if err != nil {
			return nil, fmt.Errorf("convert location: %w", err)
		}

		if *loc == location {
			found = true
			idx = i
		}
	}

	if !found {
		return nil, ErrCannotFindDevice
	}

	path := fmt.Sprintf("%s/%s/%s", PCI_PATH, devices[idx], "resource2")
	return &Guest{
		loc:     location,
		devPath: path,
	}, nil
}

// Map maps the memory into the program address space.
func (g *Guest) Map() error {
	if g.mapped {
		return ErrAlreadyMapped
	}

	stat, err := os.Stat(g.devPath)
	if err != nil {
		return fmt.Errorf("get size: %w", err)
	}

	file, err := os.OpenFile(g.devPath, os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open device file: %w", err)
	}
	defer file.Close()

	sharedMem, err := unix.Mmap(int(file.Fd()), 0, int(stat.Size()), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("mmap: %w", err)
	}

	g.sharedMem = sharedMem
	g.size = uint64(stat.Size())
	g.mapped = true
	return nil
}

// Unmap unmaps the memory.
func (g Guest) Unmap() error {
	if !g.mapped {
		return ErrAlreadyUnmapped
	}

	if err := unix.Munmap(g.sharedMem); err != nil {
		return fmt.Errorf("munmap: %w", err)
	}

	g.mapped = false
	return nil
}

// System returns the guest system type.
func (g Guest) System() string {
	return "Linux"
}

// Size returns the shared memory size in bytes.
func (g Guest) Size() uint64 {
	return g.size
}

// DevPath returns the device path.
func (g Guest) DevPath() string {
	return g.devPath
}

// SharedMem returns the shared memory region. Panics if the shared memory isn't mapped yet.
func (g Guest) SharedMem() *[]byte {
	if !g.mapped {
		panic("tried to access unmapped memory")
	}

	return &g.sharedMem
}

// Location returns the PCI location of the device.
func (g Guest) Location() PCILocation {
	return g.loc
}

// Sync makes sure the changes made to the shared memory are synced.
func (g Guest) Sync() error {
	return unix.Msync(g.sharedMem, unix.MS_SYNC)
}

// listIvshmemPCIRaw returns the ivshmem PCI names as seen in PCI_PATH.
func listIvshmemPCIRaw() ([]string, error) {
	entry, err := os.ReadDir(PCI_PATH)
	if err != nil {
		return nil, fmt.Errorf("read pci dir: %w", err)
	}

	devices := make([]string, 0)
	for _, test := range entry {
		if len(test.Name()) == 12 && len(strings.Split(test.Name(), ":")) == 3 {
			devices = append(devices, test.Name())
		}
	}

	ivshmemDevices := make([]string, 0)
	for _, dev := range devices {
		data, err := os.ReadFile(fmt.Sprintf("%s/%s/%s", PCI_PATH, dev, "vendor"))
		if err != nil {
			return nil, fmt.Errorf("vendor read: %w", err)
		}

		vendorName := strings.TrimSpace(string(data))
		if vendorName != IVSHMEM_VENDOR {
			continue
		}

		data, err = os.ReadFile(fmt.Sprintf("%s/%s/%s", PCI_PATH, dev, "device"))
		if err != nil {
			return nil, fmt.Errorf("device read: %w", err)
		}

		deviceName := strings.TrimSpace(string(data))
		if deviceName != IVSHMEM_DEVICE {
			continue
		}

		ivshmemDevices = append(ivshmemDevices, dev)
	}

	return ivshmemDevices, nil
}
