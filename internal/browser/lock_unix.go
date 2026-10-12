//go:build !windows

package browser

func lockHeld(dir string) bool {
	return profileOwner(dir) > 0
}
