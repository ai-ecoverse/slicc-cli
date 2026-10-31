package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func stage1GuardFixture(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "manifest.json")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, raw := range manifest["content_scripts"].([]any) {
		script := raw.(map[string]any)
		script["exclude_globs"] = []string{"*n=start-*", "*listener=1*"}
	}
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func Test675Stage1GuardFixtureSeparatesInstrumentationControlFromForeignWorlds(t *testing.T) {
	source := filepath.Join("..", "..", "node_modules", "@ai-ecoverse", "slicc-shared-web", "harness", "extension")
	root := stage1GuardFixture(t, source)
	read := func(dir, name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	var original, current map[string]any
	if err := json.Unmarshal(read(source, "manifest.json"), &original); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(read(root, "manifest.json"), &current); err != nil {
		t.Fatal(err)
	}
	for _, raw := range current["content_scripts"].([]any) {
		script := raw.(map[string]any)
		encoded, _ := json.Marshal(script["exclude_globs"])
		if string(encoded) != `["*n=start-*","*listener=1*"]` {
			t.Fatalf("control exclusions: %s", encoded)
		}
		delete(script, "exclude_globs")
	}
	oldJSON, _ := json.Marshal(original)
	newJSON, _ := json.Marshal(current)
	if !bytes.Equal(oldJSON, newJSON) {
		t.Fatal("fixture changes anything besides the ordinary instrumentation control exclusions")
	}
	for _, file := range []string{"content.js", "frame.html"} {
		if !bytes.Equal(read(source, file), read(root, file)) {
			t.Fatalf("the real extension fixture changed %s", file)
		}
	}
}
