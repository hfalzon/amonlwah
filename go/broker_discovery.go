package lhaw

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// discoverBrokerGrant finds the nearest repository grant. The access key stays
// in its file; Amon Hen validates it, including expiry, when servicing the load.
func discoverBrokerGrant(start, selector string) (string, string, error) {
	if start == "" {
		var err error
		start, err = os.Getwd()
		if err != nil {
			return "", "", err
		}
	}
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", "", err
	}
	for {
		entries, err := os.ReadDir(filepath.Join(dir, ".amonlhaw", "grants"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", "", errors.New("lhaw: repository grants are inaccessible")
		}
		var projects []string
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			project := strings.TrimSuffix(entry.Name(), ".json")
			if project == "" {
				continue
			}
			if selector != "" {
				if project == selector {
					return selector, dir, nil
				}
			} else {
				projects = append(projects, project)
			}
		}
		if selector == "" && len(projects) == 1 {
			return projects[0], dir, nil
		}
		if selector == "" && len(projects) > 1 {
			return "", "", errors.New("lhaw: multiple project grants; select one in .lhaw.json")
		}
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", "", errors.New("lhaw: repository boundary is inaccessible")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", "", nil
}
