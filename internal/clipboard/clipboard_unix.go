//go:build unix && !darwin

package clipboard

func find(getenv func(string) string, look func(string) (string, error)) (*Clipboard, string) {
	both := func(a, b string) (string, string, bool) {
		pa, errA := look(a)
		pb, errB := look(b)
		return pa, pb, errA == nil && errB == nil
	}
	if getenv("WAYLAND_DISPLAY") != "" {
		if c, p, ok := both("wl-copy", "wl-paste"); ok {
			r := tool{name: "wl-paste", path: p, args: []string{"--no-newline", "--type", "text"}, empty: []string{"Nothing is copied", "No selection", "No suitable type"}}
			w := tool{name: "wl-copy", path: c, args: []string{"--type", "text/plain;charset=utf-8"}}
			return &Clipboard{Name: "wl-copy", read: r.readText, write: w.writeText}, ""
		}
	}
	if getenv("DISPLAY") != "" {
		if p, err := look("xclip"); err == nil {
			r := tool{name: "xclip", path: p, args: []string{"-selection", "clipboard", "-out", "-target", "UTF8_STRING"}, empty: []string{"not available", "There is no owner"}}
			w := tool{name: "xclip", path: p, args: []string{"-selection", "clipboard", "-in"}}
			return &Clipboard{Name: "xclip", read: r.readText, write: w.writeText}, ""
		}
		if p, err := look("xsel"); err == nil {
			r := tool{name: "xsel", path: p, args: []string{"--clipboard", "--output"}}
			w := tool{name: "xsel", path: p, args: []string{"--clipboard", "--input"}}
			return &Clipboard{Name: "xsel", read: r.readText, write: w.writeText}, ""
		}
	}
	switch {
	case getenv("WAYLAND_DISPLAY") == "" && getenv("DISPLAY") == "":
		return nil, "no display (neither WAYLAND_DISPLAY nor DISPLAY is set)"
	case getenv("DISPLAY") == "":
		return nil, "no wl-copy and wl-paste on PATH"
	}
	return nil, "no wl-copy, xclip or xsel on PATH"
}
