// Package lhaw loads project configuration into the current process environment.
package lhaw

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Request identifies the selected project without containing authentication data.
type Request struct{ Selector, ProjectID string }

// Provider retrieves application values. Implementations must honor cancellation
// and must not include credentials or provider output in errors.
type Provider interface {
	Fetch(context.Context, Request) (map[string]string, error)
}

// Config controls discovery and retrieval. Default source priority is provider,
// dotenv, then the process environment. A selected provider failure is fatal.
type Config struct {
	Selector      string        `json:"selector"`
	ProjectID     string        `json:"project_id"`
	DotEnvPath    string        `json:"dotenv_path"`
	ConfigPath    string        `json:"-"`
	RootDir       string        `json:"-"`
	AmonHenBinary string        `json:"-"`
	BWSBinary     string        `json:"-"`
	Timeout       time.Duration `json:"-"`
	Provider      Provider      `json:"-"`
}

var state struct {
	sync.Mutex
	loaded   bool
	identity string
}
var loadGate = func() chan struct{} { c := make(chan struct{}, 1); c <- struct{}{}; return c }()

// Load installs one project's values. Repeated successful loads do not refetch.
func Load(selector ...string) error { return LoadContext(context.Background(), selector...) }

// LoadContext applies cancellation and a default 30-second timeout.
func LoadContext(ctx context.Context, selector ...string) error {
	if len(selector) > 1 {
		return errors.New("lhaw: Load accepts at most one selector")
	}
	cfg := Config{}
	if len(selector) == 1 {
		cfg.Selector = selector[0]
	}
	return LoadWithConfigContext(ctx, cfg)
}
func LoadWithConfig(cfg Config) error { return LoadWithConfigContext(context.Background(), cfg) }

// LoadWithConfigContext serializes initialization. Failed loads remain retryable.
func LoadWithConfigContext(ctx context.Context, cfg Config) error {
	if ctx == nil {
		return errors.New("lhaw: nil context")
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if timeout < 0 {
		return errors.New("lhaw: timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-loadGate:
	}
	defer func() { loadGate <- struct{}{} }()
	var err error
	cfg, err = readConfig(cfg)
	if err != nil {
		return err
	}
	identity := cfg.Selector
	if cfg.ProjectID != "" {
		identity = "uuid:" + cfg.ProjectID
	}
	state.Lock()
	loaded, name := state.loaded, state.identity
	state.Unlock()
	if loaded {
		if identity == name {
			return nil
		}
		return errors.New("lhaw: a different project is already loaded")
	}
	dot, err := parseDotEnv(cfg.DotEnvPath)
	if err != nil {
		return err
	}
	provider := cfg.Provider
	if provider == nil {
		provider = cliProvider{cfg: cfg, dot: dot}
	}
	var remote map[string]string
	if cfg.Selector != "" || cfg.ProjectID != "" || cfg.Provider != nil {
		remote, err = provider.Fetch(ctx, Request{cfg.Selector, cfg.ProjectID})
		if err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	delete(dot, envKey("BWS_ACCESS_TOKEN"))
	if err = install(dot, remote); err != nil {
		return err
	}
	state.Lock()
	state.loaded = true
	state.identity = identity
	state.Unlock()
	return nil
}
func Getenv(key string) string            { return os.Getenv(key) }
func LookupEnv(key string) (string, bool) { return os.LookupEnv(key) }

func configRoot(root string) (string, error) {
	if root != "" {
		return filepath.Abs(root)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		for _, name := range []string{".lhaw.json", "go.mod"} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return dir, nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
		}
		if filepath.Dir(dir) == dir {
			return cwd, nil
		}
	}
}
func readConfig(cfg Config) (Config, error) {
	root, err := configRoot(cfg.RootDir)
	if err != nil {
		return cfg, err
	}
	path := cfg.ConfigPath
	explicit := path != ""
	if path == "" {
		path = filepath.Join(root, ".lhaw.json")
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	b, err := os.ReadFile(path)
	if err == nil {
		var file Config
		if json.Unmarshal(b, &file) != nil {
			return cfg, errors.New("lhaw: invalid configuration JSON")
		}
		if cfg.Selector == "" && cfg.ProjectID == "" {
			cfg.Selector = file.Selector
			cfg.ProjectID = file.ProjectID
		}
		if cfg.DotEnvPath == "" {
			cfg.DotEnvPath = file.DotEnvPath
		}
	} else if explicit || !errors.Is(err, os.ErrNotExist) {
		return cfg, fmt.Errorf("lhaw: read configuration: %w", err)
	}
	if cfg.Selector != "" && cfg.ProjectID != "" {
		return cfg, errors.New("lhaw: choose selector or project_id")
	}
	if cfg.ProjectID != "" && !validUUID(cfg.ProjectID) {
		return cfg, errors.New("lhaw: invalid project UUID")
	}
	if cfg.DotEnvPath == "" {
		cfg.DotEnvPath = ".env"
	}
	if !filepath.IsAbs(cfg.DotEnvPath) {
		cfg.DotEnvPath = filepath.Join(filepath.Dir(path), cfg.DotEnvPath)
	}
	if cfg.AmonHenBinary == "" {
		cfg.AmonHenBinary = "amonhen"
	}
	if cfg.BWSBinary == "" {
		cfg.BWSBinary = "bws"
	}
	return cfg, nil
}
func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
func envKey(s string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(s)
	}
	return s
}
func reservedKey(key string) bool {
	key = strings.ToUpper(key)
	switch key {
	case "PATH", "PATHEXT", "TEMP", "TMP", "TMPDIR", "USERPROFILE", "HOME", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA", "SYSTEMROOT", "WINDIR", "COMSPEC", "SHELL", "BASH_ENV", "ENV", "LD_PRELOAD", "LD_LIBRARY_PATH":
		return true
	}
	return strings.HasPrefix(key, "BWS_") || strings.HasPrefix(key, "MISE_") || strings.HasPrefix(key, "DYLD_")
}
func install(layers ...map[string]string) error {
	merged := map[string]string{}
	for _, layer := range layers {
		seen := map[string]bool{}
		for key, value := range layer {
			normalized := envKey(key)
			if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, '\x00') {
				return errors.New("lhaw: invalid environment entry")
			}
			if reservedKey(key) {
				return errors.New("lhaw: reserved environment name in configuration")
			}
			if seen[normalized] {
				return errors.New("lhaw: duplicate environment names")
			}
			seen[normalized] = true
			merged[normalized] = value
		}
	}
	type previous struct {
		value   string
		present bool
	}
	old := map[string]previous{}
	for key, value := range merged {
		v, ok := os.LookupEnv(key)
		old[key] = previous{v, ok}
		if err := os.Setenv(key, value); err != nil {
			for k, p := range old {
				if p.present {
					_ = os.Setenv(k, p.value)
				} else {
					_ = os.Unsetenv(k)
				}
			}
			return errors.New("lhaw: environment installation failed")
		}
	}
	return nil
}

type cliProvider struct {
	cfg Config
	dot map[string]string
}

func (p cliProvider) Fetch(ctx context.Context, req Request) (map[string]string, error) {
	cfg := p.cfg
	if req.ProjectID == "" {
		binary, err := exec.LookPath(cfg.AmonHenBinary)
		if err == nil {
			uuidBytes, e := commandOutput(ctx, nil, binary, "project", "uuid", "-N", req.Selector)
			if e == nil {
				uuid := strings.TrimSpace(string(uuidBytes))
				if !validUUID(uuid) {
					return nil, errors.New("lhaw: Amon Hen returned an invalid project UUID")
				}
				out, e := commandOutput(ctx, nil, binary, "token", "-N", req.Selector, "--", cfg.BWSBinary, "secret", "list", uuid)
				if e != nil {
					return nil, fmt.Errorf("lhaw: Amon Hen secret retrieval: %w", e)
				}
				return parseSecrets(out)
			}
			if !errors.Is(e, errProjectNotFound) {
				return nil, fmt.Errorf("lhaw: Amon Hen project lookup: %w", e)
			}
		} else if !errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("lhaw: locate Amon Hen: %w", err)
		}
		req.ProjectID = p.dot[envKey(req.Selector)]
		if req.ProjectID == "" {
			req.ProjectID = os.Getenv(req.Selector)
		}
		if !validUUID(req.ProjectID) {
			return nil, errors.New("lhaw: selector is not an Amon Hen project or project-ID variable")
		}
	}
	out, err := commandOutput(ctx, bwsEnv(p.dot), cfg.BWSBinary, "secret", "list", req.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("lhaw: direct BWS retrieval: %w", err)
	}
	return parseSecrets(out)
}

var errProjectNotFound = errors.New("Amon Hen project not found")

func commandOutput(ctx context.Context, env []string, binary string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	configureCommand(cmd)
	cmd.Env = env
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			message := strings.ToLower(string(ee.Stderr))
			if (strings.Contains(message, "project") && strings.Contains(message, "not found")) || strings.Contains(message, "no project named") {
				return nil, errProjectNotFound
			}
			return nil, fmt.Errorf("command exited with status %d", ee.ExitCode())
		}
		return nil, errors.New("lhaw: unable to start provider executable; check Amon Hen/BWS installation")
	}
	return out, nil
}

type bwsSecret struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func parseSecrets(data []byte) (map[string]string, error) {
	var entries []bwsSecret
	if json.Unmarshal(data, &entries) != nil {
		return nil, errors.New("lhaw: invalid BWS JSON response")
	}
	out := map[string]string{}
	for _, s := range entries {
		key := envKey(s.Key)
		if _, ok := out[key]; ok {
			return nil, errors.New("lhaw: duplicate secret names")
		}
		if reservedKey(key) {
			return nil, errors.New("lhaw: reserved name in BWS project")
		}
		out[key] = s.Value
	}
	return out, nil
}
func bwsEnv(dot map[string]string) []string {
	result := []string{}
	token, hasToken := dot[envKey("BWS_ACCESS_TOKEN")]
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if hasToken && strings.EqualFold(key, "BWS_ACCESS_TOKEN") {
			continue
		}
		result = append(result, entry)
	}
	if hasToken {
		result = append(result, "BWS_ACCESS_TOKEN="+token)
	}
	return result
}

func parseDotEnv(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lhaw: read dotenv file: %w", err)
	}
	out := map[string]string{}
	for lineNo, raw := range bytes.Split(b, []byte("\n")) {
		line := strings.TrimSpace(strings.TrimSuffix(string(raw), "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("lhaw: invalid dotenv entry on line %d", lineNo+1)
		}
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, " \t\r\n") {
			return nil, fmt.Errorf("lhaw: invalid dotenv key on line %d", lineNo+1)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		normalized := envKey(key)
		if _, exists := out[normalized]; exists {
			return nil, fmt.Errorf("lhaw: duplicate dotenv name on line %d", lineNo+1)
		}
		out[normalized] = value
	}
	return out, nil
}
