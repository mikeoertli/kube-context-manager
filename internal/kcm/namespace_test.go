package kcm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNamespaceOperations(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		args                           []string
		createFails, listFails, cancel bool
		want                           string
		wantErr                        bool
		calls                          int
	}{
		{"set without API", []string{"ns", "existing"}, false, false, false, "existing", false, 0},
		{"create and switch", []string{"ns", "fresh-demo", "--create"}, false, false, false, "fresh-demo", false, 1},
		{"creation rejected", []string{"ns", "fresh-demo", "--create"}, true, false, false, "original", true, 1},
		{"invalid name", []string{"ns", "Invalid_Name", "--create"}, false, false, false, "original", true, 0},
		{"picker existing", []string{"ns"}, false, false, false, "default", false, 1},
		{"picker cancelled", []string{"ns"}, false, false, true, "original", true, 1},
		{"list rejected", []string{"ns"}, false, true, false, "original", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture(t)
			path := filepath.Join(s.Root, "prod", "config")
			writeConfig(t, path, "https://fixture.invalid", "other", "selected")
			t.Setenv("KCM_PROFILE", "prod")
			t.Setenv("KCM_CONTEXT", "selected")
			t.Setenv("KCM_FILE", path)
			bin := t.TempDir()
			log := filepath.Join(t.TempDir(), "calls")
			t.Setenv("KCM_TEST_KUBECTL_LOG", log)
			kubectl := `#!/bin/sh
printf '%s\n' CALL "$@" >> "$KCM_TEST_KUBECTL_LOG"
if [ "$5" = get ]; then
    [ "$KCM_TEST_LIST_FAIL" = yes ] && exit 1
    printf 'default\nkube-system\n'
elif [ "$5" = create ]; then
    [ "$KCM_TEST_CREATE_FAIL" = yes ] && exit 1
    printf 'namespace/%s created\n' "$7"
else
    exit 2
fi
`
			if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(kubectl), 0700); err != nil {
				t.Fatal(err)
			}
			fzf := "#!/bin/sh\nprintf '0\\tdefault\\n'\n"
			if tc.cancel {
				fzf = "#!/bin/sh\nexit 130\n"
			}
			if err := os.WriteFile(filepath.Join(bin, "fzf"), []byte(fzf), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("KCM_TEST_CREATE_FAIL", "")
			t.Setenv("KCM_TEST_LIST_FAIL", "")
			if tc.createFails {
				t.Setenv("KCM_TEST_CREATE_FAIL", "yes")
			}
			if tc.listFails {
				t.Setenv("KCM_TEST_LIST_FAIL", "yes")
			}
			before, _ := os.ReadFile(path)
			_, err := run(t, tc.args...)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, tc.wantErr)
			}
			c, err := readConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			if c.Contexts["selected"].Namespace != tc.want || c.Contexts["other"].Namespace != "original" || c.CurrentContext != "other" {
				t.Fatalf("wrong context modified: %+v", c)
			}
			if tc.wantErr {
				after, _ := os.ReadFile(path)
				if !bytes.Equal(before, after) {
					t.Fatal("failed operation modified kubeconfig")
				}
			}
			calls, _ := os.ReadFile(log)
			if strings.Count(string(calls), "CALL\n") != tc.calls {
				t.Fatalf("unexpected API calls: %s", calls)
			}
			if tc.calls > 0 && !strings.Contains(string(calls), "--kubeconfig\n"+path+"\n--context\nselected\n") {
				t.Fatalf("API call not pinned to selection: %s", calls)
			}
			if strings.Contains(strings.Join(tc.args, " "), "--create") && tc.calls > 0 && !strings.Contains(string(calls), "create\nnamespace\nfresh-demo\n") {
				t.Fatalf("wrong create arguments: %s", calls)
			}
		})
	}
}
