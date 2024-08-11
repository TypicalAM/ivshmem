//go:build windows

package ivshmem

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var ErrInvalidHandle = errors.New("invalid handle")

// CTL_CODE(FILE_DEVICE_UNKNOWN, 0x800 to 0x805, METHOD_BUFFERED, FILE_ANY_ACCESS).
const (
	ioctlIvshmemRequestPeerID = 2236416
	ioctlIvshmemRequestSize   = 2236420
	ioctlIvshmemRequestMmap   = 2236424
	ioctlIvshmemReleaseMmap   = 2236428
	ioctlIvshmemRingDoorbell  = 2236432
	ioctlIvshmemRegisterEvent = 2236436
)

var (
	ivshmemGUID                            = windows.GUID{Data1: 0xdf576976, Data2: 0x569d, Data3: 0x4672, Data4: [8]byte{0x95, 0xa0, 0xf5, 0x7e, 0x4e, 0xa0, 0xb2, 0x10}} // Allows us to find devices recognized by the ivshmem driver (df576976-569d-4672-95a0-f57e4ea0b210)
	writeCombined                    uint8 = 2                                                                                                                             // cache mode for ioctlIvshmemRequestMmap
	setupapi                               = &windows.LazyDLL{Name: "setupapi.dll", System: true}                                                                          // Since we're loading lazily, we need not worry about DDL panics
	setupDiEnumDeviceInterfaces            = setupapi.NewProc("SetupDiEnumDeviceInterfaces")
	setupDiGetDeviceInterfaceDetailW       = setupapi.NewProc("SetupDiGetDeviceInterfaceDetailW")
)

// deviceData is some basic device data, can be used to determine the device details.
type deviceData struct {
	loc     PCILocation
	devInfo windows.DevInfoData
	busAddr uint64
}

// SP_DEVICE_INTERFACE_DATA as used in SetupDiEnumDeviceInterfaces.
type deviceInterfaceData struct {
	cbSize             uint32
	interfaceClassGUID windows.GUID
	Flags              uint32
	Reserved           uint
}

// IVSHMEM_MMAP as used in IOCTL_IVSHMEM_REQUEST_MMAP.
type ivshmemMmap struct {
	peerID      uint16
	ivshmemSize uint64
	ptr         unsafe.Pointer
	vectors     uint16
}

// ListDevices lists the available ivshmem devices by their locations.
func ListDevices() ([]PCILocation, error) {
	devInfoSet, err := windows.SetupDiGetClassDevsEx(&ivshmemGUID, "", 0, windows.DIGCF_PRESENT|windows.DIGCF_DEVICEINTERFACE, 0, "")
	if err != nil {
		return nil, fmt.Errorf("device info set: %w", err)
	}
	defer windows.SetupDiDestroyDeviceInfoList(devInfoSet)

	ivshmemDevices, err := getIvshmemDevices(devInfoSet)
	if err != nil {
		return nil, fmt.Errorf("get ivshmem devs: %w", err)
	}

	ivshmemLocations := make([]PCILocation, len(ivshmemDevices))
	for i := range ivshmemDevices {
		ivshmemLocations[i] = ivshmemDevices[i].loc
	}

	return ivshmemLocations, nil
}

// Guest allows mapping a shared memory region from the windows guest.
type Guest struct {
	devPath   string
	mapped    bool
	sharedMem []byte
	size      uint64

	devHandle windows.Handle
	devData   deviceData
}

// NewGuest returns a new memory mapper.
func NewGuest(location PCILocation) (*Guest, error) {
	devInfoSet, err := windows.SetupDiGetClassDevsEx(&ivshmemGUID, "", 0, windows.DIGCF_PRESENT|windows.DIGCF_DEVICEINTERFACE, 0, "")
	if err != nil {
		return nil, fmt.Errorf("device info set: %w", err)
	}
	defer windows.SetupDiDestroyDeviceInfoList(devInfoSet)

	ivshmemDevices, err := getIvshmemDevices(devInfoSet)
	if err != nil {
		return nil, fmt.Errorf("get ivshmem devs: %w", err)
	}

	var found bool
	var idx = -1
	for i, dev := range ivshmemDevices {
		if dev.loc == location {
			found = true
			idx = i
		}
	}

	if !found {
		return nil, ErrCannotFindDevice
	}

	handle, path, err := establishHandle(devInfoSet, ivshmemDevices[idx])
	if err != nil {
		return nil, fmt.Errorf("establish handle: %w", err)
	}

	return &Guest{devHandle: *handle, devPath: path, devData: ivshmemDevices[idx]}, nil
}

// Map maps the memory into the program address space.
func (g *Guest) Map() error {
	if g.mapped {
		return ErrAlreadyMapped
	}

	var ivshmemSize uint64
	err := windows.DeviceIoControl(g.devHandle, ioctlIvshmemRequestSize, nil, 0,
		(*byte)(unsafe.Pointer(&ivshmemSize)), uint32(unsafe.Sizeof(ivshmemSize)), nil, nil)
	if err != nil {
		return fmt.Errorf("get ivshmem size: %w", err)
	}

	memMap := ivshmemMmap{}
	err = windows.DeviceIoControl(g.devHandle, ioctlIvshmemRequestMmap, (*byte)(unsafe.Pointer(&writeCombined)),
		uint32(unsafe.Sizeof(writeCombined)), (*byte)(unsafe.Pointer(&memMap)), uint32(unsafe.Sizeof(memMap)), nil, nil)
	if err != nil {
		return fmt.Errorf("map ivshmem: %w", err)
	}

	g.sharedMem = unsafe.Slice((*byte)(memMap.ptr), ivshmemSize)
	g.size = ivshmemSize
	g.mapped = true
	return nil
}

// Unmap unmaps the memory and releases the device handles.
func (g Guest) Unmap() error {
	if !g.mapped {
		return ErrAlreadyUnmapped
	}

	err := windows.DeviceIoControl(g.devHandle, ioctlIvshmemReleaseMmap, nil, 0, nil, 0, nil, nil)
	if err != nil {
		return fmt.Errorf("release ivshmem: %w", err)
	}

	err = windows.CloseHandle(g.devHandle)
	if err != nil {
		return fmt.Errorf("close handle: %w", err)
	}

	g.mapped = false
	return nil
}

// System returns the guest system type.
func (g Guest) System() string {
	return "Windows"
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
func (g Guest) SharedMem() []byte {
	if !g.mapped {
		panic("tried to access unmapped memory")
	}

	return g.sharedMem
}

// Location returns the PCI location of the device.
func (g Guest) Location() PCILocation {
	return g.devData.loc
}

// Sync makes sure the changes made to the shared memory are synced.
func (g Guest) Sync() error {
	return windows.Fsync(g.devHandle)
}

// setupDiCall is a helper function to call SetupDi* functions.
func setupDiCall(proc *windows.LazyProc, args ...uintptr) syscall.Errno {
	r1, _, errno := syscall.SyscallN(proc.Addr(), args...)
	if r1 == 0 {
		if errno != 0 {
			return errno
		}

		return syscall.EINVAL
	}

	return 0
}

// getIvshmemDevices gets the IVSHMEM devices using the setupapi.dll information.
func getIvshmemDevices(devInfoSet windows.DevInfo) ([]deviceData, error) {
	devIndex := 0
	devInfoDatas := make([]deviceData, 0)

	for {
		devInfoData, err := windows.SetupDiEnumDeviceInfo(devInfoSet, devIndex)
		if err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
				break
			}

			return nil, fmt.Errorf("ivshmem device information: %w", err)
		}

		busNumberRaw, err := windows.SetupDiGetDeviceRegistryProperty(devInfoSet, devInfoData, windows.SPDRP_BUSNUMBER)
		if err != nil {
			return nil, fmt.Errorf("ivshmem device bus number: %w", err)
		}

		busAddressRaw, err := windows.SetupDiGetDeviceRegistryProperty(devInfoSet, devInfoData, windows.SPDRP_ADDRESS)
		if err != nil {
			return nil, fmt.Errorf("ivshmem device bus address: %w", err)
		}

		rawLocation, err := windows.SetupDiGetDeviceRegistryProperty(devInfoSet, devInfoData, windows.SPDRP_LOCATION_INFORMATION)
		if err != nil {
			return nil, fmt.Errorf("ivshmem device location: %w", err)
		}

		location, err := convertLocation(rawLocation.(string))
		if err != nil {
			return nil, fmt.Errorf("convert location: %w", err)
		}

		devInfoDatas = append(devInfoDatas, deviceData{
			loc:     *location,
			busAddr: uint64(busNumberRaw.(uint32))<<32 | uint64(busAddressRaw.(uint32)),
			devInfo: *devInfoData,
		})

		devIndex++
	}

	sort.Slice(devInfoDatas, func(i, j int) bool { return devInfoDatas[i].busAddr < devInfoDatas[j].busAddr })

	return devInfoDatas, nil
}

// establishHandle establishes a handle to the device and returns the device path and the associated handle.
func establishHandle(devInfoSet windows.DevInfo, device deviceData) (*windows.Handle, string, error) {
	devInterfaceData := deviceInterfaceData{}
	devInterfaceData.cbSize = uint32(unsafe.Sizeof(devInterfaceData))
	errno := setupDiCall(
		setupDiEnumDeviceInterfaces, uintptr(devInfoSet), uintptr(unsafe.Pointer(&device.devInfo)),
		uintptr(unsafe.Pointer(&ivshmemGUID)), 0, uintptr(unsafe.Pointer(&devInterfaceData)),
	)

	if errno != 0 {
		return nil, "", fmt.Errorf("enum device interfaces: %w", errno)
	}

	var reqSize uint32
	errno = setupDiCall(
		setupDiGetDeviceInterfaceDetailW, uintptr(devInfoSet), uintptr(unsafe.Pointer(&devInterfaceData)),
		0, 0, uintptr(unsafe.Pointer(&reqSize)), 0,
	)
	if errno != 0 && errno != windows.ERROR_INSUFFICIENT_BUFFER {
		return nil, "", fmt.Errorf("device interface getsize: %w", errno)
	}

	// SP_DEVICE_INTERFACE_DETAIL_DATA_W is a real windows moment. This is basically
	// a struct { DWORD size, UTF16[] dev_path } written as a uint16, so the first two
	// elements are the size
	devInterfaceDetailData := make([]uint16, reqSize/2)
	devInterfaceDetailData[0] = 8

	// For some reason, if there isn't a delay between the GetDeviceInterfaceDetail calls
	// errno is set to 87 (The parameter is incorrect) with no way to debug the issue.
	// Trying to print the associated variables creates a micro delay so the bug doesn't appear at all.
	//
	// A real fucking Heisenbug
	time.Sleep(5 * time.Millisecond)

	errno = setupDiCall(
		setupDiGetDeviceInterfaceDetailW, uintptr(devInfoSet), uintptr(unsafe.Pointer(&devInterfaceData)),
		uintptr(unsafe.Pointer(&devInterfaceDetailData[0])), uintptr(unsafe.Pointer(&reqSize)),
	)
	if errno != 0 {
		return nil, "", fmt.Errorf("device interface detail: %w", errno)
	}

	devicePath := &devInterfaceDetailData[2:][0]
	handle, err := windows.CreateFile(
		devicePath, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0,
	)
	if err != nil {
		return nil, "", fmt.Errorf("create file: %w", err)
	}

	if handle == windows.InvalidHandle {
		return nil, "", ErrInvalidHandle
	}

	return &handle, utf16PtrToString(devicePath), nil
}

// utf16PtrToString is like UTF16ToString, but takes *uint16 as a parameter instead of []uint16. This is taken from sys/windows and I have no clue why it isn't exported.
func utf16PtrToString(ptr *uint16) string {
	if ptr == nil {
		return ""
	}

	end := unsafe.Pointer(ptr)
	length := 0

	for *(*uint16)(end) != 0 {
		end = unsafe.Pointer(uintptr(end) + unsafe.Sizeof(*ptr))
		length++
	}

	return windows.UTF16ToString(unsafe.Slice(ptr, length))
}

// convertLocation converts the location description as given by SetupDiGetDeviceRegistryProperty to a PCILocation. Expected format: "PCI bus 4, device 1, function 0".
func convertLocation(windowsLocation string) (*PCILocation, error) {
	parts := strings.Fields(windowsLocation)
	if len(parts) != 7 {
		return nil, fmt.Errorf("invalid format: %s", windowsLocation)
	}

	bus, err := strconv.Atoi(string(parts[2][0]))
	if err != nil {
		return nil, fmt.Errorf("invalid bus format: %w", err)
	}

	device, err := strconv.Atoi(string(parts[4][0]))
	if err != nil {
		return nil, fmt.Errorf("invalid device format: %w", err)
	}

	function, err := strconv.Atoi(parts[6])
	if err != nil {
		return nil, fmt.Errorf("invalid function format: %w", err)
	}

	return &PCILocation{uint8(bus), uint8(device), uint8(function)}, nil
}
