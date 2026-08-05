package localtool

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// AuditEntry is one local tool-invocation decision record.
type AuditEntry struct {
	Tool           string `json:"tool"`
	Display        string `json:"display"`
	Decision       string `json:"decision"` // allow | deny | error
	ConversationID string `json:"conversation_id,omitempty"`
}

// Auditor records tool decisions locally.
type Auditor interface {
	Record(entry AuditEntry) error
}

// FileAudit appends JSONL entries to Path.
//
// The log stays local and stays private: entries carry absolute filesystem
// paths, so the directory is created 0o700 and the file 0o600 — owner-only.
type FileAudit struct {
	Path string
}

func (a *FileAudit) Record(entry AuditEntry) error {
	if err := os.MkdirAll(filepath.Dir(a.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(a.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}
