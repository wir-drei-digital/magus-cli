package api

import "fmt"

// Error is a typed API error returned by all client methods on non-2xx
// responses. The Code and Message map directly to the API's
// {"error":{"code","message","details"}} JSON shape.
type Error struct {
	Status  int
	Code    string
	Message string
	Details any
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("api error: %s (%d)", e.Code, e.Status)
	}
	return fmt.Sprintf("api error: %s: %s", e.Code, e.Message)
}

func (e *Error) Is401() bool { return e.Status == 401 }
func (e *Error) Is403() bool { return e.Status == 403 }
func (e *Error) Is404() bool { return e.Status == 404 }
