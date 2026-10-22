// Package lhaw loads application configuration from Amon Hen, a .env file,
// and the process environment without changing the process environment.
package lhaw

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Config controls source discovery. Empty fields use the documented defaults.
type Config struct {
	// Selector names an Amon Hen project. If it is not configured in Amon Hen,
	// it is interpreted as the name of an environment variable containing a BWS
	// project UUID.
	Selector string `json:"selector"`
	// DotEnvPath defaults to .env in the current working directory.
	DotEnvPath string `json:"dotenv_path"`
	// ConfigPath defaults to .lhaw.json. An explicit Load selector overrides it.
	ConfigPath string `json:"-"`
	// AmonHenBinary and BWSBinary allow non-default executable locations.
	AmonHenBinary string `json:"-"`
	BWSBinary     string `json:"-"`
}

type values struct {
	bws  map[string]string
	dot  map[string]string
	env  map[string]string
	name string
}

var state struct {
	sync.RWMutex
	loaded bool
	v      values
}

// Load discovers the project selector and snapshots all configured sources.
// At most one project may be loaded in a process. Calling Load again with the
// same effective selector is harmless; a different selector returns an error.
func Load(selector ...string) error {
	if len(selector) > 1 {
		return errors.New("lhaw: Load accepts at most one selector")
	}
	cfg, err := readConfig(Config{})
	if err != nil {
		return err
	}
	if len(selector) == 1 {
		cfg.Selector = selector[0]
	}
	v, err := loadValues(cfg)
	if err != nil {
		return err
	}
	state.Lock()
	defer state.Unlock()
	if state.loaded {
		if state.v.name == v.name {
			return nil
		}
		return fmt.Errorf("lhaw: already loaded selector %q; one project per process is supported", state.v.name)
	}
	state.v = v
	state.loaded = true
	return nil
}

// LoadWithConfig is the explicit configuration variant of Load.
func LoadWithConfig(cfg Config) error {
	var err error
	cfg, err = readConfig(cfg)
	if err != nil {
		return err
	}
	v, err := loadValues(cfg)
	if err != nil {
		return err
	}
	state.Lock()
	defer state.Unlock()
	if state.loaded {
		if state.v.name == v.name {
			return nil
		}
		return fmt.Errorf("lhaw: already loaded selector %q; one project per process is supported", state.v.name)
	}
	state.v, state.loaded = v, true
	return nil
}

// Getenv returns the value associated with key, or "" if it is absent.
func Getenv(key string) string { value, _ := LookupEnv(key); return value }

// LookupEnv returns the selected value and reports whether it exists. An
// explicitly empty value is present.
func LookupEnv(key string) (string, bool) {
	state.RLock()
	defer state.RUnlock()
	if !state.loaded {
		return "", false
	}
	for _, layer := range []map[string]string{state.v.bws, state.v.dot, state.v.env} {
		if v, ok := layer[key]; ok {
			return v, true
		}
	}
	return "", false
}

func readConfig(cfg Config) (Config, error) {
	path := cfg.ConfigPath
	if path == "" {
		path = ".lhaw.json"
	}
	b, err := os.ReadFile(path)
	if err == nil {
		var file Config
		if e := json.Unmarshal(b, &file); e != nil {
			return cfg, fmt.Errorf("lhaw: parse %s: %w", path, e)
		}
		if cfg.Selector == "" {
			cfg.Selector = file.Selector
		}
		if cfg.DotEnvPath == "" {
			cfg.DotEnvPath = file.DotEnvPath
		}
		if cfg.AmonHenBinary == "" {
			cfg.AmonHenBinary = file.AmonHenBinary
		}
		if cfg.BWSBinary == "" {
			cfg.BWSBinary = file.BWSBinary
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return cfg, fmt.Errorf("lhaw: read %s: %w", path, err)
	}
	if cfg.DotEnvPath == "" {
		cfg.DotEnvPath = ".env"
	}
	if cfg.AmonHenBinary == "" {
		cfg.AmonHenBinary = "amonhen"
	}
	if cfg.BWSBinary == "" {
		cfg.BWSBinary = "bws"
	}
	return cfg, nil
}

func loadValues(cfg Config) (values, error) {
	dot, err := parseDotEnv(cfg.DotEnvPath)
	if err != nil {
		return values{}, err
	}
	env := snapshotEnv()
	v := values{dot: dot, env: env, name: cfg.Selector}
	if cfg.Selector == "" {
		return v, nil
	}

	if binary, e := exec.LookPath(cfg.AmonHenBinary); e == nil {
		if uuid, projectErr := commandOutput(binary, "project", "uuid", "-N", cfg.Selector); projectErr == nil {
			uuid = strings.TrimSpace(uuid)
			if uuid == "" {
				return values{}, errors.New("lhaw: Amon Hen returned an empty project UUID")
			}
			bwsJSON, e := commandOutput(binary, "token", "-N", cfg.Selector, "--", cfg.BWSBinary, "secret", "list", uuid)
			if e != nil {
				return values{}, fmt.Errorf("lhaw: retrieve Amon Hen project secrets: %w", e)
			}
			secrets, e := parseSecrets([]byte(bwsJSON))
			if e != nil {
				return values{}, e
			}
			v.bws = secrets
			return v, nil
		} else if !isProjectNotFound(projectErr) {
			return values{}, fmt.Errorf("lhaw: Amon Hen project lookup failed: %w", projectErr)
		}
	} else if !errors.Is(e, exec.ErrNotFound) {
		return values{}, fmt.Errorf("lhaw: locate Amon Hen: %w", e)
	}

	projectID, ok := dot[cfg.Selector]
	if !ok {
		projectID, ok = env[cfg.Selector]
	}
	if !ok || strings.TrimSpace(projectID) == "" {
		return values{}, fmt.Errorf("lhaw: selector %q is neither an Amon Hen project nor a non-empty project-ID environment variable", cfg.Selector)
	}
	args := []string{"secret", "list", strings.TrimSpace(projectID)}
	cmd := exec.Command(cfg.BWSBinary, args...)
	cmd.Env = bwsEnv(dot, env)
	out, err := cmd.Output()
	if err != nil {
		return values{}, fmt.Errorf("lhaw: direct BWS secret retrieval failed: %w", safeExecError(err))
	}
	v.bws, err = parseSecrets(out)
	if err != nil {
		return values{}, err
	}
	return v, nil
}

var errProjectNotFound = errors.New("Amon Hen project not found")

func commandOutput(binary string, args ...string) (string, error) {
	cmd := exec.Command(binary, args...)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && isProjectNotFound(ee) {
			return "", errProjectNotFound
		}
		return "", safeExecError(err)
	}
	return string(out), nil
}

func safeExecError(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		// Provider output is omitted because it may echo credentials or values.
		return fmt.Errorf("command exited with status %d", ee.ExitCode())
	}
	return err
}

func isProjectNotFound(err error) bool {
	if errors.Is(err, errProjectNotFound) {
		return true
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	message := strings.ToLower(string(ee.Stderr))
	return strings.Contains(message, "not found") && strings.Contains(message, "project") || strings.Contains(message, "no project named")
}

type bwsSecret struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func parseSecrets(data []byte) (map[string]string, error) {
	var secrets []bwsSecret
	if err := json.Unmarshal(data, &secrets); err != nil {
		return nil, fmt.Errorf("lhaw: parse BWS secret response: %w", err)
	}
	out := make(map[string]string, len(secrets))
	for _, s := range secrets {
		out[s.Key] = s.Value
	}
	return out, nil
}

func snapshotEnv() map[string]string {
	out := map[string]string{}
	for _, entry := range os.Environ() {
		k, v, ok := strings.Cut(entry, "=")
		if ok {
			out[k] = v
		}
	}
	return out
}

func bwsEnv(dot, env map[string]string) []string {
	// Preserve the current CLI configuration and environment, but expose only
	// the BWS token from .env to the BWS child process.
	result := os.Environ()
	token, ok := dot["BWS_ACCESS_TOKEN"]
	if !ok {
		token, ok = env["BWS_ACCESS_TOKEN"]
	}
	if !ok {
		return result
	}
	for i, entry := range result {
		if strings.HasPrefix(entry, "BWS_ACCESS_TOKEN=") {
			result[i] = "BWS_ACCESS_TOKEN=" + token
			return result
		}
	}
	return append(result, "BWS_ACCESS_TOKEN="+token)
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
		out[key] = value
	}
	return out, nil
}
