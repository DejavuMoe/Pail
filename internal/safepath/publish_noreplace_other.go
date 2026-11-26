//go:build !windows && !linux && !darwin

package safepath

func publishNoReplace(tmp, final string) error { return publishLstat(tmp, final) }
