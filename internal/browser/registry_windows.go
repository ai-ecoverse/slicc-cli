//go:build windows

package browser

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

var appPathNames = []string{"chrome.exe", "msedge.exe", "brave.exe", "vivaldi.exe", "opera.exe"}

func registryExePaths() []string {
	var out []string
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, base := range []string{`SOFTWARE\Clients\StartMenuInternet`, `SOFTWARE\WOW6432Node\Clients\StartMenuInternet`} {
			out = append(out, startMenuInternet(root, base)...)
		}
		for _, name := range appPathNames {
			if v := regDefault(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`+name); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

func startMenuInternet(root registry.Key, base string) []string {
	k, err := registry.OpenKey(root, base, registry.ENUMERATE_SUB_KEYS|registry.READ)
	if err != nil {
		return nil
	}
	defer func() { _ = k.Close() }()
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}
	var out []string
	for _, n := range names {
		if v := regDefault(root, base+`\`+n+`\shell\open\command`); v != "" {
			out = append(out, commandExe(v))
		}
	}
	return out
}

func regDefault(root registry.Key, path string) string {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer func() { _ = k.Close() }()
	v, _, err := k.GetStringValue("")
	if err != nil {
		return ""
	}
	return commandExe(v)
}

func commandExe(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, `"`) {
		if end := strings.Index(v[1:], `"`); end >= 0 {
			return v[1 : end+1]
		}
	}
	if i := strings.Index(strings.ToLower(v), ".exe"); i >= 0 {
		return v[:i+4]
	}
	return v
}
