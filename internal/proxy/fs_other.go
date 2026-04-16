//go:build !unix

package proxy

func removeIfExists(path string) error     { return nil }
func chmod(path string, mode uint32) error { return nil }
