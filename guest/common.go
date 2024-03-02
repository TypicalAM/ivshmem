package guest

import (
	"errors"
	"fmt"
)

var ErrCannotFindDevice = errors.New("cannot find device")
var ErrAlreadyMapped = errors.New("already mapped")
var ErrAlreadyUnmapped = errors.New("already unmapped")
var ErrNotMapped = errors.New("not mapped yet")

// PCILocation contains info about the location of the device.
type PCILocation struct {
	bus      uint8
	device   uint8
	function uint8
}

// String representation of the PCI location, as in windows device manager.
func (p PCILocation) String() string {
	return fmt.Sprintf("PCI bus %d, device %d, function %d", p.bus, p.device, p.function)
}

// Bus returns the PCI device bus number.
func (p PCILocation) Bus() uint8 {
	return p.bus
}

// Bus returns the PCI device number.
func (p PCILocation) Device() uint8 {
	return p.device
}

// Bus returns the PCI device function.
func (p PCILocation) Function() uint8 {
	return p.function
}
