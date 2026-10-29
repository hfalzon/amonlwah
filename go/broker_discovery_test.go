package lhaw

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func init() {
	if os.Getenv("LHAW_AUTOLOAD_HELPER") != "1" {
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "broker" {
		args := os.Args[1:]
		if len(args) != 6 || args[1] != "fetch" || args[2] != "--repo" || args[3] != os.Getenv("LHAW_AUTOLOAD_ROOT") || args[4] != "--project" || args[5] != "fixture-project" {
			os.Exit(9)
		}
		fmt.Print(`[{"key":"LHAW_AUTOLOAD_VALUE","value":"synthetic-success"}]`)
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == "--lhaw-autoload-driver" {
		if err := Load(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := Load(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if os.Getenv("LHAW_AUTOLOAD_VALUE") != "synthetic-success" || os.Getenv("BWS_ACCESS_TOKEN") != "" {
			os.Exit(8)
		}
		os.Exit(0)
	}
}

func TestLoadWithoutArgumentsUsesRepositoryGrant(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo with spaces")
	nested := filepath.Join(root, "module", "internal", "tests")
	grants := filepath.Join(root, ".amonlhaw", "grants")
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{nested, grants, bin} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{filepath.Join(root, ".git"): "fixture", filepath.Join(root, "module", "go.mod"): "module fixture\n", filepath.Join(grants, "fixture-project.json"): "{}"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	name := "amonhen"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err = os.WriteFile(filepath.Join(bin, name), data, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "--lhaw-autoload-driver")
	cmd.Dir = nested
	cmd.Env = append(os.Environ(), "LHAW_AUTOLOAD_HELPER=1", "LHAW_AUTOLOAD_ROOT="+root, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "BWS_ACCESS_TOKEN=")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Load() from nested module: %v: %s", err, output)
	}
}

func TestAutomaticGrantSelectionAndExplicitOverride(t *testing.T) {
	root := t.TempDir()
	grants := filepath.Join(root, ".amonlhaw", "grants")
	if err := os.MkdirAll(grants, 0700); err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"one", "two"} {
		if err := os.WriteFile(filepath.Join(grants, project+".json"), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := readConfig(Config{RootDir: root}); err == nil || !strings.Contains(err.Error(), "multiple project grants") {
		t.Fatalf("ambiguous selection: %v", err)
	}
	cfg, err := readConfig(Config{RootDir: root, Selector: "one"})
	if err != nil || cfg.Selector != "one" {
		t.Fatalf("explicit selection: %+v, %v", cfg, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".lhaw.json"), []byte(`{"selector":"two"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = readConfig(Config{RootDir: root})
	if err != nil || cfg.Selector != "two" {
		t.Fatalf("configured selection: %+v, %v", cfg, err)
	}
}
