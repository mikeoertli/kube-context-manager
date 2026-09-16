package kcm

import (
	_ "embed"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/pelletier/go-toml/v2"
)

//go:embed defaults.toml
var DefaultSettings []byte

type Profile struct {
	Dir     string  `toml:"dir"`
	Emoji   string  `toml:"emoji"`
	Timeout *string `toml:"timeout"`
}

type Settings struct {
	Root       string             `toml:"kube_root"`
	LocalHosts []string           `toml:"local_hosts"`
	LocalCIDRs []string           `toml:"local_cidrs"`
	Profiles   map[string]Profile `toml:"profiles"`
	Path       string             `toml:"-"`
}

func settingsPath() string {
	if p := os.Getenv("KCM_SETTINGS"); p != "" {
		return p
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "kcm", "kcm_settings.toml")
}

func expandPath(s string) (string, error) {
	if s == "~" || strings.HasPrefix(s, "~/") {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if s == "~" {
			s = h
		} else {
			s = filepath.Join(h, strings.TrimPrefix(s, "~/"))
		}
	}
	if strings.ContainsAny(s, ":\n\r\x00") {
		return "", fmt.Errorf("path cannot contain colon or control characters")
	}
	return filepath.Abs(s)
}

func loadSettings(path string) (*Settings, error) {
	s := &Settings{}
	if err := toml.Unmarshal(DefaultSettings, s); err != nil {
		return nil, err
	}
	path, err := expandPath(path)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		// Overlay profile fields, preserving built-in directories and expiry defaults.
		if err = toml.NewDecoder(strings.NewReader(string(b))).DisallowUnknownFields().Decode(s); err != nil {
			return nil, fmt.Errorf("settings: %w", err)
		}
	}
	s.Path = path
	s.Root, err = expandPath(s.Root)
	if err != nil {
		return nil, err
	}
	if p, ok := s.Profiles["local"]; !ok || p.Dir != "." {
		return nil, fmt.Errorf("local profile dir must be '.'")
	}
	for name, p := range s.Profiles {
		if err := validName(name); err != nil {
			return nil, fmt.Errorf("profile: %w", err)
		}
		if p.Dir == "" {
			return nil, fmt.Errorf("profile %q needs dir", name)
		}
		if _, err := s.dir(name); err != nil {
			return nil, err
		}
		if p.Timeout != nil {
			d, err := time.ParseDuration(*p.Timeout)
			if err != nil || d < 0 || (d > 0 && d < time.Second) {
				return nil, fmt.Errorf("profile %q timeout must be 0 or a duration of at least 1s", name)
			}
		}
	}
	for _, c := range s.LocalCIDRs {
		if _, _, err := net.ParseCIDR(c); err != nil {
			return nil, fmt.Errorf("local_cidrs: %w", err)
		}
	}
	return s, nil
}

func (s *Settings) names() []string {
	names := make([]string, 0, len(s.Profiles))
	for n := range s.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (s *Settings) dir(name string) (string, error) {
	p, ok := s.Profiles[name]
	if !ok {
		return "", fmt.Errorf("unknown profile %q", name)
	}
	d := p.Dir
	if !filepath.IsAbs(d) && !strings.HasPrefix(d, "~") {
		d = filepath.Join(s.Root, d)
	}
	return expandPath(d)
}

// configPath identifies a configured path without resolving symlinks. Profile
// membership, selection, and waivers belong to the link, not its target.
func configPath(p string) string {
	if absolute, err := filepath.Abs(p); err == nil {
		return absolute
	}
	return filepath.Clean(p)
}

// canonical resolves physical targets for writes and import collision checks.
func canonical(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(configPath(dir), configPath(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Settings) timeout(profile, file string) time.Duration {
	p := s.Profiles[profile]
	if p.Timeout != nil {
		d, _ := time.ParseDuration(*p.Timeout)
		return d
	}
	if profile == "prod" || within(file, filepath.Join(s.Root, "prod")) {
		return 8 * time.Hour
	}
	return 0
}

func (s *Settings) local(server string) bool {
	u, err := url.Parse(server)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return false
	}
	h := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	if ip != nil && ip.IsLoopback() {
		return true
	}
	for _, v := range s.LocalHosts {
		if strings.EqualFold(strings.TrimSuffix(v, "."), h) {
			return true
		}
	}
	for _, c := range s.LocalCIDRs {
		_, block, _ := net.ParseCIDR(c)
		if ip != nil && block != nil && block.Contains(ip) {
			return true
		}
	}
	return false
}

func validName(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("name cannot be empty")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return fmt.Errorf("name contains control characters")
		}
	}
	return nil
}
