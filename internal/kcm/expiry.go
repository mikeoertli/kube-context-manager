package kcm

import (
	"io"
	"path/filepath"
)

// Resolve only at expiry, so prompt updates remain free of subprocesses and
// deleted or edited kubeconfigs are never restored from cached shell state.
func (s *Settings) expireToLocal(out io.Writer) error {
	var selected *Context
	if s.AutoRevertLocal {
		contexts, err := s.contexts("local")
		if err != nil {
			return err
		}
		// Discovery sorts by context name, then source path. Waivers can allow
		// remote entries in the root, but those are not automatic local targets.
		for i := range contexts {
			c := &contexts[i]
			if !s.local(c.Server) {
				continue
			}
			if selected == nil || filepath.Base(c.File) == "config" {
				selected = c
			}
			if filepath.Base(c.File) == "config" {
				break
			}
		}
	}
	reset(out, "local")
	emit(out, "KCM_SETTINGS", s.Path)
	if selected != nil {
		emit(out, "KCM_CONTEXT", selected.Name)
		emit(out, "KCM_FILE", selected.File)
		emit(out, "KUBECONFIG", selected.File)
	}
	s.emitPromptMetadata(out, "local")
	return nil
}
