package kcm

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	api "k8s.io/client-go/tools/clientcmd/api"
)

func accessFixture(t *testing.T, handler http.HandlerFunc) (*Settings, string) {
	t.Helper()
	s := fixture(t)
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	path := filepath.Join(s.Root, "prod", "config")
	c := writeConfig(t, path, server.URL+"/wrong", "other", "selected")
	c.Clusters["selected"] = &api.Cluster{Server: server.URL + "/selected", InsecureSkipTLSVerify: true}
	c.AuthInfos["selected"] = &api.AuthInfo{Token: "selected-token"}
	c.Contexts["selected"].Cluster = "selected"
	c.Contexts["selected"].AuthInfo = "selected"
	data, err := clientcmd.Write(*c)
	if err != nil {
		t.Fatal(err)
	}
	if err = atomicWrite(path, data, true); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KCM_PROFILE", "prod")
	t.Setenv("KCM_CONTEXT", "selected")
	t.Setenv("KCM_FILE", path)
	t.Setenv("KUBECONFIG", "/intentionally/unused")
	return s, path
}
func assertReviewTarget(t *testing.T, r *http.Request) {
	t.Helper()
	if !strings.HasPrefix(r.URL.Path, "/selected/") || r.Header.Get("Authorization") != "Bearer selected-token" {
		t.Errorf("wrong context or identity: %s", r.URL.Path)
	}
}
func jsonResponse(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func TestPermissionsLiveAndRestricted(t *testing.T) {
	var calls atomic.Int32
	_, path := accessFixture(t, func(w http.ResponseWriter, r *http.Request) {
		assertReviewTarget(t, r)
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/selected/apis/authorization.k8s.io/v1/selfsubjectrulesreviews" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var req authv1.SelfSubjectRulesReview
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req.Spec.Namespace != "app" || req.Kind != "SelfSubjectRulesReview" {
			t.Errorf("wrong review: %+v", req)
		}
		verb := "get"
		if calls.Load() > 1 {
			verb = "patch"
		}
		jsonResponse(w, map[string]any{"status": authv1.SubjectRulesReviewStatus{ResourceRules: []authv1.ResourceRule{
			{APIGroups: []string{""}, Resources: []string{"secrets"}, ResourceNames: []string{"one-secret"}, Verbs: []string{verb}},
			{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"watch", "get", "get"}},
			{APIGroups: []string{"apps"}, Resources: []string{"deployments"}, Verbs: []string{"list"}},
			{APIGroups: []string{"*"}, Resources: []string{"pods/*"}, Verbs: []string{"get"}},
		}, NonResourceRules: []authv1.NonResourceRule{{NonResourceURLs: []string{"/healthz"}, Verbs: []string{"get"}}}}})
	})
	before, _ := os.ReadFile(path)
	originalEnv := os.Environ()
	for i := 0; i < 2; i++ {
		out, err := run(t, "permissions", "selected", "-n", "app")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Context: selected", "Namespace: app", "live; not cached", "complete for this namespace", "one-secret", "get,list,watch", "pods/*.*", "/healthz"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q: %s", want, out)
			}
		}
		if i == 1 && !strings.Contains(out, "patch") {
			t.Fatal("reused stale permission result")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("got %d requests", calls.Load())
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || !reflect.DeepEqual(originalEnv, os.Environ()) {
		t.Fatal("inspection mutated selection or source")
	}
}

func TestPermissionsIncompleteAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		code             int
		details, wantErr bool
	}{
		{"incomplete", `{"status":{"incomplete":true,"evaluationError":"external authorizer","resourceRules":[]}}`, "missing rules are unknown", 200, false, false},
		{"evaluation error", `{"status":{"evaluationError":"external authorizer"}}`, "INCOMPLETE", 200, false, false},
		{"details", `{"status":{"incomplete":true,"resourceRules":[{"verbs":["get"],"apiGroups":[""],"resources":["secrets"],"resourceNames":["one"]}]}}`, `"resourceNames"`, 200, true, false},
		{"no status", `{}`, "no status", 200, false, true},
		{"bad JSON", `invalid`, "invalid permission API response", 200, false, true},
		{"forbidden", `{"message":"review forbidden"}`, "HTTP 403", 403, false, true},
		{"expired credentials", `{"message":"expired"}`, "HTTP 401", 401, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accessFixture(t, func(w http.ResponseWriter, r *http.Request) {
				assertReviewTarget(t, r)
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			})
			args := []string{"permissions"}
			if tc.details {
				args = append(args, "--details")
			}
			out, err := run(t, args...)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v output=%s", err, out)
			}
			if err != nil {
				out += err.Error()
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("missing %q: %s", tc.want, out)
			}
		})
	}
}

func serveDiscovery(w http.ResponseWriter, r *http.Request) bool {
	core := metav1.APIResourceList{GroupVersion: "v1", APIResources: []metav1.APIResource{
		{Name: "pods", SingularName: "pod", ShortNames: []string{"po"}, Namespaced: true},
		{Name: "nodes", SingularName: "node", Namespaced: false},
	}}
	apps := metav1.APIGroup{Name: "apps", PreferredVersion: metav1.GroupVersionForDiscovery{GroupVersion: "apps/v1", Version: "v1"}}
	switch r.URL.Path {
	case "/selected/api/v1":
		jsonResponse(w, core)
	case "/selected/apis":
		jsonResponse(w, metav1.APIGroupList{Groups: []metav1.APIGroup{apps}})
	case "/selected/apis/apps":
		jsonResponse(w, apps)
	case "/selected/apis/apps/v1":
		jsonResponse(w, metav1.APIResourceList{GroupVersion: "apps/v1", APIResources: []metav1.APIResource{{Name: "deployments", SingularName: "deployment", ShortNames: []string{"deploy"}, Namespaced: true}}})
	default:
		return false
	}
	return true
}

func TestCanIActionScopeAndResults(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		args                              []string
		ns, group, resource, sub, nameArg string
		status                            authv1.SubjectAccessReviewStatus
		exit                              int
		want                              string
	}{
		{"allowed", []string{"list", "pods"}, "original", "", "pods", "", "", authv1.SubjectAccessReviewStatus{Allowed: true}, 0, "yes"},
		{"denied", []string{"delete", "deployments.apps", "-n", "app"}, "app", "apps", "deployments", "", "", authv1.SubjectAccessReviewStatus{Denied: true}, 1, "no"},
		{"no opinion", []string{"get", "pods"}, "original", "", "pods", "", "", authv1.SubjectAccessReviewStatus{}, 1, "no"},
		{"named subresource", []string{"get", "pods/my-pod", "--subresource", "log", "-n", "app"}, "app", "", "pods", "log", "my-pod", authv1.SubjectAccessReviewStatus{Allowed: true}, 0, "yes"},
		{"exec", []string{"create", "pods/my-pod", "--subresource", "exec"}, "original", "", "pods", "exec", "my-pod", authv1.SubjectAccessReviewStatus{Allowed: true}, 0, "yes"},
		{"cluster scoped", []string{"list", "nodes"}, "", "", "nodes", "", "", authv1.SubjectAccessReviewStatus{Allowed: true}, 0, "yes"},
		{"all namespaces", []string{"list", "pods", "-A"}, "", "", "pods", "", "", authv1.SubjectAccessReviewStatus{Allowed: true}, 0, "yes"},
		{"short name", []string{"get", "po"}, "original", "", "pods", "", "", authv1.SubjectAccessReviewStatus{Allowed: true}, 0, "yes"},
		{"group discovery", []string{"patch", "deployment"}, "original", "apps", "deployments", "", "", authv1.SubjectAccessReviewStatus{Allowed: true}, 0, "yes"},
		{"evaluation failed", []string{"get", "pods"}, "original", "", "pods", "", "", authv1.SubjectAccessReviewStatus{EvaluationError: "webhook unavailable"}, 2, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reviews atomic.Int32
			_, path := accessFixture(t, func(w http.ResponseWriter, r *http.Request) {
				assertReviewTarget(t, r)
				if serveDiscovery(w, r) {
					return
				}
				if r.URL.Path != "/selected/apis/authorization.k8s.io/v1/selfsubjectaccessreviews" || r.Method != "POST" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				reviews.Add(1)
				var request authv1.SelfSubjectAccessReview
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				want := &authv1.ResourceAttributes{Verb: tc.args[0], Namespace: tc.ns, Group: tc.group, Resource: tc.resource, Subresource: tc.sub, Name: tc.nameArg}
				if !reflect.DeepEqual(request.Spec.ResourceAttributes, want) {
					t.Errorf("got %+v want %+v", request.Spec.ResourceAttributes, want)
				}
				jsonResponse(w, map[string]any{"status": tc.status})
			})
			before, _ := os.ReadFile(path)
			out, err := run(t, append([]string{"can-i"}, tc.args...)...)
			code := 0
			if err != nil {
				var e *ExitError
				if !errors.As(err, &e) {
					t.Fatal(err)
				}
				code = e.Code
			}
			if code != tc.exit || strings.TrimSpace(out) != tc.want || reviews.Load() != 1 {
				t.Fatalf("code=%d out=%q reviews=%d err=%v", code, out, reviews.Load(), err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("check modified config")
			}
		})
	}
}

func TestAccessResolutionAndOfflineCommands(t *testing.T) {
	var calls atomic.Int32
	s, path := accessFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "unexpected network", 500) })
	for _, args := range [][]string{{"contexts"}, {"profiles"}, {"status"}, {"prompt"}, {"permissions", "--help"}, {"can-i", "--help"}} {
		if _, err := run(t, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"permissions", "missing"}, {"permissions", "selected", "--file", filepath.Join(s.Root, "qa", "config")},
		{"permissions", "--request-timeout", "0"}, {"permissions", "-n", "Invalid_Name"},
		{"can-i", "get", "pods", "-A", "-n", "app"}, {"can-i", "get", "pods/a/b"}, {"can-i", "get", "pods", "--subresource", "a/b"},
	} {
		if _, err := run(t, args...); err == nil {
			t.Fatalf("expected error: %v", args)
		}
	}
	// A named context does not borrow the selected file for disambiguation.
	writeConfig(t, filepath.Join(s.Root, "prod", "duplicate"), "https://fixture.invalid", "selected")
	if _, err := run(t, "permissions", "selected"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("duplicate: %v", err)
	}
	t.Setenv("KCM_CONTEXT", "")
	t.Setenv("KCM_FILE", "")
	if _, err := run(t, "permissions"); err == nil {
		t.Fatal("missing selection accepted")
	}
	t.Setenv("KCM_PROFILE", "local")
	if _, err := run(t, "permissions", "selected", "--file", path); err == nil {
		t.Fatal("inspected another profile")
	}
	if calls.Load() != 0 {
		t.Fatalf("offline commands/invalid requests made %d requests", calls.Load())
	}
}

func TestExplicitInspectionDoesNotSwitch(t *testing.T) {
	var calls atomic.Int32
	_, path := accessFixture(t, func(w http.ResponseWriter, r *http.Request) {
		assertReviewTarget(t, r)
		calls.Add(1)
		if serveDiscovery(w, r) {
			return
		}
		if strings.HasSuffix(r.URL.Path, "selfsubjectrulesreviews") {
			jsonResponse(w, map[string]any{"status": authv1.SubjectRulesReviewStatus{}})
		} else {
			jsonResponse(w, map[string]any{"status": authv1.SubjectAccessReviewStatus{Allowed: true}})
		}
	})
	t.Setenv("KCM_CONTEXT", "other")
	before, _ := os.ReadFile(path)
	for _, args := range [][]string{{"permissions", "selected", "--file", path}, {"can-i", "get", "pods", "--context", "selected", "--file", path}} {
		if _, err := run(t, args...); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("unexpected requests: %d", calls.Load())
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || os.Getenv("KCM_CONTEXT") != "other" {
		t.Fatal("inspection switched context")
	}
}

func TestCanIFailuresAreNotDenials(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{"authentication", `{"message":"expired credentials"}`, 401},
		{"review forbidden", `{"message":"review not permitted"}`, 403},
		{"missing status", `{}`, 200},
		{"missing decision", `{"status":{}}`, 200},
		{"contradictory", `{"status":{"allowed":true,"denied":true}}`, 200},
		{"allowed with evaluation error", `{"status":{"allowed":true,"evaluationError":"partial failure"}}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accessFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if serveDiscovery(w, r) {
					return
				}
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			})
			out, err := run(t, "can-i", "get", "pods")
			var status *ExitError
			if !errors.As(err, &status) || status.Code != 2 || strings.TrimSpace(out) != "" {
				t.Fatalf("out=%q err=%v", out, err)
			}
		})
	}
	// Invalid flags and argument counts also distinguish errors from a denial.
	for _, args := range [][]string{{"can-i"}, {"can-i", "get", "pods", "--unknown"}} {
		_, err := run(t, args...)
		var status *ExitError
		if !errors.As(err, &status) || status.Code != 2 {
			t.Fatalf("args=%v err=%v", args, err)
		}
	}
}

func TestAccessTimeoutAndDefaultNamespace(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		accessFixture(t, func(w http.ResponseWriter, r *http.Request) { time.Sleep(100 * time.Millisecond) })
		out, err := run(t, "permissions", "--request-timeout", "30ms")
		var timeout net.Error
		if !errors.As(err, &timeout) || !timeout.Timeout() || out != "" {
			t.Fatalf("out=%q err=%v", out, err)
		}
	})
	t.Run("default namespace", func(t *testing.T) {
		_, path := accessFixture(t, func(w http.ResponseWriter, r *http.Request) {
			var req authv1.SelfSubjectRulesReview
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Spec.Namespace != "default" {
				t.Errorf("got namespace %q", req.Spec.Namespace)
			}
			jsonResponse(w, map[string]any{"status": authv1.SubjectRulesReviewStatus{}})
		})
		c, err := readConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		c.Contexts["selected"].Namespace = ""
		b, err := clientcmd.Write(*c)
		if err != nil {
			t.Fatal(err)
		}
		if err = atomicWrite(path, b, true); err != nil {
			t.Fatal(err)
		}
		out, err := run(t, "permissions")
		if err != nil || !strings.Contains(out, "Namespace: default") {
			t.Fatalf("out=%q err=%v", out, err)
		}
	})
}
