package localtool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// AuditEntry is one local tool-invocation decision record.
type AuditEntry struct {
	Tool           string    `json:"tool"`
	Display        string    `json:"display"`
	Decision       string    `json:"decision"` // allow | deny | error
	TS             time.Time `json:"ts"`
	ConversationID string    `json:"conversation_id,omitempty"`
}

// Auditor records tool decisions locally.
type Auditor interface {
	Record(entry AuditEntry) error
}

// FileAudit appends JSONL entries to Path.
//
// The log stays local and stays private: entries carry absolute filesystem
// paths, so every Record forces the log file to 0o600 — owner-only — and
// creates any missing parent directory 0o700. The file mode is enforced on
// every write, not just at creation, because OpenFile's perm argument applies
// only when it creates the file and would leave a pre-existing (or
// umask-widened) log readable by others. A parent directory that already
// exists keeps whatever mode it has.
type FileAudit struct {
	Path string
}

func (a *FileAudit) Record(entry AuditEntry) (err error) {
	// Every line carries a timestamp. A caller-supplied TS is kept verbatim so
	// callers can pin it; otherwise Record stamps UTC now.
	if entry.TS.IsZero() {
		entry.TS = time.Now().UTC()
	}

	if err := os.MkdirAll(filepath.Dir(a.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(a.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	// Report close failures: bytes can fail to reach the filesystem at close
	// time (EDQUOT, NFS flush), and an audit write that silently vanished must
	// not be reported as recorded. A write error already in flight wins.
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()

	// Defeat a permissive umask and tighten a log that already existed with
	// wider perms. The audit log is a security artifact, so failing to secure
	// it is a Record failure, not a warning.
	if err := os.Chmod(a.Path, 0o600); err != nil {
		return err
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}
