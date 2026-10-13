package fence

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
)

const name = "slicc"

func ConfigDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func DataDir() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return ConfigDir()
	case "windows":
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, name), nil
		}
		return ConfigDir()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(xdg("XDG_DATA_HOME", filepath.Join(home, ".local", "share")), name), nil
}

func Paths() []string {
	var out []string
	add := func(p string) {
		if p != "" && filepath.IsAbs(p) {
			out = append(out, filepath.Clean(p))
		}
	}
	for _, f := range []func() (string, error){ConfigDir, DataDir, cacheDir} {
		if p, err := f(); err == nil {
			add(p)
		}
	}
	home, _ := os.UserHomeDir()
	for _, p := range jerkPaths(home) {
		add(p)
	}
	for _, p := range fnoxPaths(home) {
		add(p)
	}
	for _, p := range browser.PrivateDirs(browser.Options{}) {
		add(p)
	}
	return out
}

func cacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func xdg(env, fallback string) string {
	if v := os.Getenv(env); v != "" && filepath.IsAbs(v) {
		return v
	}
	return fallback
}

func jerkPaths(home string) []string {
	out := []string{os.Getenv("SLICC_JERK_CONFIG")}
	if runtime.GOOS == "windows" {
		for _, env := range []string{"APPDATA", "LOCALAPPDATA", "ProgramData"} {
			if dir := os.Getenv(env); dir != "" {
				out = append(out, filepath.Join(dir, "slicc-jerk"))
			}
		}
		return out
	}
	if home == "" {
		return append(out, "/etc/slicc-jerk")
	}
	return append(out,
		filepath.Join(xdg("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "slicc-jerk"),
		filepath.Join(xdg("XDG_STATE_HOME", filepath.Join(home, ".local", "state")), "slicc-jerk"),
		"/etc/slicc-jerk",
	)
}

func fnoxPaths(home string) []string {
	var out []string
	if home != "" {
		configBase := filepath.Join(home, ".config")
		stateBase := filepath.Join(home, ".local", "state")
		dataBase := filepath.Join(home, ".local", "share")
		if runtime.GOOS == "windows" {
			configBase = filepath.Join(home, "AppData", "Local")
			stateBase, dataBase = configBase, configBase
		}
		data := xdg("FNOX_DATA_DIR", filepath.Join(xdg("XDG_DATA_HOME", dataBase), "fnox"))
		out = append(out,
			xdg("FNOX_CONFIG_DIR", filepath.Join(xdg("XDG_CONFIG_HOME", configBase), "fnox")),
			xdg("FNOX_STATE_DIR", filepath.Join(xdg("XDG_STATE_HOME", stateBase), "fnox")),
			data,
			xdg("FNOX_PLUGINS_DIR", filepath.Join(data, "plugins")),
		)
	}
	if key := os.Getenv("FNOX_AGE_KEY_FILE"); key != "" {
		if rest, ok := strings.CutPrefix(key, "~"); ok && home != "" && (rest == "" || rest[0] == '/' || rest[0] == filepath.Separator) {
			key = filepath.Join(home, rest)
		}
		out = append(out, key)
	}
	if runtime.GOOS != "windows" {
		if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
			out = append(out, filepath.Join(dir, "fnox"))
		}
		user := "fnox-" + strconv.Itoa(os.Geteuid())
		out = append(out, filepath.Join(os.TempDir(), user), filepath.Join("/tmp", user))
		if tmp := darwinUserTemp(); tmp != "" {
			out = append(out, filepath.Join(tmp, user))
		}
	}
	return out
}

func darwinUserTemp() string {
	if runtime.GOOS != "darwin" || os.Getenv("TMPDIR") != "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/getconf", "DARWIN_USER_TEMP_DIR").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
