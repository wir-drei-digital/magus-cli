package localtool

// Plan is the validated, confined, client-canonical action. Display is what the
// approval prompt shows (anti-spoofing: derived purely client-side); MatchPath
// is what allowlist rules match against.
type Plan struct {
	Tool      string
	Tier      string
	Display   string
	MatchPath string

	path string // internal: resolved path for Execute
}

// Tool is a locally-executable capability the cloud may propose.
type Tool interface {
	Name() string
	Tier() string
	Validate(params map[string]any) error
	Plan(params map[string]any) (Plan, error)
	Execute(plan Plan) (any, error)
}

// Registry maps advertised tool names to their implementations.
type Registry map[string]Tool

func (r Registry) Names() []string {
	names := make([]string, 0, len(r))
	for n := range r {
		names = append(names, n)
	}
	return names
}
