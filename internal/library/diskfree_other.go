//go:build !windows && !linux && !darwin && !freebsd && !dragonfly

package library

import "errors"

// diskSpace is not available on this system; folders then report no free space.
func diskSpace(string) (Space, error) { return Space{}, errors.ErrUnsupported }
