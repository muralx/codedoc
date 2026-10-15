// Package fork is a locally replaced module.
package fork

// Forked reports that the local copy is in use.
func Forked() bool { return true }
