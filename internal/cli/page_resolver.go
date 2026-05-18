package cli

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/wir-drei-digital/magus-cli/internal/api"
	"github.com/wir-drei-digital/magus-cli/internal/config"
)

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// resolvePage takes a user-supplied page reference and returns the page UUID.
//
// Accepted forms:
//   - UUID (returned as-is)
//   - "<brain>/<page-slug>" where brain is an id-or-slug
//   - "<page-slug>" uses the active brain (errors if none)
func resolvePage(ctx context.Context, c *api.Client, ref string) (string, error) {
	if uuidRE.MatchString(ref) {
		return ref, nil
	}

	var brainRef, slug string
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		brainRef = ref[:i]
		slug = ref[i+1:]
	} else {
		cfg, err := config.Load()
		if err != nil {
			return "", err
		}
		brainRef = config.ResolveActiveBrain(cfg, "")
		if brainRef == "" {
			return "", fmt.Errorf("page slug %q given without an active brain; pass <brain>/<page-slug> or run `magus brain use <id>`", ref)
		}
		slug = ref
	}

	page, err := c.GetPageBySlug(ctx, brainRef, slug)
	if err != nil {
		return "", err
	}
	return page.ID, nil
}
