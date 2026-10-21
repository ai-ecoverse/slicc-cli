//go:build !unix && !windows

package clipboard

func find(func(string) string, func(string) (string, error)) (*Clipboard, string) {
	return nil, "no clipboard on this system"
}
