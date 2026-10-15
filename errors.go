package sunspec

import (
	"errors"
	"fmt"
)

var (
	// ErrNotSunSpec is returned by Discover when no base address holds the SunSpec marker.
	ErrNotSunSpec = errors.New("sunspec: no SunSpec marker")

	// ErrInvalidMap is matched by *MapError.
	ErrInvalidMap = errors.New("sunspec: invalid map")

	// ErrNoDevice is returned by Read and Write on a model from NewModel, which has no device to read from.
	ErrNoDevice = errors.New("sunspec: model has no device")
)

// Sentinels wrapped by *PointError.
var (
	// ErrNoPoint is returned by the setters of a nil *Point: one a model does not have.
	ErrNoPoint = errors.New("sunspec: no such point")

	// ErrReadOnly is a write to a point that is not writable.
	ErrReadOnly = errors.New("sunspec: read-only point")

	// ErrType is a value of the wrong kind for the point's type, such as text for an int16.
	ErrType = errors.New("sunspec: wrong type")

	// ErrOutOfRange is a value the point's type cannot hold, its not-implemented value included.
	ErrOutOfRange = errors.New("sunspec: value out of range")

	// ErrNotImplemented is a scaled value set while the point's scale factor is not implemented.
	ErrNotImplemented = errors.New("sunspec: scale factor not implemented")
)

// MapError is returned for a device whose models cannot be found or read as their headers say, such as a model past
// address 65535, or one whose header changed since Discover. It matches ErrInvalidMap.
type MapError struct {
	Address uint16
	Reason  string
}

func (e *MapError) Error() string {
	return fmt.Sprintf("sunspec: invalid map at %d: %s", e.Address, e.Reason)
}

func (e *MapError) Is(target error) bool {
	return target == ErrInvalidMap
}

// PointError is returned by a point's setters. It unwraps to one of ErrReadOnly, ErrType, ErrOutOfRange or
// ErrNotImplemented.
type PointError struct {
	Model uint16
	Point string
	Err   error
}

func (e *PointError) Error() string {
	return fmt.Sprintf("%s, for point %s of model %d", e.Err, e.Point, e.Model)
}

func (e *PointError) Unwrap() error {
	return e.Err
}
