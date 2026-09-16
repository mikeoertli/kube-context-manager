package kcm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
	api "k8s.io/client-go/tools/clientcmd/api"
)

type Context struct {
	Name      string
	File      string
	Server    string
	Namespace string
}

func discoverableFile(name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pem", ".crt", ".key", ".bak":
		return false
	}
	return true
}

func readConfig(path string) (*api.Config, error) {
	c, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(c.Contexts) == 0 {
		return nil, fmt.Errorf("%s: no contexts", path)
	}
	for name, ctx := range c.Contexts {
		if err := validName(name); err != nil {
			return nil, err
		}
		if ctx == nil || c.Clusters[ctx.Cluster] == nil {
			return nil, fmt.Errorf("%s: context %q references a missing cluster", path, name)
		}
		if ctx.AuthInfo != "" && c.AuthInfos[ctx.AuthInfo] == nil {
			return nil, fmt.Errorf("%s: context %q references a missing user", path, name)
		}
		if err := validName(c.Clusters[ctx.Cluster].Server); err != nil {
			return nil, fmt.Errorf("%s: invalid server", path)
		}
	}
	return c, nil
}

func scanDir(dir string) ([]Context, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Context
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !discoverableFile(name) {
			continue
		}
		path := filepath.Join(dir, name)
		if _, err := expandPath(path); err != nil {
			return nil, fmt.Errorf("unsupported kubeconfig filename %q: %w", path, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if !within(path, dir) {
			return nil, fmt.Errorf("%s: symlink points outside profile directory", path)
		}
		c, err := readConfig(path)
		if err != nil {
			return nil, err
		}
		for n, ctx := range c.Contexts {
			out = append(out, Context{n, path, c.Clusters[ctx.Cluster].Server, ctx.Namespace})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].File < out[j].File
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

type Waiver struct {
	Context     string `json:"context"`
	File        string `json:"file"`
	Server      string `json:"server"`
	Fingerprint string `json:"fingerprint"`
}

func fingerprint(c Context) string {
	h := sha256.Sum256([]byte(c.Name + "\x00" + canonical(c.File) + "\x00" + c.Server))
	return hex.EncodeToString(h[:])
}

func (s *Settings) waivers() ([]Waiver, error) {
	b, err := os.ReadFile(filepath.Join(s.Root, ".kcm_waivers"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var w []Waiver
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, fmt.Errorf(".kcm_waivers: %w", err)
	}
	return w, nil
}

func (s *Settings) validateRoot() error {
	contexts, err := scanDir(s.Root)
	if err != nil {
		return err
	}
	waivers, err := s.waivers()
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, w := range waivers {
		allowed[w.Fingerprint] = true
	}
	var bad []string
	for _, c := range contexts {
		if !s.local(c.Server) && !allowed[fingerprint(c)] {
			bad = append(bad, fmt.Sprintf("%q (%s)", c.Name, c.File))
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("non-local contexts in kube root: %s; move into a profile or use kcm waive <context>", strings.Join(bad, ", "))
	}
	return nil
}

func (s *Settings) contexts(profile string) ([]Context, error) {
	if err := s.validateRoot(); err != nil {
		return nil, err
	}
	d, err := s.dir(profile)
	if err != nil {
		return nil, err
	}
	return scanDir(d)
}

func selectContext(contexts []Context, name, file string) (Context, error) {
	var found []Context
	for _, c := range contexts {
		if c.Name == name && (file == "" || canonical(c.File) == canonical(file)) {
			found = append(found, c)
		}
	}
	if len(found) == 0 {
		return Context{}, fmt.Errorf("context %q not found in this profile", name)
	}
	if len(found) > 1 {
		return Context{}, fmt.Errorf("context %q is ambiguous; use --file or the switcher", name)
	}
	return found[0], nil
}

// atomicWrite publishes a fully written, mode-0600 file. Exclusive publication
// uses a hard link so another process cannot win a check-then-rename race.
func atomicWrite(path string, b []byte, replace bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".kcm-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if replace {
		return os.Rename(f.Name(), path)
	}
	if err = os.Link(f.Name(), path); err != nil {
		return fmt.Errorf("install %s (will not overwrite): %w", path, err)
	}
	return nil
}
