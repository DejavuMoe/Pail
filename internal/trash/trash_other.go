//go:build !windows && !linux && !darwin

package trash

func moveToTrash(string) error { return ErrUnavailable }
