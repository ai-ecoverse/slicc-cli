package clipboard

var utf8Env = []string{"LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8", "LC_CTYPE=UTF-8"}

func find(getenv func(string) string, look func(string) (string, error)) (*Clipboard, string) {
	paste, err1 := look("pbpaste")
	cp, err2 := look("pbcopy")
	if err1 != nil || err2 != nil {
		return nil, "no pbcopy and pbpaste on PATH"
	}
	r := tool{name: "pbpaste", path: paste, args: []string{"-Prefer", "txt"}, env: utf8Env}
	w := tool{name: "pbcopy", path: cp, env: utf8Env, capture: true}
	return &Clipboard{Name: "pbcopy", read: r.readText, write: w.writeText}, ""
}
