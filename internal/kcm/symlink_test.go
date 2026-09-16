package kcm

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	authv1 "k8s.io/api/authorization/v1"
)

func configLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestSymlinkDiscoveryAndSelectionUseLink(t *testing.T) {
	s := fixture(t)
	target := filepath.Join(t.TempDir(), "repo-config")
	writeConfig(t, target, "https://repo.invalid", "shared")
	first := filepath.Join(s.Root, "dev", "first")
	second := filepath.Join(s.Root, "dev", "second")
	configLink(t, target, first)
	relative, err := filepath.Rel(filepath.Dir(second), target)
	if err != nil {
		t.Fatal(err)
	}
	configLink(t, relative, second)
	cs, err := s.contexts("dev")
	if err != nil || len(cs) != 2 {
		t.Fatalf("discovery: %+v %v", cs, err)
	}
	c, err := selectContext(cs, "shared", second)
	if err != nil || c.File != second {
		t.Fatalf("link disambiguation: %+v %v", c, err)
	}
	if _, err = selectContext(cs, "shared", target); err == nil {
		t.Fatal("target path matched profile entries")
	}
	t.Setenv("KCM_PROFILE", "dev")
	out, err := run(t, "context", "shared", "--file", second)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"KCM_FILE", "KUBECONFIG"} {
		if !strings.Contains(out, "export "+key+"="+quote(second)) {
			t.Fatalf("lost link path: %s", out)
		}
	}
	t.Setenv("KCM_CONTEXT", "shared")
	t.Setenv("KCM_FILE", second)
	t.Setenv("KUBECONFIG", second)
	out, err = run(t, "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "*>  shared") != 1 || strings.Contains(out, target) {
		t.Fatalf("wrong selection markers or displayed path:\n%s", out)
	}
	if _, err = run(t, "ns", "updated"); err != nil {
		t.Fatal(err)
	}
	if link, err := os.Readlink(second); err != nil || link != relative {
		t.Fatalf("namespace edit replaced symlink: %q %v", link, err)
	}
	updated, err := readConfig(target)
	if err != nil || updated.Contexts["shared"].Namespace != "updated" {
		t.Fatalf("namespace not written to target: %+v %v", updated, err)
	}
}

func TestSymlinkTimeoutUsesPlacement(t *testing.T) {
	s := fixture(t)
	prodTarget := filepath.Join(s.Root, "prod", "actual")
	writeConfig(t, prodTarget, "https://prod.invalid", "remote")
	devLink := filepath.Join(s.Root, "dev", "from-prod")
	configLink(t, prodTarget, devLink)
	if got := s.timeout("dev", devLink); got != 0 {
		t.Fatalf("followed production target: %v", got)
	}
	external := filepath.Join(t.TempDir(), "repo-config")
	writeConfig(t, external, "https://repo.invalid", "repo")
	prodLink := filepath.Join(s.Root, "prod", "from-repo")
	configLink(t, external, prodLink)
	s.Profiles["support"] = Profile{Dir: "prod"}
	if got := s.timeout("support", prodLink); got != 8*time.Hour {
		t.Fatalf("lost prod placement timeout: %v", got)
	}
	disabled := "0"
	s.Profiles["support"] = Profile{Dir: "prod", Timeout: &disabled}
	if got := s.timeout("support", prodLink); got != 0 {
		t.Fatalf("ignored profile timeout override: %v", got)
	}
}

func TestSymlinkRootWaiversUseLink(t *testing.T) {
	s := fixture(t)
	target := filepath.Join(t.TempDir(), "repo-config")
	writeConfig(t, target, "https://remote.invalid", "remote")
	first := filepath.Join(s.Root, "first")
	second := filepath.Join(s.Root, "second")
	configLink(t, target, first)
	configLink(t, target, second)
	if err := s.validateRoot(); err == nil {
		t.Fatal("remote root links bypassed validation")
	}
	if _, err := run(t, "waive", "remote", "--file", first); err != nil {
		t.Fatal(err)
	}
	ws, err := s.waivers()
	if err != nil || len(ws) != 1 || ws[0].File != first {
		t.Fatalf("waiver not attached to link: %+v %v", ws, err)
	}
	if err := s.validateRoot(); err == nil {
		t.Fatal("waiver leaked to another link")
	}
	if _, err := run(t, "waive", "remote", "--file", second); err != nil {
		t.Fatal(err)
	}
	if err := s.validateRoot(); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "unwaive", "remote", "--file", first); err != nil {
		t.Fatal(err)
	}
	ws, err = s.waivers()
	if err != nil || len(ws) != 1 || ws[0].File != second {
		t.Fatalf("unwaive affected wrong link: %+v %v", ws, err)
	}
}

func TestSymlinkBrokenAndDirectoryTargets(t *testing.T) {
	s := fixture(t)
	configLink(t, t.TempDir(), filepath.Join(s.Root, "directory-link"))
	if cs, err := s.contexts("local"); err != nil || len(cs) != 0 {
		t.Fatalf("recursed through directory link: %+v %v", cs, err)
	}
	broken := filepath.Join(s.Root, "broken")
	configLink(t, filepath.Join(t.TempDir(), "missing"), broken)
	if _, err := s.contexts("local"); err == nil || !strings.Contains(err.Error(), broken) {
		t.Fatalf("broken link not reported: %v", err)
	}
}

func TestSymlinkPermissionChecksUseSelectedConfig(t *testing.T) {
	s, target := accessFixture(t, func(w http.ResponseWriter, r *http.Request) {
		assertReviewTarget(t, r)
		if serveDiscovery(w, r) {
			return
		}
		if strings.HasSuffix(r.URL.Path, "selfsubjectrulesreviews") {
			jsonResponse(w, map[string]any{"status": authv1.SubjectRulesReviewStatus{}})
		} else {
			jsonResponse(w, map[string]any{"status": authv1.SubjectAccessReviewStatus{Allowed: true}})
		}
	})
	link := filepath.Join(s.Root, "dev", "repo-link")
	configLink(t, target, link)
	t.Setenv("KCM_PROFILE", "dev")
	t.Setenv("KCM_FILE", link)
	out, err := run(t, "permissions")
	if err != nil || !strings.Contains(out, "Config: "+link) {
		t.Fatalf("permission summary lost link: %v %s", err, out)
	}
	out, err = run(t, "can-i", "get", "pods")
	if err != nil || out != "yes\n" {
		t.Fatalf("access check: %v %s", err, out)
	}
}
