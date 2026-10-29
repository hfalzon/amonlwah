package lhaw

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type providerFunc func(context.Context, Request) (map[string]string, error)

func (f providerFunc) Fetch(c context.Context, r Request) (map[string]string, error) { return f(c, r) }
func reset(t *testing.T) {
	t.Helper()
	state.Lock()
	state.loaded = false
	state.identity = ""
	state.Unlock()
	t.Cleanup(func() { state.Lock(); state.loaded = false; state.identity = ""; state.Unlock() })
}
func TestConcurrentLoadAndProjectGuard(t *testing.T) {
	reset(t)
	t.Setenv("LHAW_TEST_VALUE", "before")
	root := t.TempDir()
	var calls atomic.Int32
	cfg := Config{RootDir: root, Selector: "test", Provider: providerFunc(func(context.Context, Request) (map[string]string, error) {
		calls.Add(1)
		return map[string]string{"LHAW_TEST_VALUE": "after"}, nil
	})}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- LoadWithConfig(cfg) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 || os.Getenv("LHAW_TEST_VALUE") != "after" {
		t.Fatal("load was not installed exactly once")
	}
	cfg.Selector = "different"
	if LoadWithConfig(cfg) == nil || calls.Load() != 1 {
		t.Fatal("different project reached provider")
	}
}
func TestFailureRetryAndTimeout(t *testing.T) {
	reset(t)
	calls := 0
	root := t.TempDir()
	cfg := Config{RootDir: root, Selector: "test", Provider: providerFunc(func(context.Context, Request) (map[string]string, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("synthetic failure")
		}
		return nil, nil
	})}
	if LoadWithConfig(cfg) == nil {
		t.Fatal("failure hidden")
	}
	if err := LoadWithConfig(cfg); err != nil {
		t.Fatal(err)
	}
	reset(t)
	cfg.Timeout = 20 * time.Millisecond
	cfg.Provider = providerFunc(func(ctx context.Context, _ Request) (map[string]string, error) { <-ctx.Done(); return nil, ctx.Err() })
	if err := LoadWithConfig(cfg); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
}
func TestModuleRootDiscovery(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "internal", "tests")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"go.mod": "module example.invalid/test\n", ".lhaw.json": `{"selector":"test-project","dotenv_path":"test.env"}`} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestModuleRootDiscoveryHelper$")
	cmd.Dir = nested
	cmd.Env = append(os.Environ(), "LHAW_TEST_DISCOVERY_ROOT="+root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("module discovery helper: %v\n%s", err, output)
	}
}

func TestModuleRootDiscoveryHelper(t *testing.T) {
	root := os.Getenv("LHAW_TEST_DISCOVERY_ROOT")
	if root == "" {
		t.Skip("run by TestModuleRootDiscovery")
	}
	cfg, err := readConfig(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Selector != "test-project" || cfg.DotEnvPath != filepath.Join(root, "test.env") {
		t.Fatal("wrong discovery root")
	}
	cfg, err = readConfig(Config{Selector: "explicit"})
	if err != nil || cfg.Selector != "explicit" {
		t.Fatal("explicit selector ignored")
	}
}
func TestValidationBeforeInstall(t *testing.T) {
	t.Setenv("LHAW_TEST_VALUE", "before")
	for _, layer := range []map[string]string{{"PATH": "bad", "LHAW_TEST_VALUE": "after"}, {"BAD\x00KEY": "bad", "LHAW_TEST_VALUE": "after"}, {"LHAW_TEST_VALUE": "bad\x00value"}} {
		if install(layer) == nil {
			t.Fatal("invalid input accepted")
		}
		if os.Getenv("LHAW_TEST_VALUE") != "before" {
			t.Fatal("partial installation")
		}
	}
	if _, err := parseSecrets([]byte(`[{"key":"API_KEY","value":"a"},{"key":"API_KEY","value":"b"}]`)); err == nil {
		t.Fatal("duplicate accepted")
	}
	if runtime.GOOS == "windows" {
		if _, err := parseSecrets([]byte(`[{"key":"api_key","value":"a"},{"key":"API_KEY","value":"b"}]`)); err == nil {
			t.Fatal("case duplicate accepted")
		}
	}
	if _, err := parseSecrets([]byte(`[{"key":"PATHEXT","value":"a"}]`)); err == nil {
		t.Fatal("reserved key accepted")
	}
}
func TestDirectProjectID(t *testing.T) {
	reset(t)
	const id = "12345678-1234-4123-8123-123456789abc"
	err := LoadWithConfig(Config{RootDir: t.TempDir(), ProjectID: id, Provider: providerFunc(func(_ context.Context, r Request) (map[string]string, error) {
		if r.ProjectID != id || r.Selector != "" {
			t.Fatal("wrong request")
		}
		return nil, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readConfig(Config{RootDir: t.TempDir(), ProjectID: "invalid"}); err == nil {
		t.Fatal("invalid UUID accepted")
	}
}
func TestProviderProcess(t *testing.T) {
	if os.Getenv("LHAW_HELPER") != "1" {
		return
	}
	switch os.Getenv("LHAW_HELPER_MODE") {
	case "sleep":
		time.Sleep(time.Minute)
	case "error":
		os.Stderr.WriteString("synthetic sensitive output")
		os.Exit(9)
	default:
		os.Stdout.WriteString(`[{"key":"LHAW_TEST_VALUE","value":"fake"}]`)
	}
	os.Exit(0)
}
func TestSubprocessCancellationAndSafeErrors(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LHAW_HELPER", "1")
	t.Setenv("LHAW_HELPER_MODE", "sleep")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err = commandOutput(ctx, nil, binary, "-test.run=^TestProviderProcess$"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation: %v", err)
	}
	t.Setenv("LHAW_HELPER_MODE", "error")
	if _, err = commandOutput(context.Background(), nil, binary, "-test.run=^TestProviderProcess$"); err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatal("provider output leaked or failure hidden")
	}
}

// TestMain also acts as an isolated fake Amon Hen/BWS executable.
func TestMain(m *testing.M) {
	if os.Getenv("LHAW_CLI_HELPER") == "1" {
		args := os.Args[1:]
		if len(args) > 0 {
			switch args[0] {
			case "project":
				if os.Getenv("LHAW_CLI_MODE") == "missing" {
					os.Stderr.WriteString("project not found")
					os.Exit(1)
				}
				os.Stdout.WriteString("12345678-1234-4123-8123-123456789abc")
				os.Exit(0)
			case "token":
				if os.Getenv("LHAW_CLI_MODE") == "auth-error" {
					os.Stderr.WriteString("synthetic authentication detail")
					os.Exit(7)
				}
				if len(args) != 8 || args[1] != "-N" || args[3] != "--" || args[5] != "secret" || args[6] != "list" {
					os.Exit(8)
				}
				os.Stdout.WriteString(`[{"key":"LHAW_TEST_VALUE","value":"fake"}]`)
				os.Exit(0)
			case "secret":
				if len(args) != 3 || args[1] != "list" || !validUUID(args[2]) {
					os.Exit(8)
				}
				os.Stdout.WriteString(`[{"key":"LHAW_TEST_VALUE","value":"direct"}]`)
				os.Exit(0)
			}
		}
	}
	os.Exit(m.Run())
}
func TestCLIProviderDirectAndFallback(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LHAW_CLI_HELPER", "1")
	t.Setenv("LHAW_CLI_MODE", "missing")
	t.Setenv("LHAW_TEST_PROJECT", "12345678-1234-4123-8123-123456789abc")
	p := cliProvider{cfg: Config{AmonHenBinary: binary, BWSBinary: binary}}
	for _, request := range []Request{{Selector: "LHAW_TEST_PROJECT"}, {ProjectID: "12345678-1234-4123-8123-123456789abc"}} {
		got, err := p.Fetch(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if got["LHAW_TEST_VALUE"] != "direct" {
			t.Fatal("wrong direct retrieval")
		}
	}
	t.Setenv("LHAW_CLI_MODE", "success")
	got, err := p.Fetch(context.Background(), Request{Selector: "LHAW_TEST_PROJECT"})
	if err != nil || got["LHAW_TEST_VALUE"] != "fake" {
		t.Fatalf("Amon Hen retrieval: %v", err)
	}
	t.Setenv("LHAW_CLI_MODE", "auth-error")
	if _, err := p.Fetch(context.Background(), Request{Selector: "LHAW_TEST_PROJECT"}); err == nil || strings.Contains(err.Error(), "authentication detail") {
		t.Fatal("authentication failure fell back or leaked output")
	}
}
