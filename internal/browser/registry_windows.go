//go:build windows

package browser

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

var hives = map[string]registry.Key{"HKCU": registry.CURRENT_USER, "HKLM": registry.LOCAL_MACHINE}

func registryExePaths() []string {
	reg := catalog.Platforms.Windows.Registry
	var out []string
	for _, hive := range reg.Hives {
		root := hives[hive]
		for _, base := range reg.StartMenuInternet {
			out = append(out, startMenuInternet(root, base, reg.OpenCommand)...)
		}
		for _, name := range reg.AppPathNames {
			if v := regDefault(root, reg.AppPaths+`\`+name); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

func startMenuInternet(root registry.Key, base, command string) []string {
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
		if v := regDefault(root, base+`\`+n+`\`+command); v != "" {
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
