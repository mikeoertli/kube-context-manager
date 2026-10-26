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
		var err error
		selected, err = s.defaultLocalContext()
		if err != nil {
			return err
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

func (s *Settings) defaultLocalContext() (*Context, error) {
	contexts, err := s.contexts("local")
	if err != nil {
		return nil, err
	}
	var selected *Context
	// Stable discovery order: context name, then source path. Waived remote
	// endpoints must never be selected by reset or automatic expiry recovery.
	for i := range contexts {
		c := &contexts[i]
		if !s.local(c.Server) {
			continue
		}
		if selected == nil {
			selected = c
		}
		if filepath.Base(c.File) == "config" {
			return c, nil
		}
	}
	return selected, nil
}
