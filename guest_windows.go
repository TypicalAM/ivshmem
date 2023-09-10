//go:build windows

package ivshmem

import (
	"errors"
	"fmt"
	"sort"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// errInvalidHandle is returned when the handle is invalid.
var errInvalidHandle = errors.New("invalid handle")

// errIndexOutOfRange is returned when the index is out of range.
var errIndexOutOfRange = errors.New("index out of range")

// deviceData allows us to sort by bus address.
type deviceData struct {
	devInfo    windows.DevInfoData
	busAddress uint64
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

// GuestMapper allows mapping a shared memory region from the windows guest.
type GuestMapper struct {
	DevicePath string
	SharedMem  []byte
	devHandle  windows.Handle
	Size       uint64
	Vectors    uint16
}

// Allows us to find devices recognized by the ivshmem driver (df576976-569d-4672-95a0-f57e4ea0b210).
var ivshmemGUID = windows.GUID{0xdf576976, 0x569d, 0x4672, [8]byte{0x95, 0xa0, 0xf5, 0x7e, 0x4e, 0xa0, 0xb2, 0x10}}

// Control codes obtained using CTL_CODE(FILE_DEVICE_UNKNOWN, 0x800 to 0x805, METHOD_BUFFERED, FILE_ANY_ACCESS).
const ioctlIvshmemRequestSize = 2236420

const (
	ioctlIvshmemRequestMmap = 2236424
	ioctlIvshmemReleaseMmap = 2236428
)

// writeCombined is a cache mode for ioctlIvshmemRequestMmap.
var writeCombined uint8 = 2

// Since we're loading lazily, we need not worry about DDL panics.
var setupapi = &windows.LazyDLL{Name: "setupapi.dll", System: true}

var (
	setupDiEnumDeviceInterfaces      = setupapi.NewProc("SetupDiEnumDeviceInterfaces")
	setupDiGetDeviceInterfaceDetailW = setupapi.NewProc("SetupDiGetDeviceInterfaceDetailW")
)

// NewGuest return a new memory mapper.
func NewGuest(devIndex int) (*GuestMapper, error) {
	devInfoSet, err := windows.SetupDiGetClassDevsEx(&ivshmemGUID, "", 0, windows.DIGCF_PRESENT|windows.DIGCF_DEVICEINTERFACE, 0, "")
	if err != nil {
		return nil, fmt.Errorf("device info set: %w", err)
	}

	ivshmemDevices, err := getIvshmemDevices(devInfoSet)
	if err != nil {
		return nil, fmt.Errorf("get ivshmem devs: %w", err)
	}

	if devIndex >= len(ivshmemDevices) {
		return nil, errIndexOutOfRange
	}

	handle, path, err := establishHandle(devInfoSet, ivshmemDevices[devIndex])
	if err != nil {
		return nil, fmt.Errorf("establish handle: %w", err)
	}

	if err := windows.SetupDiDestroyDeviceInfoList(devInfoSet); err != nil {
		return nil, fmt.Errorf("destroy device info list: %w", err)
	}

	return &GuestMapper{devHandle: *handle, DevicePath: path}, nil
}

// Map maps the memory and returns the mapped memory, the size of the memory and an error if one occurred.
func (m *GuestMapper) Map() error {
	var ivshmemSize uint64
	if err := windows.DeviceIoControl(m.devHandle, ioctlIvshmemRequestSize, nil, 0,
		(*byte)(unsafe.Pointer(&ivshmemSize)), uint32(unsafe.Sizeof(ivshmemSize)), nil, nil); err != nil {
		return fmt.Errorf("get ivshmem size: %w", err)
	}

	memMap := ivshmemMmap{}
	if err := windows.DeviceIoControl(m.devHandle, ioctlIvshmemRequestMmap, (*byte)(unsafe.Pointer(&writeCombined)),
		uint32(unsafe.Sizeof(writeCombined)), (*byte)(unsafe.Pointer(&memMap)), uint32(unsafe.Sizeof(memMap)), nil, nil); err != nil {
		return fmt.Errorf("map ivshmem: %w", err)
	}

	m.SharedMem = unsafe.Slice((*byte)(memMap.ptr), ivshmemSize)
	m.Size = ivshmemSize

	return nil
}

// Unmap unmaps the memory and releases the device handle.
func (m *GuestMapper) Unmap() error {
	if err := windows.DeviceIoControl(m.devHandle, ioctlIvshmemReleaseMmap, nil, 0, nil, 0, nil, nil); err != nil {
		return fmt.Errorf("release ivshmem: %w", err)
	}

	if err := windows.CloseHandle(m.devHandle); err != nil {
		return fmt.Errorf("close handle: %w", err)
	}

	return nil
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

		devInfoDatas = append(devInfoDatas, deviceData{
			busAddress: uint64(busNumberRaw.(uint32))<<32 | uint64(busAddressRaw.(uint32)),
			devInfo:    *devInfoData,
		})
		devIndex++
	}

	sort.Slice(devInfoDatas, func(i, j int) bool { return devInfoDatas[i].busAddress < devInfoDatas[j].busAddress })

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

	// Hack stolen from distatus/battery, also couldn't get it to work. We emulate a struct with an array of uint16, remember that
	// the first two elements are the byte count (size)
	devInterfaceDetailData := make([]uint16, reqSize/2)
	size := (*uint32)(unsafe.Pointer(&devInterfaceDetailData[0]))

	if unsafe.Sizeof(uint(0)) == 8 {
		*size = 8
	} else {
		*size = 6
	}

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
		return nil, "", errInvalidHandle
	}

	return &handle, utf16PtrToString(devicePath), nil
}

// utf16PtrToString is like UTF16ToString, but takes *uint16 as a parameter instead of []uint16. This is taken from sys/windows and I have
// no clue why it isn't exported.
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
