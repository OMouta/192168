//go:build !windows

package config

// shareLogDir has nothing to open up: the directory belongs to whoever made it.
func shareLogDir(dir string) error { return nil }
